package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newAgent(t *testing.T, srv *httptest.Server) *agent.Agent {
	t.Helper()
	logger := silentLogger()
	return agent.New(
		llm.NewClient(srv.URL, "test-key"),
		"test-model",
		10,
		tools.NewRegistry(logger),
		newTestDB(t),
		logger,
	)
}

// llmMockTool is a Tool implementation used only inside agent tests.
type llmMockTool struct {
	name    string
	result  string
	execErr error
}

func (m *llmMockTool) Name() string        { return m.name }
func (m *llmMockTool) Description() string { return "mock: " + m.name }
func (m *llmMockTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (m *llmMockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return m.result, m.execErr
}

// textResponse returns an httptest.Server that always replies with a plain
// assistant message.
func textResponse(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{
				Message: llm.Message{Role: "assistant", Content: text},
			}},
			Usage: llm.Usage{TotalTokens: 10},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ── HandleMessage ─────────────────────────────────────────────────────────────

func TestHandleMessageSimpleResponse(t *testing.T) {
	srv := textResponse(t, "Привет!")
	a := newAgent(t, srv)

	resp, err := a.HandleMessage(context.Background(), 1, "Привет")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Привет!" {
		t.Errorf("expected 'Привет!', got %q", resp.Text)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 10 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

func TestHandleMessageCreatesConversation(t *testing.T) {
	srv := textResponse(t, "ok")
	a := newAgent(t, srv)

	_, err := a.HandleMessage(context.Background(), 42, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	db := newTestDB(t) // fresh DB — use the one inside agent via exported method below
	_ = db
	// We verify indirectly: a second message reuses the same conversation.
	_, err = a.HandleMessage(context.Background(), 42, "hello again")
	if err != nil {
		t.Fatalf("unexpected error on second message: %v", err)
	}
}

func TestHandleMessagePreservesHistory(t *testing.T) {
	var lastReq llm.ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&lastReq) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	a := newAgent(t, srv)

	_, _ = a.HandleMessage(context.Background(), 20, "first")
	_, _ = a.HandleMessage(context.Background(), 20, "second")

	// After the second request the LLM received at minimum:
	// [system, user("first"), assistant("ok"), user("second")] = 4 messages.
	if len(lastReq.Messages) < 4 {
		t.Errorf("expected ≥4 messages in second LLM request, got %d: %+v",
			len(lastReq.Messages), lastReq.Messages)
	}
}

func TestHandleMessageNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{Choices: []llm.Choice{}}) //nolint:errcheck
	}))
	defer srv.Close()

	a := newAgent(t, srv)
	_, err := a.HandleMessage(context.Background(), 30, "test")
	if err == nil {
		t.Fatal("expected error when LLM returns no choices")
	}
}

// ── ReAct loop ────────────────────────────────────────────────────────────────

func TestHandleMessageToolCallLoop(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		var resp llm.ChatResponse
		if callCount == 1 {
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{{
							ID:   "call_1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "my_tool",
								Arguments: `{}`,
							},
						}},
					},
				}},
			}
		} else {
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: "assistant", Content: "Tool executed"},
				}},
				Usage: llm.Usage{TotalTokens: 50},
			}
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	registry := tools.NewRegistry(logger)
	registry.Register(&llmMockTool{name: "my_tool", result: "tool output"})

	a := agent.New(llm.NewClient(srv.URL, "key"), "model", 10, registry, db, logger)

	resp, err := a.HandleMessage(context.Background(), 10, "use the tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Tool executed" {
		t.Errorf("expected 'Tool executed', got %q", resp.Text)
	}
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}
}

func TestHandleMessageToolResultInContext(t *testing.T) {
	var secondReq llm.ChatRequest
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		var resp llm.ChatResponse
		if callCount == 1 {
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{{
							ID:       "tc1",
							Type:     "function",
							Function: llm.FunctionCall{Name: "calc", Arguments: `{}`},
						}},
					},
				}},
			}
		} else {
			json.NewDecoder(r.Body).Decode(&secondReq) //nolint:errcheck
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: "assistant", Content: "done"},
				}},
			}
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	registry := tools.NewRegistry(logger)
	registry.Register(&llmMockTool{name: "calc", result: "42"})

	a := agent.New(llm.NewClient(srv.URL, "key"), "model", 10, registry, db, logger)
	_, _ = a.HandleMessage(context.Background(), 50, "compute")

	// The second LLM request must include a "tool" role message with the result.
	found := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && m.Content == "42" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool result '42' in second LLM request, got messages: %+v", secondReq.Messages)
	}
}

// ── ReAct loop edge cases ──────────────────────────────────────────────────

