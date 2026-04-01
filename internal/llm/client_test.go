package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/llm"
)

// ── Chat ──────────────────────────────────────────────────────────────────────

func TestChatSuccess(t *testing.T) {
	want := llm.ChatResponse{
		ID: "resp-1",
		Choices: []llm.Choice{{
			Message:      llm.Message{Role: "assistant", Content: "Hello!"},
			FinishReason: "stop",
		}},
		Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("unexpected Authorization: %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(want) //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "test-key")
	resp, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != want.ID {
		t.Errorf("expected id %q, got %q", want.ID, resp.ID)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello!" {
		t.Errorf("unexpected content: %q", resp.Choices[0].Message.Content)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.Usage.TotalTokens)
	}
}

func TestChatAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid api key"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "bad-key")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected '401' in error, got: %v", err)
	}
}

func TestChatRequestPayload(t *testing.T) {
	var received llm.ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:       "glm-4.7",
		Messages:    []llm.Message{{Role: "system", Content: "be helpful"}, {Role: "user", Content: "hi"}},
		MaxTokens:   1024,
		Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.Model != "glm-4.7" {
		t.Errorf("expected model 'glm-4.7', got %q", received.Model)
	}
	if len(received.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(received.Messages))
	}
	if received.MaxTokens != 1024 {
		t.Errorf("expected max_tokens 1024, got %d", received.MaxTokens)
	}
	if received.Stream {
		t.Error("expected stream=false for Chat()")
	}
}

func TestChatServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error")) //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got: %v", err)
	}
}

// ── ChatStream ────────────────────────────────────────────────────────────────

func TestChatStreamSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`{"choices":[{"delta":{"role":"assistant","content":"Hello"}}]}`,
			`{"choices":[{"delta":{"content":", world"}}]}`,
			`{"choices":[{"delta":{"content":"!"}}],"usage":{"total_tokens":10}}`,
		}
		for _, chunk := range chunks {
			w.Write([]byte("data: " + chunk + "\n\n")) //nolint:errcheck
		}
		w.Write([]byte("data: [DONE]\n\n")) //nolint:errcheck
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	var parts []string
	err := client.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}, func(d llm.StreamDelta) {
		if d.Content != "" {
			parts = append(parts, d.Content)
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(parts, ""); got != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got %q", got)
	}
}

func TestChatStreamDoneCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data: [DONE]\n\n")) //nolint:errcheck
	}))
	defer srv.Close()

	doneCalled := false
	client := llm.NewClient(srv.URL, "key")
	err := client.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}, func(d llm.StreamDelta) {
		if d.Done {
			doneCalled = true
		}
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !doneCalled {
		t.Error("expected Done delta to be delivered")
	}
}

func TestChatStreamAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("forbidden")) //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	err := client.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}, func(_ llm.StreamDelta) {})
	if err == nil {
		t.Fatal("expected error for 403 response")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected '403' in error, got: %v", err)
	}
}

func TestChatStreamSetsSteamTrue(t *testing.T) {
	var received llm.ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&received) //nolint:errcheck
		w.Write([]byte("data: [DONE]\n\n"))       //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	_ = client.ChatStream(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	}, func(_ llm.StreamDelta) {})

	if !received.Stream {
		t.Error("expected stream=true for ChatStream()")
	}
}
