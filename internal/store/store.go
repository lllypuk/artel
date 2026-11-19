// Package store owns SQLite. Every mutation, audit event and receipt shares one
// immediate transaction; no optimistic check is performed outside that transaction.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	_ "modernc.org/sqlite" // SQLite driver.
)

//go:embed schema.sql
var schema string

//go:embed observer_schema.sql
var observerSchema string

//go:embed delivery_schema.sql
var deliverySchema string

const schemaVersion = 3

type Store struct {
	db              *sql.DB
	observerMu      sync.Mutex
	observerClock   func() time.Time
	observerSeen    map[string]time.Time
	deliveryMu      sync.Mutex
	deliveryReadyMu sync.Mutex
	deliveryReady   map[string]deliveryReadyState
}

type Snapshot struct {
	InstanceID string              `json:"instance_id"`
	EventSeq   int64               `json:"event_seq"`
	Projects   []domain.Project    `json:"projects"`
	Tasks      []domain.Task       `json:"tasks"`
	Queues     map[string][]string `json:"queues"`
}

type Change struct {
	Object string         `json:"object"`
	ID     string         `json:"id"`
	Before map[string]any `json:"before"`
	After  map[string]any `json:"after"`
}

type Event struct {
	Seq       int64    `json:"seq"`
	Action    string   `json:"action"`
	ProjectID string   `json:"project_id"`
	TaskID    string   `json:"task_id,omitempty"`
	Source    string   `json:"source"`
	Timestamp string   `json:"timestamp"`
	Changes   []Change `json:"changes"`
}

type Result struct {
	Status int
	Body   []byte
}
type Mutation struct{ Key, Method, Path, Hash string }

func Open(ctx context.Context, path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("database path: %w", err)
	}
	if err := prepareFiles(absolute); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: absolute}
	query := uri.Query()
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"} {
		query.Add("_pragma", pragma)
	}
	query.Set("_txlock", "immediate")
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, observerClock: time.Now, observerSeen: make(map[string]time.Time), deliveryReady: make(map[string]deliveryReadyState)}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Do not change a foreign database's journal format before schema validation.
	var journal string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil || journal != "wal" {
		_ = db.Close()
		return nil, errors.New("cannot enable database WAL")
	}
	return s, nil
}

// prepareFiles refuses symlinks/special files, including SQLite sidecars. The
// daemon's private, locked data directory protects these names during its life.
func prepareFiles(path string) error {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		info, err := os.Lstat(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("unsafe database file")
		}
	}
	// #nosec G304 -- Explicit daemon-owned database path; exclusive creation cannot follow a symlink.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot create database file")
	}
	return f.Close()
}

func (s *Store) Close() error { return s.db.Close() }

func requireSchemaQueries(ctx context.Context, tx *sql.Tx, queries []string, message string) error {
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return errors.New(message)
		}
		_ = rows.Close()
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unrecognized database schema")
		}
		if _, err := tx.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("create schema: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(id,instance_id) VALUES(1,?)", uuid.NewString()); err != nil {
			return err
		}
	} else if version < 1 || version > schemaVersion {
		return errors.New("unsupported database schema version")
	}
	var applicationID int
	if err := tx.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return err
	}
	if applicationID != 1095914546 {
		return errors.New("unrecognized database application")
	}
	var instance string
	if err := tx.QueryRowContext(ctx, "SELECT instance_id FROM metadata WHERE id=1").Scan(&instance); err != nil {
		return errors.New("invalid database metadata")
	}
	if _, err := uuid.Parse(instance); err != nil {
		return errors.New("invalid database identity")
	}
	// Verify required tables/columns even when a foreign database claims our version.
	if err := requireSchemaQueries(ctx, tx, []string{
		"SELECT id,root_path,archived,record FROM projects LIMIT 0",
		"SELECT id,project_id,state,record FROM tasks LIMIT 0",
		"SELECT project_id,position,task_id FROM queue_entries LIMIT 0",
		"SELECT seq,action,project_id,task_id,source,timestamp,changes FROM events LIMIT 0",
		"SELECT key,method,path,payload_hash,status,body FROM mutation_receipts LIMIT 0",
	}, "invalid database schema"); err != nil {
		return err
	}
	if version < 2 {
		if _, err := tx.ExecContext(ctx, observerSchema); err != nil {
			return fmt.Errorf("upgrade observer schema: %w", err)
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, deliverySchema); err != nil {
			return fmt.Errorf("upgrade delivery schema: %w", err)
		}
	}
	if err := requireSchemaQueries(ctx, tx, []string{
		"SELECT id,verifier,record FROM observers LIMIT 0",
		"SELECT observer_id,runtime_id,retired FROM observer_identities LIMIT 0",
		"SELECT observer_id,runtime_id,record FROM observer_runtimes LIMIT 0",
		"SELECT principal,key,request_hash,result FROM observer_receipts LIMIT 0",
		"SELECT seq,observer_id,action,timestamp FROM observer_audit LIMIT 0",
	}, "invalid observer schema"); err != nil {
		return err
	}
	return commitAfterSchemaCheck(ctx, tx)
}

