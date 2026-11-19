package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// This historical fixture must not track either production schema file.
//
//go:embed testdata/v1.sql
var v1Schema string

func populatedV1(t *testing.T, path string) (Snapshot, []Event, []byte) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), v1Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata(id,instance_id) VALUES(1,?)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	p, task := seed(t, old)
	if _, err := db.ExecContext(t.Context(), "UPDATE tasks SET state='queued' WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	task.State = "queued"
	record, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE tasks SET record=? WHERE id=?", string(record), task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO queue_entries(project_id,position,task_id) VALUES(?,0,?)", p.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	snap, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := old.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	receipts := receiptDump(t, old)
	return snap, events, receipts
}
func receiptDump(t *testing.T, s *Store) []byte {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "SELECT key,method,path,payload_hash,status,body FROM mutation_receipts ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var all []any
	for rows.Next() {
		var key, method, path, hash string
		var status int
		var body []byte
		if err := rows.Scan(&key, &method, &path, &hash, &status, &body); err != nil {
			t.Fatal(err)
		}
		all = append(all, []any{key, method, path, hash, status, body})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestObserverV1UpgradePreservesPopulatedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	snap, events, receipts := populatedV1(t, path)
	for range 2 {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		history, err := s.Events(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snap, after) || !reflect.DeepEqual(events, history) || string(receipts) != string(receiptDump(t, s)) {
			t.Fatal("v1 domain data/identity/receipt changed")
		}
		var version int
		if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 3 {
			t.Fatal(version)
		}
		if countRows(t, s, "observers") != 0 {
			t.Fatal("migration created observation")
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestObserverV1UpgradeFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.sqlite")
	snap, _, receipts := populatedV1(t, path)
	original := observerSchema
	observerSchema = original + "\nTHIS IS AN INJECTED UPGRADE ERROR;"
	s, err := Open(t.Context(), path)
	observerSchema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("bad upgrade succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	var version, tables int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name LIKE 'observer%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	after, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 || tables != 0 || !reflect.DeepEqual(snap, after) || string(receipts) != string(receiptDump(t, old)) {
		t.Fatal("partial upgrade committed")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
func TestObserverMigrationRejectsClaimedForeignAndFutureSchemas(t *testing.T) {
	for _, alter := range []string{"PRAGMA user_version=3", "PRAGMA application_id=7", "DROP TABLE events", "UPDATE metadata SET instance_id='invalid'", "PRAGMA user_version=2"} {
		t.Run(alter, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.sqlite")
			populatedV1(t, path)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), alter); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(t.Context(), path)
			if err == nil {
				_ = s.Close()
				t.Fatal("invalid schema accepted")
			}
		})
	}
}