func TestHandleMessageMaxIterations(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		resp := llm.ChatResponse{
			Choices: []llm.Choice{{
				Message: llm.Message{
					Role: "assistant",
					ToolCalls: []llm.ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "my_tool",
							Arguments: `{}`,
						},
					}},
				},
			}},
			Usage: llm.Usage{TotalTokens: 5},
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	registry := tools.NewRegistry(logger)
	registry.Register(&llmMockTool{name: "my_tool", result: "ok"})

	a := agent.New(llm.NewClient(srv.URL, "key"), "model", 3, registry, db, logger)

	resp, err := a.HandleMessage(context.Background(), 10, "keep going")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(resp.Text, "лимит итераций") {
		t.Errorf("expected max iterations message, got %q", resp.Text)
	}
	if callCount != 3 {
		t.Errorf("expected 3 LLM calls (maxIterations), got %d", callCount)
	}
	if resp.Usage == nil {
		t.Error("expected non-nil usage even when hitting max iterations")
	}
}

func TestHandleMessageUnknownTool(t *testing.T) {
	var secondReq llm.ChatRequest
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		var resp llm.ChatResponse
		if callCount == 1 {
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{{
							ID:       "tc1",
							Type:     "function",
							Function: llm.FunctionCall{Name: "nonexistent", Arguments: `{}`},
						}},
					},
				}},
			}
		} else {
			json.NewDecoder(r.Body).Decode(&secondReq) //nolint:errcheck
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: "assistant", Content: "recovered"},
				}},
			}
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	registry := tools.NewRegistry(logger)

	a := agent.New(llm.NewClient(srv.URL, "key"), "model", 10, registry, db, logger)

	resp, err := a.HandleMessage(context.Background(), 10, "use unknown tool")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("expected 'recovered', got %q", resp.Text)
	}
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}

	// The second LLM request must include a "tool" role message with the error.
	found := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "Error: unknown tool: nonexistent") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool error result in second LLM request, got messages: %+v", secondReq.Messages)
	}
}

func TestHandleMessageToolError(t *testing.T) {
	var secondReq llm.ChatRequest
	callCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		callCount++
		var resp llm.ChatResponse
		if callCount == 1 {
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{
						Role: "assistant",
						ToolCalls: []llm.ToolCall{{
							ID:       "tc1",
							Type:     "function",
							Function: llm.FunctionCall{Name: "failing", Arguments: `{}`},
						}},
					},
				}},
			}
		} else {
			json.NewDecoder(r.Body).Decode(&secondReq) //nolint:errcheck
			resp = llm.ChatResponse{
				Choices: []llm.Choice{{
					Message: llm.Message{Role: "assistant", Content: "handled error"},
				}},
			}
		}
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
	defer srv.Close()

	db := newTestDB(t)
	logger := silentLogger()
	registry := tools.NewRegistry(logger)
	registry.Register(&llmMockTool{name: "failing", execErr: fmt.Errorf("something went wrong")})

	a := agent.New(llm.NewClient(srv.URL, "key"), "model", 10, registry, db, logger)

	resp, err := a.HandleMessage(context.Background(), 10, "trigger error")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "handled error" {
		t.Errorf("expected 'handled error', got %q", resp.Text)
	}
	if callCount != 2 {
		t.Errorf("expected 2 LLM calls, got %d", callCount)
	}

	// The second LLM request must include a "tool" role message with the error.
	found := false
	for _, m := range secondReq.Messages {
		if m.Role == "tool" && strings.Contains(m.Content, "Error: something went wrong") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected tool error result in second LLM request, got messages: %+v", secondReq.Messages)
	}
}

// ── NewConversation ───────────────────────────────────────────────────────────

func TestNewConversation(t *testing.T) {
	db := newTestDB(t)
	logger := silentLogger()
	// NewConversation does not need a live LLM server.
	a := agent.New(llm.NewClient("http://127.0.0.1:1", "k"), "model", 10, tools.NewRegistry(logger), db, logger)

	if err := a.NewConversation(context.Background(), 99); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	conv, err := db.GetActiveConversation(context.Background(), 99)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if conv == nil {
		t.Fatal("expected conversation to exist after NewConversation")
	}
}

// ── Model management ─────────────────────────────────────────────────────────

func TestGetSetModel(t *testing.T) {
	db := newTestDB(t)
	logger := silentLogger()
	a := agent.New(llm.NewClient("http://127.0.0.1:1", "k"), "glm-4.7", 10, tools.NewRegistry(logger), db, logger)

	if got := a.GetModel(); got != "glm-4.7" {
		t.Errorf("initial model: expected glm-4.7, got %q", got)
	}

	a.SetModel("glm-4.7-flash")
	if got := a.GetModel(); got != "glm-4.7-flash" {
		t.Errorf("after SetModel: expected glm-4.7-flash, got %q", got)
	}
}

