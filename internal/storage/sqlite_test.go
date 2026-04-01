package storage_test

import (
	"context"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	return db
}

// ── conversations ─────────────────────────────────────────────────────────────

func TestCreateAndGetConversation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateConversation(ctx, 100, "test-model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero conversation id")
	}

	conv, err := db.GetActiveConversation(ctx, 100)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conv == nil {
		t.Fatal("expected conversation, got nil")
	}
	if conv.ID != id {
		t.Errorf("expected id %d, got %d", id, conv.ID)
	}
	if conv.ChatID != 100 {
		t.Errorf("expected chat_id 100, got %d", conv.ChatID)
	}
	if conv.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", conv.Model)
	}
}

func TestGetActiveConversationNotFound(t *testing.T) {
	db := openTestDB(t)
	conv, err := db.GetActiveConversation(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conv != nil {
		t.Errorf("expected nil, got %+v", conv)
	}
}

func TestGetActiveMostRecent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, err := db.CreateConversation(ctx, 200, "model-1")
	if err != nil {
		t.Fatalf("create first conversation: %v", err)
	}
	id2, err := db.CreateConversation(ctx, 200, "model-2")
	if err != nil {
		t.Fatalf("create second conversation: %v", err)
	}

	// When updated_at values are equal (same-second precision), the query falls
	// back to ORDER BY id DESC — so the higher id wins.
	conv, err := db.GetActiveConversation(ctx, 200)
	if err != nil {
		t.Fatalf("get active conversation: %v", err)
	}
	if conv.ID != id2 {
		t.Errorf("expected most-recent conversation id=%d, got id=%d", id2, conv.ID)
	}
}

// ── messages ──────────────────────────────────────────────────────────────────

func TestAddAndGetMessages(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 300, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	type row struct{ role, content, toolCallID string }
	rows := []row{
		{"user", "Hello!", ""},
		{"assistant", "Hi there!", ""},
		{"tool", "result data", "call_123"},
	}
	for _, r := range rows {
		if err := db.AddMessage(ctx, convID, r.role, r.content, r.toolCallID, ""); err != nil {
			t.Fatalf("add message %q: %v", r.content, err)
		}
	}

	msgs, err := db.GetMessages(ctx, convID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if m.Role != rows[i].role {
			t.Errorf("msg[%d]: expected role %q, got %q", i, rows[i].role, m.Role)
		}
		if m.Content != rows[i].content {
			t.Errorf("msg[%d]: expected content %q, got %q", i, rows[i].content, m.Content)
		}
		if m.ToolCallID != rows[i].toolCallID {
			t.Errorf("msg[%d]: expected tool_call_id %q, got %q", i, rows[i].toolCallID, m.ToolCallID)
		}
	}
}

func TestAddMessageWithToolCallsJSON(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 400, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	const toolCallsJSON = `[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]`
	if err := db.AddMessage(ctx, convID, "assistant", "", "", toolCallsJSON); err != nil {
		t.Fatalf("add message: %v", err)
	}

	msgs, err := db.GetMessages(ctx, convID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ToolCallsJSON != toolCallsJSON {
		t.Errorf("expected tool_calls_json %q, got %q", toolCallsJSON, msgs[0].ToolCallsJSON)
	}
}

func TestGetMessagesEmpty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 500, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	msgs, err := db.GetMessages(ctx, convID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

// ── notes ─────────────────────────────────────────────────────────────────────

func TestCreateAndSearchNotesByTitle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 600, "My Title", "My Content", "tag1,tag2")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero note id")
	}

	notes, err := db.SearchNotes(ctx, 600, "My Title")
	if err != nil {
		t.Fatalf("search notes: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 note, got %d", len(notes))
	}
	if notes[0].Title != "My Title" {
		t.Errorf("expected title 'My Title', got %q", notes[0].Title)
	}
	if notes[0].Content != "My Content" {
		t.Errorf("expected content 'My Content', got %q", notes[0].Content)
	}
	if notes[0].Tags != "tag1,tag2" {
		t.Errorf("expected tags 'tag1,tag2', got %q", notes[0].Tags)
	}
}

func TestSearchNotesByContent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, _ = db.CreateNote(ctx, 700, "Note 1", "About Go programming", "go")
	_, _ = db.CreateNote(ctx, 700, "Note 2", "About Python programming", "python")
	_, _ = db.CreateNote(ctx, 700, "Note 3", "About cooking", "food")

	notes, err := db.SearchNotes(ctx, 700, "programming")
	if err != nil {
		t.Fatalf("search notes: %v", err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes, got %d", len(notes))
	}
}

func TestSearchNotesByTag(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, _ = db.CreateNote(ctx, 800, "Note 1", "Content 1", "go,backend")
	_, _ = db.CreateNote(ctx, 800, "Note 2", "Content 2", "python,backend")
	_, _ = db.CreateNote(ctx, 800, "Note 3", "Content 3", "cooking")

	notes, err := db.SearchNotes(ctx, 800, "backend")
	if err != nil {
		t.Fatalf("search notes: %v", err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes with 'backend' tag, got %d", len(notes))
	}
}

func TestSearchNotesEmpty(t *testing.T) {
	db := openTestDB(t)
	notes, err := db.SearchNotes(context.Background(), 900, "anything")
	if err != nil {
		t.Fatalf("search notes: %v", err)
	}
	if len(notes) != 0 {
		t.Errorf("expected 0 notes, got %d", len(notes))
	}
}

func TestSearchNotesListAll(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, _ = db.CreateNote(ctx, 1000, "Note A", "Content A", "")
	_, _ = db.CreateNote(ctx, 1000, "Note B", "Content B", "")
	_, _ = db.CreateNote(ctx, 1000, "Note C", "Content C", "")

	// Empty query should return all notes.
	notes, err := db.SearchNotes(ctx, 1000, "")
	if err != nil {
		t.Fatalf("list all notes: %v", err)
	}
	if len(notes) != 3 {
		t.Errorf("expected 3 notes, got %d", len(notes))
	}
}

func TestNotesIsolatedByChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	_, _ = db.CreateNote(ctx, 1100, "User 1 Note", "Content", "")
	_, _ = db.CreateNote(ctx, 1200, "User 2 Note", "Content", "")

	notes1, _ := db.SearchNotes(ctx, 1100, "")
	notes2, _ := db.SearchNotes(ctx, 1200, "")

	if len(notes1) != 1 || notes1[0].Title != "User 1 Note" {
		t.Errorf("unexpected notes for user 1100: %+v", notes1)
	}
	if len(notes2) != 1 || notes2[0].Title != "User 2 Note" {
		t.Errorf("unexpected notes for user 1200: %+v", notes2)
	}
}
