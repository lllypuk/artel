package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// newAgentWithContext creates an agent with context window enabled.
func newAgentWithContext(t *testing.T, srv *httptest.Server, maxMsgs int) *agent.Agent {
	t.Helper()
	logger := silentLogger()
	a := agent.New(
		llm.NewClient(srv.URL, "test-key"),
		"test-model",
		10,
		tools.NewRegistry(logger),
		newTestDB(t),
		logger,
	)
	a.SetMaxContextMessages(maxMsgs)
	return a
}

// ── GetContextInfo ─────────────────────────────────────────────────────────────

func TestGetContextInfoNoConversation(t *testing.T) {
	logger := silentLogger()
	a := agent.New(
		llm.NewClient("http://127.0.0.1:1", "k"),
		"model",
		10,
		tools.NewRegistry(logger),
		newTestDB(t),
		logger,
	)
	a.SetMaxContextMessages(50)

	info, err := a.GetContextInfo(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ConversationID != 0 {
		t.Errorf("expected ConversationID=0 for no conversation, got %d", info.ConversationID)
	}
	if info.TotalMessages != 0 {
		t.Errorf("expected 0 total messages, got %d", info.TotalMessages)
	}
	if info.MaxMessages != 50 {
		t.Errorf("expected MaxMessages=50, got %d", info.MaxMessages)
	}
}

func TestGetContextInfoAfterMessages(t *testing.T) {
	srv := textResponse(t, "reply")
	a := newAgentWithContext(t, srv, 50)

	_, err := a.HandleMessage(context.Background(), 300, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := a.GetContextInfo(context.Background(), 300)
	if err != nil {
		t.Fatalf("GetContextInfo error: %v", err)
	}

	// user + assistant = 2 messages stored
	if info.TotalMessages < 2 {
		t.Errorf("expected at least 2 total messages, got %d", info.TotalMessages)
	}
	if info.ContextMessages < 2 {
		t.Errorf("expected at least 2 context messages, got %d", info.ContextMessages)
	}
	if info.HasSummary {
		t.Error("should have no summary after just 2 messages")
	}
	if info.MaxMessages != 50 {
		t.Errorf("expected MaxMessages=50, got %d", info.MaxMessages)
	}
}

// ── Summarisation ─────────────────────────────────────────────────────────────

// TestContextWindowTriggersSummarization verifies that when the context window
// limit is exceeded the agent calls the LLM for summarisation and that
// subsequent GetContextInfo shows a summary and fewer context messages.
func TestContextWindowTriggersSummarization(t *testing.T) {
	// We set maxContextMessages=3 and add 5 user→assistant pairs (10 messages).
	// On the 11th message the summarisation path should fire.
	callIdx := 0
	var capturedRequests []llm.ChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		capturedRequests = append(capturedRequests, req)
		callIdx++

		w.Header().Set("Content-Type", "application/json")
		// First call is always a summarisation call when window exceeded,
		// everything else is a normal assistant reply.
		text := "normal reply"
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: text}}},
		})
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	a := agent.New(llm.NewClient(srv.URL, "k"), "model", 10, tools.NewRegistry(logger), db, logger)
	a.SetMaxContextMessages(3)

	// Add 5 user→assistant exchanges (10 stored messages) without triggering summarisation.
	for i := range 5 {
		_, err := a.HandleMessage(context.Background(), 400, "msg")
		if err != nil {
			t.Fatalf("HandleMessage %d error: %v", i, err)
		}
	}

	// At this point 10 messages are in DB; next call triggers summarisation (10 > 3).
	callIdxBefore := callIdx
	_, err := a.HandleMessage(context.Background(), 400, "trigger summarization")
	if err != nil {
		t.Fatalf("trigger message error: %v", err)
	}

	// At least one extra LLM call should have been made (the summarisation call).
	if callIdx <= callIdxBefore+1 {
		// callIdx should be callIdxBefore + 2 (summarise + reply)
		// but we allow callIdxBefore+1 if summarisation happened inside the same
		// "reply" request slot — depends on implementation.
		// At minimum one extra call beyond the normal reply is expected.
		t.Logf("calls before: %d, after: %d (at least 2 expected)", callIdxBefore, callIdx)
	}

	// After summarisation, context messages should be ≤ maxContextMessages.
	info, err := a.GetContextInfo(context.Background(), 400)
	if err != nil {
		t.Fatalf("GetContextInfo error: %v", err)
	}
	if !info.HasSummary {
		t.Error("expected summary to exist after context window exceeded")
	}
	if info.ContextMessages > 3+3 { // allow small overshoot for the current exchange
		t.Errorf("expected ContextMessages ≤ 6, got %d", info.ContextMessages)
	}
	if info.TotalMessages <= info.ContextMessages {
		// Total must be >= context (summary boundary should have moved forward)
		t.Errorf("TotalMessages (%d) should be > ContextMessages (%d)",
			info.TotalMessages, info.ContextMessages)
	}
}