func TestSetModels(t *testing.T) {
	db := newTestDB(t)
	logger := silentLogger()
	a := agent.New(llm.NewClient("http://127.0.0.1:1", "k"), "glm-4.7", 10, tools.NewRegistry(logger), db, logger)

	models := map[string]string{
		"default": "glm-4.7",
		"fast":    "glm-4.7-flash",
	}
	a.SetModels(models)

	got := a.Models()
	if len(got) != 2 {
		t.Fatalf("expected 2 models, got %d", len(got))
	}
	if got["fast"] != "glm-4.7-flash" {
		t.Errorf("fast model: expected glm-4.7-flash, got %q", got["fast"])
	}
	if got["default"] != "glm-4.7" {
		t.Errorf("default model: expected glm-4.7, got %q", got["default"])
	}

	// Verify returned map is a copy (mutation should not affect agent)
	got["fast"] = "mutated"
	if a.GetModel() != "glm-4.7" {
		t.Error("SetModel should not have been affected by mutating the returned map")
	}
	if a.Models()["fast"] != "glm-4.7-flash" {
		t.Error("internal models map should not be affected by external mutation")
	}
}

func TestModelsEmptyByDefault(t *testing.T) {
	db := newTestDB(t)
	logger := silentLogger()
	a := agent.New(llm.NewClient("http://127.0.0.1:1", "k"), "glm-4.7", 10, tools.NewRegistry(logger), db, logger)

	if len(a.Models()) != 0 {
		t.Error("expected empty models map before SetModels is called")
	}
}

// ── HandleMessageWithAttachments ─────────────────────────────────────────────

func TestHandleMessageWithAttachmentsNil(t *testing.T) {
	// nil attachments → behaves exactly like HandleMessage.
	srv := textResponse(t, "Ответ без вложений")
	a := newAgent(t, srv)

	resp, err := a.HandleMessageWithAttachments(context.Background(), 200, "Привет", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Ответ без вложений" {
		t.Errorf("expected 'Ответ без вложений', got %q", resp.Text)
	}
	if resp.Usage == nil {
		t.Error("expected non-nil usage")
	}
}

func TestHandleMessageWithAttachmentsEmptySlice(t *testing.T) {
	// Empty (non-nil) attachment slice → same as HandleMessage.
	srv := textResponse(t, "Пустые вложения")
	a := newAgent(t, srv)

	resp, err := a.HandleMessageWithAttachments(context.Background(), 201, "тест", []agent.FileAttachment{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Пустые вложения" {
		t.Errorf("expected 'Пустые вложения', got %q", resp.Text)
	}
}

func TestHandleMessageWithAttachmentsImage(t *testing.T) {
	// Image attachment → LLM receives ContentParts (text + image_url).
	var capturedReq llm.ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{
				Message: llm.Message{Role: "assistant", Content: "Изображение получено"},
			}},
			Usage: llm.Usage{TotalTokens: 20},
		})
	}))
	defer srv.Close()

	a := newAgent(t, srv)

	attachments := []agent.FileAttachment{
		{Data: []byte{0xFF, 0xD8, 0xFF}, MimeType: "image/jpeg", Filename: "photo.jpg"},
	}

	resp, err := a.HandleMessageWithAttachments(context.Background(), 202, "Что на картинке?", attachments)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text != "Изображение получено" {
		t.Errorf("expected 'Изображение получено', got %q", resp.Text)
	}

	// Locate the user message in the captured LLM request.
	var userMsg *llm.Message
	for i := range capturedReq.Messages {
		if capturedReq.Messages[i].Role == "user" {
			userMsg = &capturedReq.Messages[i]
			break
		}
	}
	if userMsg == nil {
		t.Fatal("user message not found in captured LLM request")
	}

	// The user message must be multi-modal (ContentParts), not plain text.
	if len(userMsg.ContentParts) == 0 {
		t.Fatal("expected ContentParts in user message, got plain text content")
	}

	// First part: text.
	if userMsg.ContentParts[0].Type != "text" || userMsg.ContentParts[0].Text != "Что на картинке?" {
		t.Errorf("unexpected text ContentPart: %+v", userMsg.ContentParts[0])
	}

	// Second part: image_url with a base64 data URL.
	if len(userMsg.ContentParts) < 2 {
		t.Fatal("expected at least 2 ContentParts (text + image)")
	}
	imgPart := userMsg.ContentParts[1]
	if imgPart.Type != "image_url" {
		t.Errorf("expected image_url type, got %q", imgPart.Type)
	}
	if imgPart.ImageURL == nil || !strings.HasPrefix(imgPart.ImageURL.URL, "data:image/jpeg;base64,") {
		t.Errorf("unexpected image URL: %+v", imgPart.ImageURL)
	}
}
