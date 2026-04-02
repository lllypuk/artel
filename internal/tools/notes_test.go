package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func openNotesDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func notesExec(t *testing.T, nt *tools.NotesTool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return nt.Execute(context.Background(), raw)
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestNotesToolName(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	if nt.Name() != "notes" {
		t.Errorf("expected 'notes', got %q", nt.Name())
	}
}

func TestNotesToolDescription(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	if nt.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestNotesToolParameters(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	params := nt.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	if _, ok := props["action"]; !ok {
		t.Error("expected 'action' in properties")
	}
}

// ── create ────────────────────────────────────────────────────────────────────

func TestNotesCreate(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, err := notesExec(t, nt, map[string]any{
		"action":  "create",
		"title":   "Test Note",
		"content": "Hello content",
		"tags":    "test,go",
		"chat_id": 100,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "created") {
		t.Errorf("expected 'created' in result, got %q", result)
	}
}

// ── list ──────────────────────────────────────────────────────────────────────

func TestNotesList(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))

	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Note 1", "content": "Content 1", "chat_id": 200,
	})

	result, err := notesExec(t, nt, map[string]any{"action": "list", "chat_id": 200})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Note 1") {
		t.Errorf("expected 'Note 1' in result, got %q", result)
	}
}

func TestNotesListEmpty(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, err := notesExec(t, nt, map[string]any{"action": "list", "chat_id": 300})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No notes") {
		t.Errorf("expected 'No notes' in result, got %q", result)
	}
}

func TestNotesListMultiple(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	for i := range 3 {
		_, _ = notesExec(t, nt, map[string]any{
			"action": "create", "title": "Note", "content": "Content", "chat_id": i + 400,
		})
	}
	// Verify each user only sees their own note.
	for i := range 3 {
		result, _ := notesExec(t, nt, map[string]any{"action": "list", "chat_id": i + 400})
		if !strings.Contains(result, "1 note(s)") {
			t.Errorf("chat %d: expected '1 notes', got %q", i+400, result)
		}
	}
}

// ── search ────────────────────────────────────────────────────────────────────

func TestNotesSearch(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))

	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Go lang note", "content": "Goroutines are amazing", "chat_id": 500,
	})
	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Python note", "content": "Lists and dicts", "chat_id": 500,
	})

	result, err := notesExec(t, nt, map[string]any{
		"action": "search", "query": "goroutines", "chat_id": 500,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToLower(result), "go lang") {
		t.Errorf("expected 'Go lang' in result, got %q", result)
	}
	if strings.Contains(result, "Python") {
		t.Errorf("did not expect 'Python' in result, got %q", result)
	}
}

func TestNotesSearchNotFound(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, err := notesExec(t, nt, map[string]any{
		"action": "search", "query": "nonexistent", "chat_id": 600,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No notes") {
		t.Errorf("expected 'No notes' in result, got %q", result)
	}
}

func TestNotesSearchByTag(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Work", "content": "Work stuff", "tags": "work,important", "chat_id": 700,
	})
	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Personal", "content": "Personal stuff", "tags": "personal", "chat_id": 700,
	})

	result, err := notesExec(t, nt, map[string]any{
		"action": "search", "query": "important", "chat_id": 700,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Work") {
		t.Errorf("expected 'Work' note in result, got %q", result)
	}
	if strings.Contains(result, "Personal") {
		t.Errorf("did not expect 'Personal' note in result, got %q", result)
	}
}

// ── error cases ───────────────────────────────────────────────────────────────

func TestNotesCreateNoTitle(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "create", "chat_id": 800})
	if err == nil {
		t.Fatal("expected error when title is missing")
	}
}

func TestNotesSearchNoQuery(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "search", "chat_id": 800})
	if err == nil {
		t.Fatal("expected error when query is missing")
	}
}

func TestNotesDeleteNoID(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "delete", "chat_id": 800})
	if err == nil {
		t.Fatal("expected error for delete without id")
	}
}

