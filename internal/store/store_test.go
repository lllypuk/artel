package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func seed(t *testing.T, s *Store) (domain.Project, domain.Task) {
	t.Helper()
	p := domain.Project{ID: uuid.NewString(), RootPath: "/test", Name: "test", Version: 1, QueueVersion: 1}
	task := domain.Task{ID: uuid.NewString(), ProjectID: p.ID, Title: "test", State: domain.Backlog, Version: 1}
	_, err := s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
		snap.Projects = append(snap.Projects, p)
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[p.ID] = []string{}
		return Result{Status: 201, Body: []byte(`{}`)}, &Event{Action: "seed", ProjectID: p.ID, TaskID: task.ID, Timestamp: "2026-11-17T00:00:00Z", Changes: []Change{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, task
}

func TestSQLitePragmasAndConstraints(t *testing.T) {
	s := openTest(t)
	p, task := seed(t, s)
	for _, pair := range []struct {
		name string
		want int
	}{{"foreign_keys", 1}, {"busy_timeout", 5000}, {"synchronous", 2}, {"user_version", 1}} {
		var got int
		if err := s.db.QueryRowContext(t.Context(), "PRAGMA "+pair.name).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != pair.want {
			t.Fatalf("%s=%d", pair.name, got)
		}
	}
	var mode string
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL: %s %v", mode, err)
	}
	invalid := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO projects(id,root_path,archived,record) VALUES('duplicate','/test',0,'{}')", nil},
		{"INSERT INTO tasks(id,project_id,state,record) VALUES('orphan','missing','backlog','{}')", nil},
		{"INSERT INTO queue_entries(project_id,position,task_id) VALUES(?,-1,?)", []any{p.ID, task.ID}},
		{"INSERT INTO queue_entries(project_id,position,task_id) VALUES(?,0,'missing')", []any{p.ID}},
		{"UPDATE tasks SET state='invalid' WHERE id=?", []any{task.ID}},
		{"DELETE FROM projects WHERE id=?", []any{p.ID}},
		{"INSERT INTO events(action,project_id,source,timestamp,changes) VALUES('bad',?,'agent','now','[]')", []any{p.ID}},
	}
	for _, q := range invalid {
		if _, err := s.db.ExecContext(t.Context(), q.query, q.args...); err == nil {
			t.Fatalf("constraint missing: %s", q.query)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE tasks SET state='in_progress' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO tasks(id,project_id,state,record) VALUES('second',?,'in_progress','{}')", p.ID); err == nil {
		t.Fatal("two in_progress rows accepted")
	}
	// Composite queue FK rejects a real task owned by a different project.
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO projects(id,root_path,archived,record) VALUES('other','/other',0,'{}')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO queue_entries(project_id,position,task_id) VALUES('other',0,?)", task.ID); err == nil {
		t.Fatal("cross-project queue FK missing")
	}
}

func TestRollbackAtEveryMutationStage(t *testing.T) {
	for _, stage := range []string{"callback", "persist", "event", "receipt"} {
		t.Run(stage, func(t *testing.T) {
			s := openTest(t)
			p, _ := seed(t, s)
			if stage == "receipt" {
				if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER fail_receipt BEFORE INSERT ON mutation_receipts BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var receipts int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM mutation_receipts").Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			_, err = s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
				snap.Projects[0].Name = "must roll back"
				e := &Event{Action: "edit", ProjectID: p.ID, Timestamp: "now", Changes: []Change{}}
				switch stage {
				case "callback":
					return Result{}, nil, errors.New("injected callback failure")
				case "persist":
					snap.Tasks = append(snap.Tasks, domain.Task{ID: "bad", ProjectID: "missing", State: domain.Backlog})
				case "event":
					e.TaskID = "missing"
				}
				return Result{Status: 200, Body: []byte(`{}`)}, e, nil
			})
			if err == nil {
				t.Fatal("injection did not fail")
			}
			after, err := s.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partial data/event committed")
			}
			var got int
			if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM mutation_receipts").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != receipts {
				t.Fatal("failed operation left receipt")
			}
		})
	}
}

func TestRejectUnknownAndFutureDatabases(t *testing.T) {
	for _, schema := range []string{
		"CREATE TABLE legacy_messages(id INTEGER)",
		"PRAGMA user_version=999",
		"PRAGMA user_version=1; CREATE TABLE metadata(id INTEGER,instance_id TEXT)",
	} {
		path := filepath.Join(t.TempDir(), "unknown.sqlite")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), schema); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		// #nosec G304 -- Test-owned incompatible database fixture.
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := Open(t.Context(), path)
		if err == nil {
			_ = s.Close()
			t.Fatal("foreign/future database accepted")
		}
		// #nosec G304 -- Same test-owned incompatible database fixture.
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("foreign database modified during schema rejection")
		}
		// Failure never deletes or recreates an incompatible database.
		db, err = sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		var check string
		if err := db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&check); err != nil || check != "ok" {
			t.Fatalf("database damaged: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefuseDatabaseSymlinksAndSpecialFiles(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		path := filepath.Join(t.TempDir(), "db")
		target := filepath.Join(t.TempDir(), "untouched")
		if err := os.WriteFile(target, []byte("sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path+suffix); err != nil {
			t.Fatal(err)
		}
		s, err := Open(t.Context(), path)
		if err == nil {
			_ = s.Close()
			t.Fatal("database symlink accepted")
		}
		// #nosec G304 -- Sentinel in a private test temporary directory.
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "sentinel" {
			t.Fatal("followed database symlink")
		}
	}
	path := filepath.Join(t.TempDir(), "db")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(t.Context(), path); err == nil {
		_ = s.Close()
		t.Fatal("directory accepted as database")
	}
}

func TestMigrationAtomicityAndPersistentIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.sqlite")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if s, err := Open(ctx, path); err == nil {
		_ = s.Close()
		t.Fatal("cancelled migration succeeded")
	}
	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = seed(t, first)
	before, err := first.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}()
	after, err := second.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		b, _ := json.Marshal(before)
		a, _ := json.Marshal(after)
		t.Fatalf("reopen mismatch %s %s", b, a)
	}
	if _, err := uuid.Parse(after.InstanceID); err != nil {
		t.Fatal(err)
	}
	other := openTest(t)
	snap, err := other.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snap.InstanceID == after.InstanceID {
		t.Fatal("different databases share identity")
	}
}
