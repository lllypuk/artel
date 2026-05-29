package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// newInMemoryDB creates a test SQLite DB in memory.
func newInMemoryDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

// mustMarshal marshals v to json.RawMessage, failing the test on error.
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic("marshal: " + err.Error())
	}
	return data
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// mockEmbedder implements Embedder for tests.
type mockEmbedder struct {
	vec []float32
	err error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, m.err
}

func TestOllamaEmbedderConfigDefaults(t *testing.T) {
	e := NewOllamaEmbedder(OllamaEmbedderConfig{
		BaseURL: "http://localhost:11434",
		Model:   "nomic-embed-text",
	})
	if e.baseURL != "http://localhost:11434" {
		t.Errorf("expected baseURL http://localhost:11434, got %s", e.baseURL)
	}
	if e.model != "nomic-embed-text" {
		t.Errorf("expected model nomic-embed-text, got %s", e.model)
	}
	if e.client == nil {
		t.Fatal("expected non-nil http client")
	}
	if e.client.Timeout == 0 {
		t.Error("expected non-zero timeout")
	}
}

func TestNotesToolWithEmbedder(t *testing.T) {
	db := newInMemoryDB(t)
	notes := NewNotesToolWithEmbedder(db, &mockEmbedder{
		vec: []float32{0.1, 0.2, 0.3},
	})
	if notes.embedder == nil {
		t.Fatal("expected non-nil embedder")
	}
	if notes.db == nil {
		t.Fatal("expected non-nil db")
	}
}

func TestExecuteCreateWithEmbedder(t *testing.T) {
	db := newInMemoryDB(t)
	notes := NewNotesToolWithEmbedder(db, &mockEmbedder{
		vec: []float32{0.1, 0.2, 0.3},
	})

	ctx := context.WithValue(context.Background(), ChatIDKey, int64(1))
	args := mustMarshal(notesArgs{Action: "create", Title: "Test", Content: "Hello", Tags: "dev"})
	result, err := notes.Execute(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Note created (id: 1)" {
		t.Errorf("unexpected result: %s", result)
	}

	// Verify embedding was saved.
	ids, err := db.SearchSimilar(ctx, []float32{0.1, 0.2, 0.3}, 10)
	if err != nil {
		t.Fatalf("search similar: %v", err)
	}
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("expected embedding for note 1, got %v", ids)
	}
}

func TestMergeNotesByID(t *testing.T) {
	a := []storage.Note{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}}
	b := []storage.Note{{ID: 2, Title: "B2"}, {ID: 3, Title: "C"}}
	merged := mergeNotesByID(a, b)

	if len(merged) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(merged))
	}
	if merged[0].ID != 1 || merged[0].Title != "A" {
		t.Errorf("first: expected [1, A], got [%d, %s]", merged[0].ID, merged[0].Title)
	}
	if merged[1].ID != 2 || merged[1].Title != "B" {
		t.Errorf("second: expected [2, B], got [%d, %s]", merged[1].ID, merged[1].Title)
	}
	if merged[2].ID != 3 || merged[2].Title != "C" {
		t.Errorf("third: expected [3, C], got [%d, %s]", merged[2].ID, merged[2].Title)
	}
}

func TestExecuteSearchWithEmbedder(t *testing.T) {
	db := newInMemoryDB(t)
	notes := NewNotesToolWithEmbedder(db, &mockEmbedder{
		vec: []float32{0.5, 0.5},
	})

	ctx := context.WithValue(context.Background(), ChatIDKey, int64(1))

	// Create a few notes with embeddings.
	note1ID, _ := db.CreateNote(ctx, 1, "Go", "golang development", "dev")
	note2ID, _ := db.CreateNote(ctx, 1, "Recipes", "cooking tips", "food")
	db.SaveEmbedding(ctx, note1ID, []float32{1.0, 0.0}) //nolint:errcheck
	db.SaveEmbedding(ctx, note2ID, []float32{0.0, 1.0}) //nolint:errcheck

	// Search: query embedding [0.5, 0.5] is equidistant from both.
	// FTS5 should find them by keywords; semantic adds both.
	args := mustMarshal(notesArgs{Action: "search", Query: "development"})
	result, err := notes.Execute(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(result, "Go") {
		t.Errorf("expected 'Go' in results, got: %s", result)
	}
}

func TestNotesToolWithoutEmbedder(t *testing.T) {
	db := newInMemoryDB(t)
	notes := NewNotesTool(db)
	if notes.embedder != nil {
		t.Error("expected nil embedder")
	}
}