func commitAfterSchemaCheck(ctx context.Context, tx *sql.Tx) error {
	if err := requireSchemaQueries(ctx, tx, []string{
		"SELECT id,verifier,record FROM delivery_bindings LIMIT 0",
		"SELECT seq,id,task_id,binding_id,receipt_key,blocked,record FROM delivery_messages LIMIT 0",
		"SELECT attempt_id,message_seq,binding_id,writer_id,state,record FROM delivery_attempts LIMIT 0",
		"SELECT principal,key,request_hash,result FROM delivery_receipts LIMIT 0",
	}, "invalid delivery schema"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	snap, err := load(ctx, tx)
	if err != nil {
		return Snapshot{}, err
	}
	return snap, tx.Commit()
}

func load(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	snap := Snapshot{Projects: []domain.Project{}, Tasks: []domain.Task{}, Queues: map[string][]string{}}
	if err := tx.QueryRowContext(ctx, "SELECT instance_id FROM metadata WHERE id=1").Scan(&snap.InstanceID); err != nil {
		return snap, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(seq),0) FROM events").Scan(&snap.EventSeq); err != nil {
		return snap, err
	}
	if err := readRecords(ctx, tx, "SELECT record FROM projects ORDER BY id", &snap.Projects); err != nil {
		return snap, err
	}
	if err := readRecords(ctx, tx, "SELECT record FROM tasks ORDER BY id", &snap.Tasks); err != nil {
		return snap, err
	}
	for _, p := range snap.Projects {
		snap.Queues[p.ID] = []string{}
	}
	rows, err := tx.QueryContext(ctx, "SELECT project_id,task_id FROM queue_entries ORDER BY project_id,position")
	if err != nil {
		return snap, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var p, t string
		if err := rows.Scan(&p, &t); err != nil {
			return snap, err
		}
		snap.Queues[p] = append(snap.Queues[p], t)
	}
	return snap, rows.Err()
}

func readRecords[T any](ctx context.Context, tx *sql.Tx, query string, out *[]T) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return err
		}
		var record T
		if err := json.Unmarshal([]byte(data), &record); err != nil {
			return err
		}
		*out = append(*out, record)
	}
	return rows.Err()
}

// Apply replays a receipt before invoking fn (including its version checks).
// fn must not do network I/O and may mutate only this transaction's snapshot.
func (s *Store) Apply(ctx context.Context, m Mutation, fn func(*Snapshot) (Result, *Event, error)) (Result, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var method, path, hash string
	var result Result
	err = tx.QueryRowContext(ctx, "SELECT method,path,payload_hash,status,body FROM mutation_receipts WHERE key=?", m.Key).Scan(&method, &path, &hash, &result.Status, &result.Body)
	if err == nil {
		if method != m.Method || path != m.Path || hash != m.Hash {
			return Result{}, domain.Conflict("idempotency key reused for a different request")
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	snap, err := load(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	result, event, err := fn(&snap)
	if err != nil {
		return Result{}, err
	}
	if event != nil {
		if err := persist(ctx, tx, snap); err != nil {
			return Result{}, err
		}
		changes, err := json.Marshal(event.Changes)
		if err != nil {
			return Result{}, err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO events(action,project_id,task_id,source,timestamp,changes) VALUES(?,?,?,'owner_manual',?,?)", event.Action, event.ProjectID, nullable(event.TaskID), event.Timestamp, string(changes))
		if err != nil {
			return Result{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO mutation_receipts(key,method,path,payload_hash,status,body) VALUES(?,?,?,?,?,?)", m.Key, m.Method, m.Path, m.Hash, result.Status, result.Body); err != nil {
		return Result{}, err
	}
	return result, tx.Commit()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func persist(ctx context.Context, tx *sql.Tx, snap Snapshot) error {
	for _, p := range snap.Projects {
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO projects(id,root_path,archived,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET root_path=excluded.root_path,archived=excluded.archived,record=excluded.record", p.ID, p.RootPath, p.Archived, string(data)); err != nil {
			return err
		}
	}
	// Release the unique in_progress index before applying a possible state swap.
	// Other connections cannot observe this temporary state inside the transaction.
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET state='backlog' WHERE state='in_progress'"); err != nil {
		return err
	}
	for _, t := range snap.Tasks {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO tasks(id,project_id,state,record) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,record=excluded.record", t.ID, t.ProjectID, t.State, string(data)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM queue_entries"); err != nil {
		return err
	}
	// Delivery commissions exist only in schema v3+; raw legacy fixtures may not.
	var deliveryTables int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='delivery_messages'").Scan(&deliveryTables); err != nil {
		return err
	}
	if deliveryTables != 0 {
		// Closing a task or archiving a project permanently blocks undelivered
		// commissions in the same transaction as the domain change.
		if _, err := tx.ExecContext(ctx, "UPDATE delivery_messages SET blocked=1,block_reason='task_closed' WHERE blocked=0 AND task_id IN (SELECT id FROM tasks WHERE state IN ('done','cancelled'))"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE delivery_messages SET blocked=1,block_reason='project_archived' WHERE blocked=0 AND task_id IN (SELECT id FROM tasks WHERE project_id IN (SELECT id FROM projects WHERE archived=1))"); err != nil {
			return err
		}
	}
	for project, queue := range snap.Queues {
		for pos, task := range queue {
			if _, err := tx.ExecContext(ctx, "INSERT INTO queue_entries(project_id,position,task_id) VALUES(?,?,?)", project, pos, task); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Events(ctx context.Context) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT seq,action,project_id,coalesce(task_id,''),source,timestamp,changes FROM events ORDER BY seq")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := []Event{}
	for rows.Next() {
		var e Event
		var changes string
		if err := rows.Scan(&e.Seq, &e.Action, &e.ProjectID, &e.TaskID, &e.Source, &e.Timestamp, &changes); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(changes), &e.Changes); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
