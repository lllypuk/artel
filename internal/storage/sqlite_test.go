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

// ── Notes CRUD ────────────────────────────────────────────────────────────────

func TestGetNote(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1300, "Original Title", "Original Content", "tag1,tag2")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	note, err := db.GetNote(ctx, id, 1300)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if note == nil {
		t.Fatal("expected note, got nil")
	}
	if note.ID != id {
		t.Errorf("expected id %d, got %d", id, note.ID)
	}
	if note.ChatID != 1300 {
		t.Errorf("expected chat_id 1300, got %d", note.ChatID)
	}
	if note.Title != "Original Title" {
		t.Errorf("expected title %q, got %q", "Original Title", note.Title)
	}
	if note.Content != "Original Content" {
		t.Errorf("expected content %q, got %q", "Original Content", note.Content)
	}
	if note.Tags != "tag1,tag2" {
		t.Errorf("expected tags %q, got %q", "tag1,tag2", note.Tags)
	}
	if note.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
	if note.UpdatedAt.IsZero() {
		t.Error("expected updated_at to be set")
	}
}

func TestGetNote_NotFound(t *testing.T) {
	db := openTestDB(t)

	note, err := db.GetNote(context.Background(), 99999, 1301)
	if err == nil {
		t.Fatal("expected error for missing note")
	}
	if note != nil {
		t.Errorf("expected nil note, got %+v", note)
	}
}

func TestGetNote_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1302, "Private", "Secret", "hidden")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	note, err := db.GetNote(ctx, id, 1303)
	if err == nil {
		t.Fatal("expected error when accessing note from another chat")
	}
	if note != nil {
		t.Errorf("expected nil note, got %+v", note)
	}

	ownerNote, err := db.GetNote(ctx, id, 1302)
	if err != nil {
		t.Fatalf("get note as owner: %v", err)
	}
	if ownerNote == nil || ownerNote.Title != "Private" {
		t.Errorf("unexpected owner note: %+v", ownerNote)
	}
}

func TestUpdateNote(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1304, "Before", "Old content", "old")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	before, err := db.GetNote(ctx, id, 1304)
	if err != nil {
		t.Fatalf("get note before update: %v", err)
	}

	if err := db.UpdateNote(ctx, id, 1304, "After", "New content", "new,updated"); err != nil {
		t.Fatalf("update note: %v", err)
	}

	after, err := db.GetNote(ctx, id, 1304)
	if err != nil {
		t.Fatalf("get note after update: %v", err)
	}
	if after.Title != "After" {
		t.Errorf("expected title %q, got %q", "After", after.Title)
	}
	if after.Content != "New content" {
		t.Errorf("expected content %q, got %q", "New content", after.Content)
	}
	if after.Tags != "new,updated" {
		t.Errorf("expected tags %q, got %q", "new,updated", after.Tags)
	}
	if after.CreatedAt.IsZero() || after.UpdatedAt.IsZero() {
		t.Errorf("expected timestamps to be set, got created_at=%v updated_at=%v", after.CreatedAt, after.UpdatedAt)
	}
	if before.ID != after.ID {
		t.Errorf("expected id to stay %d, got %d", before.ID, after.ID)
	}
	if after.ChatID != 1304 {
		t.Errorf("expected chat_id 1304, got %d", after.ChatID)
	}
}

func TestUpdateNote_WrongChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1305, "Before", "Original", "tag")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	err = db.UpdateNote(ctx, id, 1306, "After", "Changed", "new")
	if err == nil {
		t.Fatal("expected error when updating note from another chat")
	}

	note, err := db.GetNote(ctx, id, 1305)
	if err != nil {
		t.Fatalf("get note after failed update: %v", err)
	}
	if note.Title != "Before" {
		t.Errorf("expected title %q, got %q", "Before", note.Title)
	}
	if note.Content != "Original" {
		t.Errorf("expected content %q, got %q", "Original", note.Content)
	}
	if note.Tags != "tag" {
		t.Errorf("expected tags %q, got %q", "tag", note.Tags)
	}
}

func TestDeleteNote(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1307, "Delete me", "Soon gone", "trash")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	if err := db.DeleteNote(ctx, id, 1307); err != nil {
		t.Fatalf("delete note: %v", err)
	}

	note, err := db.GetNote(ctx, id, 1307)
	if err == nil {
		t.Fatal("expected error when loading deleted note")
	}
	if note != nil {
		t.Errorf("expected nil note after delete, got %+v", note)
	}
}

