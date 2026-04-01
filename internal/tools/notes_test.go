package tools_test

import (
	"context"
	"encoding/json"
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
		if !strings.Contains(result, "1 notes") {
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

func TestNotesUnknownAction(t *testing.T) {
	nt := tools.NewNotesTool(openNotesDB(t))
	_, err := notesExec(t, nt, map[string]any{"action": "delete", "chat_id": 800})
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
