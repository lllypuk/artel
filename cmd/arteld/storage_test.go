package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/artel/internal/local"
)

func TestDatabaseFailureIsFatalAndRedacted(t *testing.T) {
	base := t.TempDir()
	data := filepath.Join(base, "data")
	lock, err := local.Acquire(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(data, "artel.sqlite")
	original := []byte("not a database; private diagnostic sentinel")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	code := serve(t.Context(), []string{"--listen", "127.0.0.1:0", "--data-dir", data, "--token-file", filepath.Join(base, "private", "token")}, &out, &diagnostic)
	if code != 1 || out.Len() != 0 || diagnostic.String() != "arteld: cannot initialize database\n" {
		t.Fatal("unsafe database failure handling")
	}
	// #nosec G304 -- Test-owned database fixture under t.TempDir only.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("failed startup replaced database")
	}
	assertUnlocked(t, data)
}