func TestUpdateNote_NotFound(t *testing.T) {
	db := openTestDB(t)

	err := db.UpdateNote(context.Background(), 99999, 1310, "Title", "Content", "tag")
	if err == nil {
		t.Fatal("expected error when updating non-existent note")
	}
}

func TestDeleteNote_NotFound(t *testing.T) {
	db := openTestDB(t)

	err := db.DeleteNote(context.Background(), 99999, 1311)
	if err == nil {
		t.Fatal("expected error when deleting non-existent note")
	}
}

func TestDeleteNote_WrongChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateNote(ctx, 1308, "Keep me", "Still here", "safe")
	if err != nil {
		t.Fatalf("create note: %v", err)
	}

	err = db.DeleteNote(ctx, id, 1309)
	if err == nil {
		t.Fatal("expected error when deleting note from another chat")
	}

	note, err := db.GetNote(ctx, id, 1308)
	if err != nil {
		t.Fatalf("get note after failed delete: %v", err)
	}
	if note == nil {
		t.Fatal("expected note to remain after failed delete")
	}
	if note.Title != "Keep me" {
		t.Errorf("expected title %q, got %q", "Keep me", note.Title)
	}
}

// ── context management ─────────────────────────────────────────────────────────────────────────────────

func TestGetMessagesSince(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 2000, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Add 5 messages
	var ids []int64
	for range 5 {
		// We add messages and then retrieve their IDs via GetMessages.
		_ = db.AddMessage(ctx, convID, "user", "msg", "", "")
	}
	msgs, err := db.GetMessages(ctx, convID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}

	// GetMessagesSince(afterID=0) should return all 5
	all, err := db.GetMessagesSince(ctx, convID, 0)
	if err != nil {
		t.Fatalf("get messages since 0: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5 messages, got %d", len(all))
	}

	// GetMessagesSince(afterID=ids[1]) should return messages 3,4,5 (after the second)
	partial, err := db.GetMessagesSince(ctx, convID, ids[1])
	if err != nil {
		t.Fatalf("get messages since ids[1]: %v", err)
	}
	if len(partial) != 3 {
		t.Errorf("expected 3 messages, got %d", len(partial))
	}

	// GetMessagesSince(afterID=last) should return 0 messages
	none, err := db.GetMessagesSince(ctx, convID, ids[len(ids)-1])
	if err != nil {
		t.Fatalf("get messages since last: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 messages, got %d", len(none))
	}
}

func TestGetMessageCount(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 2100, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	count, err := db.GetMessageCount(ctx, convID)
	if err != nil {
		t.Fatalf("get count (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	for range 7 {
		_ = db.AddMessage(ctx, convID, "user", "msg", "", "")
	}
	count, err = db.GetMessageCount(ctx, convID)
	if err != nil {
		t.Fatalf("get count after inserts: %v", err)
	}
	if count != 7 {
		t.Errorf("expected 7, got %d", count)
	}
}

func TestUpdateConversationSummary(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 2200, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Initially no summary
	conv, err := db.GetActiveConversation(ctx, 2200)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conv.SummaryText != "" {
		t.Errorf("expected empty summary, got %q", conv.SummaryText)
	}
	if conv.SummarizedUntilID != 0 {
		t.Errorf("expected SummarizedUntilID=0, got %d", conv.SummarizedUntilID)
	}

	// Add a summary
	if err := db.UpdateConversationSummary(ctx, convID, "Тестовое резюме", 42); err != nil {
		t.Fatalf("update summary: %v", err)
	}

	// Reload and verify
	conv, err = db.GetActiveConversation(ctx, 2200)
	if err != nil {
		t.Fatalf("get conversation after update: %v", err)
	}
	if conv.SummaryText != "Тестовое резюме" {
		t.Errorf("expected summary 'Тестовое резюме', got %q", conv.SummaryText)
	}
	if conv.SummarizedUntilID != 42 {
		t.Errorf("expected SummarizedUntilID=42, got %d", conv.SummarizedUntilID)
	}

	// Update summary again
	if err := db.UpdateConversationSummary(ctx, convID, "Новое резюме", 100); err != nil {
		t.Fatalf("update summary again: %v", err)
	}
	conv, err = db.GetActiveConversation(ctx, 2200)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if conv.SummaryText != "Новое резюме" {
		t.Errorf("expected updated summary, got %q", conv.SummaryText)
	}
	if conv.SummarizedUntilID != 100 {
		t.Errorf("expected SummarizedUntilID=100, got %d", conv.SummarizedUntilID)
	}
}
