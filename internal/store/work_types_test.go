package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

type legacyTaskRow struct {
	ID        string
	ProjectID string
	State     domain.State
	Record    string
}

type populatedV6State struct {
	snapshot         Snapshot
	events           []Event
	domainReceipts   []byte
	observerReceipts []byte
	tasks            []legacyTaskRow
	otherTables      string
	schema           string
	resultTaskID     string
	resultID         string
	reviewID         string
	questionID       string
	messageID        string
}

func workTypeMutation(t *testing.T, s *Store, key string, mutate func(*Snapshot)) {
	t.Helper()
	_, err := s.Apply(t.Context(), Mutation{Key: key}, func(snap *Snapshot) (Result, *Event, error) {
		mutate(snap)
		return Result{Status: 200, Body: []byte(`{}`)}, &Event{
			Action: "work_types.fixture", ProjectID: snap.Projects[0].ID,
			Timestamp: "2026-11-22T00:00:00Z", Changes: []Change{},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func legacyTaskRows(t *testing.T, db *sql.DB) []legacyTaskRow {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT id,project_id,state,record FROM tasks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var result []legacyTaskRow
	for rows.Next() {
		var row legacyTaskRow
		if err := rows.Scan(&row.ID, &row.ProjectID, &row.State, &row.Record); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func databaseTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_master
		WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

var tableDumpQueries = map[string]string{
	"current_task_results": "SELECT * FROM current_task_results ORDER BY rowid",
	"delivery_attempts":    "SELECT * FROM delivery_attempts ORDER BY rowid",
	"delivery_bindings":    "SELECT * FROM delivery_bindings ORDER BY rowid",
	"delivery_messages":    "SELECT * FROM delivery_messages ORDER BY rowid",
	"delivery_receipts":    "SELECT * FROM delivery_receipts ORDER BY rowid",
	"events":               "SELECT * FROM events ORDER BY rowid",
	"metadata":             "SELECT * FROM metadata ORDER BY rowid",
	"mutation_receipts":    "SELECT * FROM mutation_receipts ORDER BY rowid",
	"observer_audit":       "SELECT * FROM observer_audit ORDER BY rowid",
	"observer_identities":  "SELECT * FROM observer_identities ORDER BY rowid",
	"observer_receipts":    "SELECT * FROM observer_receipts ORDER BY rowid",
	"observer_runtimes":    "SELECT * FROM observer_runtimes ORDER BY rowid",
	"observers":            "SELECT * FROM observers ORDER BY rowid",
	"owner_questions":      "SELECT * FROM owner_questions ORDER BY rowid",
	"projects":             "SELECT * FROM projects ORDER BY rowid",
	"queue_entries":        "SELECT * FROM queue_entries ORDER BY rowid",
	"sqlite_sequence":      "SELECT * FROM sqlite_sequence ORDER BY rowid",
	"task_results":         "SELECT * FROM task_results ORDER BY rowid",
	"task_reviews":         "SELECT * FROM task_reviews ORDER BY rowid",
}

func tableDump(t *testing.T, db *sql.DB, table string) []any {
	t.Helper()
	query, ok := tableDumpQueries[table]
	if !ok {
		t.Fatalf("no dump query for table %q", table)
	}
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []any
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func databaseDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	tables := databaseTables(t, db)
	tables = append(tables, "sqlite_sequence")
	sort.Strings(tables)
	dump := make(map[string][]any)
	for _, table := range tables {
		if table == "tasks" {
			continue
		}
		dump[table] = tableDump(t, db, table)
	}
	encoded, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func schemaDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT type,name,tbl_name,sql FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var result []any
	for rows.Next() {
		var objectType, name, table string
		var definition sql.NullString
		if err := rows.Scan(&objectType, &name, &table, &definition); err != nil {
			t.Fatal(err)
		}
		result = append(result, []any{objectType, name, table, definition})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func populatedV6(t *testing.T, path string) populatedV6State {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{v1Schema, observerSchema, deliverySchema, questionsSchema, resultsSchema, currentReviewsSchema} {
		if _, err := db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata(id,instance_id) VALUES(1,?)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db, observerClock: time.Now, observerSeen: make(map[string]time.Time), deliveryReady: make(map[string]time.Time)}
	project, queued := seed(t, old)
	running := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "running v6", State: domain.InProgress, Version: 7}
	workTypeMutation(t, old, uuid.NewString(), func(snap *Snapshot) {
		for i := range snap.Tasks {
			if snap.Tasks[i].ID == queued.ID {
				snap.Tasks[i].State = domain.Queued
				snap.Tasks[i].Version = 3
			}
		}
		snap.Tasks = append(snap.Tasks, running)
		snap.Queues[project.ID] = []string{queued.ID}
	})
	result := mustSubmitResult(t, old, running.ID, resultMutation("/v6-submit"), submitInput(resultRepoA, resultHeadA, ""))
	review := mustAttachReview(t, old, running.ID, result.ID, resultMutation("/v6-review"), reviewInput(result, "v6"))
	questions := mustQuestionEnv(t, old)
	question := questions.mustCreate(t, old, uuid.NewString(), "v6 question")
	questions.mustAnswer(t, old, question.ID, uuid.NewString(), "v6 answer")
	if err := old.DeliveryReady(t.Context(), questions.binding.ID, questions.instance, questions.writer); err != nil {
		t.Fatal(err)
	}
	message := questions.mustAppend(t, old, "v6 commission")
	attempt := questions.mustSpend(t, old, questions.writer, message)
	questions.mustVerify(t, old, questions.writer, attempt)
	questions.mustReceipt(t, old, questions.receiptKey(t, old, message.ID), questions.receiptFor(attempt))
	assertNoForeignKeyViolations(t, db)
	snapshot, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := old.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("fixture version=%d err=%v", version, err)
	}
	return populatedV6State{
		snapshot: snapshot, events: events, domainReceipts: receiptDump(t, old), observerReceipts: observerReceiptDump(t, old),
		tasks: legacyTaskRows(t, db), otherTables: databaseDump(t, db), schema: schemaDump(t, db),
		resultTaskID: running.ID, resultID: result.ID, reviewID: review.ID, questionID: question.ID, messageID: message.ID,
	}
}

func expectedMigratedSnapshot(before Snapshot) Snapshot {
	before.Tasks = append([]domain.Task(nil), before.Tasks...)
	for i := range before.Tasks {
		before.Tasks[i].Kind = domain.KindTask
		before.Tasks[i].ParentID = ""
	}
	return before
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var violations []string
	for rows.Next() {
		var table string
		var rowid any
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			t.Fatal(err)
		}
		violations = append(violations, table)
	}
	if err := rows.Err(); err != nil || len(violations) != 0 {
		t.Fatalf("foreign key violations=%v err=%v", violations, err)
	}
}

func assertMigratedTaskRows(t *testing.T, s *Store, before populatedV6State) {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "SELECT id,project_id,kind,parent_id,state,record FROM tasks ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var migrated []legacyTaskRow
	for rows.Next() {
		var row legacyTaskRow
		var kind domain.Kind
		var parent sql.NullString
		if err := rows.Scan(&row.ID, &row.ProjectID, &kind, &parent, &row.State, &row.Record); err != nil {
			t.Fatal(err)
		}
		if kind != domain.KindTask || parent.Valid {
			t.Fatalf("legacy task kind=%q parent=%+v", kind, parent)
		}
		record, err := unmarshalTaskRecord([]byte(row.Record))
		if err != nil {
			t.Fatal(err)
		}
		if record.Kind != domain.KindTask || record.ParentID != "" || record.State != row.State {
			t.Fatalf("SQL/JSON mismatch: SQL=%+v JSON=%+v", row, record)
		}
		migrated = append(migrated, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(migrated) != len(before.tasks) {
		t.Fatalf("tasks=%d want=%d", len(migrated), len(before.tasks))
	}
	for i := range migrated {
		oldRecord, err := unmarshalTaskRecord([]byte(before.tasks[i].Record))
		if err != nil {
			t.Fatal(err)
		}
		newRecord, err := unmarshalTaskRecord([]byte(migrated[i].Record))
		if err != nil {
			t.Fatal(err)
		}
		if migrated[i].ID != before.tasks[i].ID || migrated[i].ProjectID != before.tasks[i].ProjectID ||
			migrated[i].State != before.tasks[i].State || newRecord.Version != oldRecord.Version {
			t.Fatalf("identity/state/version changed: before=%+v after=%+v", before.tasks[i], migrated[i])
		}
	}
}

func assertMigratedDependentReads(t *testing.T, s *Store, before populatedV6State) {
	t.Helper()
	history, err := s.TaskResultHistory(t.Context(), before.resultTaskID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	foundResult, foundReview := false, false
	for _, entry := range history.Entries {
		foundResult = foundResult || (entry.Result != nil && entry.Result.ID == before.resultID)
		foundReview = foundReview || (entry.Review != nil && entry.Review.ID == before.reviewID)
	}
	if !foundResult || !foundReview {
		t.Fatalf("result/review missing after migration: %+v", history)
	}
	if got := questionByID(t, s, questionsTaskID(t, s, before.questionID), before.snapshot.InstanceID, before.questionID); got.ID != before.questionID {
		t.Fatalf("question missing after migration: %+v", got)
	}
	messages, _, err := s.ListDeliveryMessages(t.Context(), deliveryMessageTaskID(t, s, before.messageID), 0, 100, []string{before.messageID})
	if err != nil || len(messages) != 1 || messages[0].Status != "observed" {
		t.Fatalf("delivery missing after migration: %+v %v", messages, err)
	}
}

func assertV6Migration(t *testing.T, s *Store, before populatedV6State) {
	t.Helper()
	after, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := expectedMigratedSnapshot(before.snapshot); !reflect.DeepEqual(after, want) {
		gotJSON, _ := json.Marshal(after)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("migrated snapshot mismatch\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
	events, err := s.Events(t.Context())
	if err != nil || !reflect.DeepEqual(events, before.events) {
		t.Fatalf("migration changed events: %v", err)
	}
	if string(receiptDump(t, s)) != string(before.domainReceipts) || string(observerReceiptDump(t, s)) != string(before.observerReceipts) {
		t.Fatal("migration changed receipts")
	}
	if got := databaseDump(t, s.db); got != before.otherTables {
		t.Fatal("migration changed queue/results/reviews/delivery/questions/metadata")
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	assertNoForeignKeyViolations(t, s.db)
	assertMigratedTaskRows(t, s, before)
	assertMigratedDependentReads(t, s, before)
}

func questionsTaskID(t *testing.T, s *Store, questionID string) string {
	t.Helper()
	var taskID string
	if err := s.db.QueryRowContext(t.Context(), "SELECT task_id FROM owner_questions WHERE id=?", questionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func deliveryMessageTaskID(t *testing.T, s *Store, messageID string) string {
	t.Helper()
	var taskID string
	if err := s.db.QueryRowContext(t.Context(), "SELECT task_id FROM delivery_messages WHERE id=?", messageID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func TestWorkTypesCleanAndPopulatedV6Migration(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		s := openTest(t)
		var version int
		if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 7 {
			t.Fatalf("version=%d err=%v", version, err)
		}
		var columns int
		if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info('tasks')
			WHERE name IN ('kind','parent_id')`).Scan(&columns); err != nil || columns != 2 {
			t.Fatalf("work type columns=%d err=%v", columns, err)
		}
		assertNoForeignKeyViolations(t, s.db)
		if countRows(t, s, "events") != 0 {
			t.Fatal("clean migration emitted events")
		}
	})
	t.Run("populated v6", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "v6.sqlite")
		before := populatedV6(t, path)
		for range 2 {
			s, err := Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			assertV6Migration(t, s, before)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestWorkTypesV6MigrationFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6-rollback.sqlite")
	before := populatedV6(t, path)
	original := workTypesSchema
	workTypesSchema = original + "\nTHIS IS AN INJECTED WORK TYPES UPGRADE ERROR;"
	s, err := Open(t.Context(), path)
	workTypesSchema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("bad work types migration succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if got := schemaDump(t, db); got != before.schema {
		t.Fatal("failed migration changed schema")
	}
	if got := legacyTaskRows(t, db); !reflect.DeepEqual(got, before.tasks) {
		t.Fatal("failed migration changed tasks")
	}
	if got := databaseDump(t, db); got != before.otherTables {
		t.Fatal("failed migration changed dependent tables")
	}
	assertNoForeignKeyViolations(t, db)
}

func TestWorkTypesV6ReaderRejectsV7WithoutChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- Test-owned private database fixture.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldVersion := schemaVersion
	schemaVersion = 6
	old, openErr := Open(t.Context(), path)
	schemaVersion = oldVersion
	if openErr == nil {
		_ = old.Close()
		t.Fatal("v6 reader accepted v7 database")
	}
	// #nosec G304 -- Test-owned private database fixture.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("v6 reader modified v7 database")
	}
}

func TestWorkTypesDTOAndSQLJSONSynchronization(t *testing.T) {
	s := openTest(t)
	project, task := seed(t, s)
	epic := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "epic", Kind: domain.KindEpic, State: domain.Open, Version: 1}
	bug := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "bug", Kind: domain.KindBug, ParentID: epic.ID,
		State: domain.Backlog, Version: 1, ExpectedBehavior: "expected", ActualBehavior: "actual", FixEvidence: "owner evidence"}
	workTypeMutation(t, s, uuid.NewString(), func(snap *Snapshot) {
		snap.Tasks = append(snap.Tasks, epic, bug)
	})
	encoded, err := marshalTaskRecord(bug)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"kind":"bug"`, `"parent_id":"` + epic.ID + `"`, `"expected_behavior":"expected"`, `"actual_behavior":"actual"`, `"fix_evidence":"owner evidence"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("DTO %s missing %s", encoded, field)
		}
	}
	if strings.Contains(string(encoded), "summary") {
		t.Fatal("Epic summary leaked into persisted DTO")
	}
	legacy, err := json.Marshal(bug)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "kind") || strings.Contains(string(legacy), "parent_id") || strings.Contains(string(legacy), "fix_evidence") {
		t.Fatal("additive storage fields changed legacy transport JSON")
	}
	// Structural SQL values are authoritative if an old/manual record is stale.
	if _, err := s.db.ExecContext(t.Context(), "UPDATE tasks SET kind='bug',state='queued',parent_id=?,record=? WHERE id=?", epic.ID, `{"id":"`+task.ID+`","state":"done","kind":"epic"}`, task.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range snapshot.Tasks {
		if got.ID == task.ID && (got.Kind != domain.KindBug || got.State != domain.Queued || got.ParentID != epic.ID) {
			t.Fatalf("SQL did not synchronize JSON record: %+v", got)
		}
	}
}

func TestWorkTypesSQLConstraintsAndExecutableIndex(t *testing.T) {
	s := openTest(t)
	project, task := seed(t, s)
	otherID := uuid.NewString()
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO projects(id,root_path,archived,record) VALUES(?, '/other',0,'{}')", otherID); err != nil {
		t.Fatal(err)
	}
	epicID := uuid.NewString()
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO tasks(id,project_id,kind,parent_id,state,record)
		VALUES(?,?,'epic',NULL,'open','{}')`, epicID, project.ID); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('bad-kind',?,'story',NULL,'backlog','{}')`, []any{project.ID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('bad-epic-state',?,'epic',NULL,'backlog','{}')`, []any{project.ID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('bad-task-state',?,'task',NULL,'open','{}')`, []any{project.ID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('epic-parent',?,'epic',?,'open','{}')`, []any{project.ID, epicID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('self-parent',?,'task','self-parent','backlog','{}')`, []any{project.ID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('cross-project',?,'bug',?,'backlog','{}')`, []any{otherID, epicID}},
		{`INSERT INTO tasks(id,project_id,kind,parent_id,state,record) VALUES('missing-parent',?,'task','missing','backlog','{}')`, []any{project.ID}},
	}
	for _, test := range invalid {
		if _, err := s.db.ExecContext(t.Context(), test.query, test.args...); err == nil {
			t.Fatalf("constraint missing: %s", test.query)
		}
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO tasks(id,project_id,kind,parent_id,state,record)
		VALUES('child',?,'bug',?,'backlog','{}')`, project.ID, epicID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE tasks SET kind='epic',state='in_progress' WHERE id=?", epicID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE tasks SET state='in_progress' WHERE id='child'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), "UPDATE tasks SET state='in_progress' WHERE id=?", task.ID); err == nil {
		t.Fatal("second executable in_progress row accepted")
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO tasks(id,project_id,kind,parent_id,state,record)
		VALUES('second-epic',?,'epic',NULL,'in_progress','{}')`, project.ID); err != nil {
		t.Fatal("partial unique index blocked a second Epic")
	}
}

func TestPersistLeavesEpicInProgressAcrossUnrelatedMutation(t *testing.T) {
	s := openTest(t)
	project, _ := seed(t, s)
	epic := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "epic", Kind: domain.KindEpic, State: domain.InProgress, Version: 1}
	bug := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "bug", Kind: domain.KindBug, State: domain.InProgress, Version: 1}
	workTypeMutation(t, s, uuid.NewString(), func(snap *Snapshot) {
		snap.Tasks = append(snap.Tasks, epic, bug)
	})
	workTypeMutation(t, s, uuid.NewString(), func(snap *Snapshot) {
		snap.Projects[0].Description = "unrelated edit"
		for i := range snap.Tasks {
			if snap.Tasks[i].ID == bug.ID {
				snap.Tasks[i].FixEvidence = "edited"
			}
		}
	})
	snapshot, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range snapshot.Tasks {
		if got.ID == epic.ID && (got.Kind != domain.KindEpic || got.State != domain.InProgress) {
			t.Fatalf("Epic rewritten by persist: %+v", got)
		}
		if got.ID == bug.ID && (got.Kind != domain.KindBug || got.State != domain.InProgress || got.FixEvidence != "edited") {
			t.Fatalf("Bug lost during persist: %+v", got)
		}
	}
	var epicState, bugState domain.State
	if err := s.db.QueryRowContext(t.Context(), "SELECT state FROM tasks WHERE id=?", epic.ID).Scan(&epicState); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT state FROM tasks WHERE id=?", bug.ID).Scan(&bugState); err != nil {
		t.Fatal(err)
	}
	if epicState != domain.InProgress || bugState != domain.InProgress {
		t.Fatalf("SQL states epic=%q bug=%q", epicState, bugState)
	}
}