func TestNotesUnknownAction(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "fly", "chat_id": 800})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestNotesInvalidJSON(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := nt.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── get ───────────────────────────────────────────────────────────────────────

func TestNotesGet(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, _ := notesExec(t, nt, map[string]any{
		"action": "create", "title": "Get me", "content": "Full content here", "chat_id": 900,
	})
	var id int64
	if _, err := fmt.Sscanf(result, "Note created (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id from %q: %v", result, err)
	}

	out, err := notesExec(t, nt, map[string]any{"action": "get", "id": id, "chat_id": 900})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Full content here") {
		t.Errorf("expected full content in result, got %q", out)
	}
}

func TestNotesGetNotFound(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "get", "id": 9999, "chat_id": 900})
	if err == nil {
		t.Fatal("expected error for nonexistent note")
	}
}

// ── edit ──────────────────────────────────────────────────────────────────────

func TestNotesEdit(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, _ := notesExec(t, nt, map[string]any{
		"action": "create", "title": "Old title", "content": "Old content", "tags": "old", "chat_id": 1000,
	})
	var id int64
	if _, err := fmt.Sscanf(result, "Note created (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id: %v", err)
	}

	_, err := notesExec(t, nt, map[string]any{
		"action": "edit", "id": id, "title": "New title", "chat_id": 1000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, _ := notesExec(t, nt, map[string]any{"action": "get", "id": id, "chat_id": 1000})
	if !strings.Contains(out, "New title") {
		t.Errorf("expected updated title, got %q", out)
	}
	// Content and tags should be preserved.
	if !strings.Contains(out, "Old content") {
		t.Errorf("expected original content preserved, got %q", out)
	}
}

// ── delete ────────────────────────────────────────────────────────────────────

func TestNotesDelete(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, _ := notesExec(t, nt, map[string]any{
		"action": "create", "title": "To delete", "content": "bye", "chat_id": 1100,
	})
	var id int64
	if _, err := fmt.Sscanf(result, "Note created (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id: %v", err)
	}

	_, err := notesExec(t, nt, map[string]any{"action": "delete", "id": id, "chat_id": 1100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be gone.
	out, _ := notesExec(t, nt, map[string]any{"action": "list", "chat_id": 1100})
	if !strings.Contains(out, "No notes") {
		t.Errorf("expected empty list after delete, got %q", out)
	}
}

func TestNotesDeleteWrongUser(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	result, _ := notesExec(t, nt, map[string]any{
		"action": "create", "title": "Private", "content": "secret", "chat_id": 1200,
	})
	var id int64
	if _, err := fmt.Sscanf(result, "Note created (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id: %v", err)
	}

	// Another user tries to delete.
	_, err := notesExec(t, nt, map[string]any{"action": "delete", "id": id, "chat_id": 9999})
	if err == nil {
		t.Fatal("expected error when deleting someone else's note")
	}
}

// ── FTS5 search ───────────────────────────────────────────────────────────────

func TestNotesFTSMultiWord(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	if !nt.DB().HasNotesFTS() {
		t.Skip("FTS5 not available")
	}

	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Deploy guide",
		"content": "How to deploy kubernetes cluster in production", "chat_id": 1300,
	})
	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Shopping list",
		"content": "Milk eggs bread", "chat_id": 1300,
	})

	// Multi-word query: both words must match
	out, err := notesExec(t, nt, map[string]any{
		"action": "search", "query": "kubernetes production", "chat_id": 1300,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Deploy guide") {
		t.Errorf("expected 'Deploy guide' in FTS results, got %q", out)
	}
	if strings.Contains(out, "Shopping") {
		t.Errorf("did not expect 'Shopping' in FTS results, got %q", out)
	}
}

func TestNotesFTSStemming(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	if !nt.DB().HasNotesFTS() {
		t.Skip("FTS5 not available")
	}

	_, _ = notesExec(t, nt, map[string]any{
		"action": "create", "title": "Running notes",
		"content": "I enjoy running in the morning", "chat_id": 1400,
	})

	// Porter stemmer: "run" should match "running"
	out, err := notesExec(t, nt, map[string]any{
		"action": "search", "query": "run", "chat_id": 1400,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Running notes") {
		t.Errorf("expected stemming to match 'running' with 'run', got %q", out)
	}
}
