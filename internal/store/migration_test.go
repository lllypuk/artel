package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResultsV4UpgradePreservesDomainAndRollsBackFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v4.sqlite")
	before, events, receipts := populatedV1(t, path)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{observerSchema, deliverySchema, questionsSchema} {
		if _, err := db.ExecContext(t.Context(), migration); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original := resultsSchema
	resultsSchema = original + "\nTHIS IS AN INJECTED RESULTS UPGRADE ERROR;"
	s, err := Open(t.Context(), path)
	resultsSchema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("bad results migration succeeded")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	after, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	afterEvents, err := old.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var version, tables int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name IN ('task_results','current_task_results','task_reviews')").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if version != 4 || tables != 0 || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(events, afterEvents) || string(receipts) != string(receiptDump(t, old)) {
		t.Fatal("partial results migration committed")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	assertResultsV4Recovery(t, path, before)
}

func assertResultsV4Recovery(t *testing.T, path string, before Snapshot) {
	t.Helper()
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	migrated, err := s.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(expectedMigratedSnapshot(before), migrated) {
		t.Fatalf("v4 domain changed: %v", err)
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	var redundant int
	if err := s.db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM pragma_table_info('task_results') WHERE name='seq') +
		(SELECT count(*) FROM pragma_table_info('task_reviews') WHERE name='seq') +
		(SELECT count(*) FROM sqlite_master WHERE type='index' AND name IN ('task_results_task','task_reviews_result'))`).Scan(&redundant); err != nil || redundant != 0 {
		t.Fatalf("redundant result ordering state=%d err=%v", redundant, err)
	}
}

func TestCurrentReviewIndexUpgradesV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/v5-submit"), submitInput(resultRepoA, resultHeadA, ""))
	review := mustAttachReview(t, s, task.ID, result.ID, resultMutation("/v5-review"), reviewInput(result, "v5"))
	if _, err := s.db.ExecContext(t.Context(), "DROP INDEX task_reviews_current_candidate; PRAGMA user_version=5;"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	var version int
	if err := reopened.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	rows, err := reopened.db.QueryContext(t.Context(), `EXPLAIN QUERY PLAN SELECT id FROM task_reviews
		WHERE task_id=? AND result_id=? AND rowid>? ORDER BY rowid LIMIT ?`, task.ID, result.ID, 0, 21)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err := rows.Err(); err != nil || len(plans) != 1 || !strings.Contains(plans[0], "USING INDEX task_reviews_current_candidate") {
		t.Fatalf("expected ordered candidate index search, got %v: %v", plans, err)
	}
	page, err := reopened.CurrentTaskReviews(t.Context(), task.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	requireCurrentReviewPage(t, page, result.ID, review.ID, false)
}

func TestFailedMigrationRollsBackDDL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.sqlite")
	original := schema
	schema = original + "\nTHIS IS AN INJECTED MIGRATION ERROR;"
	// No parallel tests: schema is embedded immutable production input. Restore it
	// immediately after Open, including on any unexpected result.
	s, err := Open(t.Context(), path)
	schema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("invalid migration succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var count, version, application int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "PRAGMA application_id").Scan(&application); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if count != 0 || version != 0 || application != 0 {
		t.Fatalf("partial migration: tables=%d version=%d app=%d", count, version, application)
	}
	recovered, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}