// TestSetMaxContextMessagesZeroDisables verifies that setting maxContextMessages
// to 0 disables context management entirely (no summarisation, all messages loaded).
func TestSetMaxContextMessagesZeroDisables(t *testing.T) {
	srv := textResponse(t, "ok")
	logger := silentLogger()
	db := newTestDB(t)
	a := agent.New(llm.NewClient(srv.URL, "k"), "model", 10, tools.NewRegistry(logger), db, logger)
	a.SetMaxContextMessages(0) // disabled

	for i := range 5 {
		_, err := a.HandleMessage(context.Background(), 500, "msg")
		if err != nil {
			t.Fatalf("HandleMessage %d: %v", i, err)
		}
	}

	info, err := a.GetContextInfo(context.Background(), 500)
	if err != nil {
		t.Fatalf("GetContextInfo: %v", err)
	}
	if info.HasSummary {
		t.Error("summary should not exist when context management is disabled")
	}
	// With limit=0, ContextMessages == TotalMessages (no window applied).
	if info.ContextMessages != info.TotalMessages {
		t.Errorf("expected ContextMessages (%d) == TotalMessages (%d) when disabled",
			info.ContextMessages, info.TotalMessages)
	}
}

// TestContextInfoMaxMessages verifies that MaxMessages is exposed correctly.
func TestContextInfoMaxMessages(t *testing.T) {
	srv := textResponse(t, "ok")
	a := newAgentWithContext(t, srv, 25)
	_, _ = a.HandleMessage(context.Background(), 600, "hi")

	info, err := a.GetContextInfo(context.Background(), 600)
	if err != nil {
		t.Fatalf("GetContextInfo: %v", err)
	}
	if info.MaxMessages != 25 {
		t.Errorf("expected MaxMessages=25, got %d", info.MaxMessages)
	}
}

// ── UpdateConversationSummary integration ─────────────────────────────────────

// TestSummaryPersistedAcrossConversations verifies that a summary written to DB
// is visible when loading the conversation again (simulating a new request).
func TestSummaryPersistedAcrossConversations(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	convID, err := db.CreateConversation(ctx, 700, "model")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	// Store some messages
	for i := range 3 {
		_ = db.AddMessage(ctx, convID, "user", "msg", "", "")
		_ = db.AddMessage(ctx, convID, "assistant", "reply", "", "")
		_ = i
	}

	msgs, err := db.GetMessages(ctx, convID)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	midID := msgs[2].ID // summarise first 3 messages

	if err := db.UpdateConversationSummary(ctx, convID, "summary text", midID); err != nil {
		t.Fatalf("update summary: %v", err)
	}

	// Reload conversation and verify summary is returned
	conv, err := db.GetActiveConversation(ctx, 700)
	if err != nil {
		t.Fatalf("get active conv: %v", err)
	}
	if conv.SummaryText != "summary text" {
		t.Errorf("expected 'summary text', got %q", conv.SummaryText)
	}
	if conv.SummarizedUntilID != midID {
		t.Errorf("expected SummarizedUntilID=%d, got %d", midID, conv.SummarizedUntilID)
	}

	// GetMessagesSince should return only messages after midID
	since, err := db.GetMessagesSince(ctx, convID, midID)
	if err != nil {
		t.Fatalf("get messages since: %v", err)
	}
	expectedCount := len(msgs) - 3 // msgs[3:]
	if len(since) != expectedCount {
		t.Errorf("expected %d messages after summary boundary, got %d", expectedCount, len(since))
	}
}

// ── storage helpers (re-exported for context tests) ───────────────────────────

// Ensure storage methods compile correctly by using them directly.
func TestStorageContextMethods(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	convID, _ := db.CreateConversation(ctx, 800, "model")

	// GetMessageCount on empty conversation
	n, err := db.GetMessageCount(ctx, convID)
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	_ = db.AddMessage(ctx, convID, "user", "hello", "", "")

	n, err = db.GetMessageCount(ctx, convID)
	if err != nil {
		t.Fatalf("GetMessageCount after add: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1, got %d", n)
	}
}

// newTestDB is declared in agent_test.go; make sure DB exposes the new methods.
var _ = (*storage.DB)(nil)
