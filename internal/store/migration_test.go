package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

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
