package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/llm"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ── Provider interface ───────────────────────────────────────────────────────

func TestClientImplementsProvider(t *testing.T) {
	var _ llm.Provider = (*llm.Client)(nil)
}

func TestMultiClientImplementsProvider(t *testing.T) {
	var _ llm.Provider = (*llm.MultiClient)(nil)
}

// ── APIError ─────────────────────────────────────────────────────────────────

func TestAPIError(t *testing.T) {
	err := &llm.APIError{StatusCode: 500, Body: "internal error"}
	if err.Error() != "API error 500: internal error" {
		t.Errorf("unexpected error string: %s", err.Error())
	}
}

func TestIsRetriable(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retriable bool
	}{
		{"nil", nil, false},
		{"500", &llm.APIError{StatusCode: 500}, true},
		{"502", &llm.APIError{StatusCode: 502}, true},
		{"503", &llm.APIError{StatusCode: 503}, true},
		{"429", &llm.APIError{StatusCode: 429}, true},
		{"400", &llm.APIError{StatusCode: 400}, false},
		{"401", &llm.APIError{StatusCode: 401}, false},
		{"403", &llm.APIError{StatusCode: 403}, false},
		{"404", &llm.APIError{StatusCode: 404}, false},
		{"context canceled", context.Canceled, false},
		{"deadline exceeded", context.DeadlineExceeded, false},
		{"network error", errors.New("connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := llm.IsRetriable(tt.err); got != tt.retriable {
				t.Errorf("IsRetriable(%v) = %v, want %v", tt.err, got, tt.retriable)
			}
		})
	}
}

// ── ClientOption ─────────────────────────────────────────────────────────────

func TestWithExtraHeaders(t *testing.T) {
	var receivedHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key", llm.WithExtraHeaders(map[string]string{
		"HTTP-Referer": "https://example.com",
		"X-Title":      "test-agent",
	}))

	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := receivedHeaders.Get("HTTP-Referer"); got != "https://example.com" {
		t.Errorf("expected HTTP-Referer 'https://example.com', got %q", got)
	}
	if got := receivedHeaders.Get("X-Title"); got != "test-agent" {
		t.Errorf("expected X-Title 'test-agent', got %q", got)
	}
}

func TestChatReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"overloaded"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	client := llm.NewClient(srv.URL, "key")
	_, err := client.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})

	var apiErr *llm.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 503 {
		t.Errorf("expected status 503, got %d", apiErr.StatusCode)
	}
}

// ── MultiClient ──────────────────────────────────────────────────────────────

func okProvider(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{
				Message: llm.Message{Role: "assistant", Content: content},
			}},
			Usage: llm.Usage{TotalTokens: 10},
		})
	}))
}

func failProvider(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		w.Write([]byte(`{"error":"fail"}`)) //nolint:errcheck
	}))
}

func TestMultiClientPrimarySuccess(t *testing.T) {
	primary := okProvider(t, "from primary")
	defer primary.Close()
	fallback := okProvider(t, "from fallback")
	defer fallback.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "primary", Client: llm.NewClient(primary.URL, "k"), Priority: 1},
		{Name: "fallback", Client: llm.NewClient(fallback.URL, "k"), Priority: 2},
	}, llm.MultiClientConfig{MaxRetries: 1}, testLogger())

	resp, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "from primary" {
		t.Errorf("expected 'from primary', got %q", resp.Choices[0].Message.Content)
	}
}

func TestMultiClientFallbackOn5xx(t *testing.T) {
	primary := failProvider(t, 500)
	defer primary.Close()
	fallback := okProvider(t, "from fallback")
	defer fallback.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "primary", Client: llm.NewClient(primary.URL, "k"), Priority: 1},
		{Name: "fallback", Client: llm.NewClient(fallback.URL, "k"), Priority: 2},
	}, llm.MultiClientConfig{MaxRetries: 0}, testLogger())

	resp, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "from fallback" {
		t.Errorf("expected 'from fallback', got %q", resp.Choices[0].Message.Content)
	}
}

func TestMultiClientNoFallbackOn4xx(t *testing.T) {
	primary := failProvider(t, 401)
	defer primary.Close()
	fallback := okProvider(t, "from fallback")
	defer fallback.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "primary", Client: llm.NewClient(primary.URL, "k"), Priority: 1},
		{Name: "fallback", Client: llm.NewClient(fallback.URL, "k"), Priority: 2},
	}, llm.MultiClientConfig{MaxRetries: 0}, testLogger())

	// 4xx is non-retriable but we still try next provider since primary is "errored"
	resp, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "from fallback" {
		t.Errorf("expected 'from fallback', got %q", resp.Choices[0].Message.Content)
	}
}

func TestMultiClientAllProvidersFail(t *testing.T) {
	p1 := failProvider(t, 500)
	defer p1.Close()
	p2 := failProvider(t, 502)
	defer p2.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "p1", Client: llm.NewClient(p1.URL, "k"), Priority: 1},
		{Name: "p2", Client: llm.NewClient(p2.URL, "k"), Priority: 2},
	}, llm.MultiClientConfig{MaxRetries: 0}, testLogger())

	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

func TestMultiClientRetryBeforeFallback(t *testing.T) {
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error")) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "primary", Client: llm.NewClient(srv.URL, "k"), Priority: 1},
	}, llm.MultiClientConfig{
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	}, testLogger())

	resp, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("unexpected content: %q", resp.Choices[0].Message.Content)
	}
	if n := callCount.Load(); n != 3 {
		t.Errorf("expected 3 calls (2 failures + 1 success), got %d", n)
	}
}

func TestMultiClientModelResolution(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.ChatRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		receivedModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{
			Name:   "primary",
			Client: llm.NewClient("http://127.0.0.1:1", "k"),
			Models: map[string]string{
				"default": "glm-4-plus",
				"fast":    "glm-4-flash",
			},
			Priority: 1,
		},
		{
			Name:   "openrouter",
			Client: llm.NewClient(srv.URL, "k"),
			Models: map[string]string{
				"default": "google/gemini-2.5-flash",
				"fast":    "google/gemini-2.5-flash",
			},
			Priority: 2,
		},
	}, llm.MultiClientConfig{MaxRetries: 0}, testLogger())

	// Request with primary's model name should be resolved to openrouter's model via alias lookup
	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "glm-4-plus",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedModel != "google/gemini-2.5-flash" {
		t.Errorf("expected model 'google/gemini-2.5-flash', got %q", receivedModel)
	}
}

func TestMultiClientModelResolutionFast(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.ChatRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		receivedModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{
			Name:     "primary",
			Client:   llm.NewClient("http://127.0.0.1:1", "k"),
			Models:   map[string]string{"default": "glm-4-plus", "fast": "glm-4-flash"},
			Priority: 1,
		},
		{
			Name:     "fallback",
			Client:   llm.NewClient(srv.URL, "k"),
			Models:   map[string]string{"default": "model-a", "fast": "model-b"},
			Priority: 2,
		},
	}, llm.MultiClientConfig{MaxRetries: 0}, testLogger())

	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "glm-4-flash",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedModel != "model-b" {
		t.Errorf("expected 'model-b' (via fast alias), got %q", receivedModel)
	}
}

func TestMultiClientModelPassthrough(t *testing.T) {
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.ChatRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		receivedModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{
			Name:     "provider",
			Client:   llm.NewClient(srv.URL, "k"),
			Models:   map[string]string{"default": "model-a"},
			Priority: 1,
		},
	}, llm.MultiClientConfig{}, testLogger())

	// Unknown model name should pass through as-is
	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "custom-model-xyz",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedModel != "custom-model-xyz" {
		t.Errorf("expected 'custom-model-xyz', got %q", receivedModel)
	}
}

func TestMultiClientProviderStatus(t *testing.T) {
	srv := okProvider(t, "ok")
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "alpha", Client: llm.NewClient(srv.URL, "k"), Priority: 1},
		{Name: "beta", Client: llm.NewClient(srv.URL, "k"), Priority: 2},
	}, llm.MultiClientConfig{}, testLogger())

	status := mc.ProviderStatus()
	if len(status) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(status))
	}
	if status[0].Name != "alpha" || status[0].Priority != 1 || !status[0].Healthy {
		t.Errorf("unexpected status for alpha: %+v", status[0])
	}
	if status[1].Name != "beta" || status[1].Priority != 2 || !status[1].Healthy {
		t.Errorf("unexpected status for beta: %+v", status[1])
	}
}

func TestMultiClientPriorityOrdering(t *testing.T) {
	// Provide providers in reverse order — MultiClient should sort by priority.
	var firstCalledProvider string
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstCalledProvider == "" {
			firstCalledProvider = "high-priority"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if firstCalledProvider == "" {
			firstCalledProvider = "low-priority"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv2.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "low", Client: llm.NewClient(srv2.URL, "k"), Priority: 10},
		{Name: "high", Client: llm.NewClient(srv1.URL, "k"), Priority: 1},
	}, llm.MultiClientConfig{}, testLogger())

	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if firstCalledProvider != "high-priority" {
		t.Errorf("expected high-priority provider first, got %q", firstCalledProvider)
	}
}

func TestMultiClientForcedFallbackWhenAllUnhealthy(t *testing.T) {
	// Use a provider that fails enough to be marked unhealthy, then recovers.
	var callCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 5 { // first 5 calls fail
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error")) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "recovered"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{Name: "only", Client: llm.NewClient(srv.URL, "k"), Priority: 1},
	}, llm.MultiClientConfig{
		MaxRetries:       0,
		MaxConsecErrors:  2,
		CooldownDuration: time.Hour, // long cooldown so forced fallback kicks in
		InitialBackoff:   time.Millisecond,
	}, testLogger())

	// First few calls fail and mark provider unhealthy.
	for range 3 {
		_, _ = mc.Chat(context.Background(), llm.ChatRequest{
			Model:    "model",
			Messages: []llm.Message{{Role: "user", Content: "hi"}},
		})
	}

	// Provider should be unhealthy now. Next call triggers forced fallback.
	// Calls 4,5 fail, call 6 succeeds.
	resp, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "model",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("expected forced fallback to succeed, got error: %v", err)
	}
	if resp.Choices[0].Message.Content != "recovered" {
		t.Errorf("expected 'recovered', got %q", resp.Choices[0].Message.Content)
	}
}

func TestMultiClientDirectAliasLookup(t *testing.T) {
	// When model name matches an alias directly (e.g. "default"), resolve it.
	var receivedModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req llm.ChatRequest
		json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
		receivedModel = req.Model
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(llm.ChatResponse{ //nolint:errcheck
			Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	mc := llm.NewMultiClient([]llm.ProviderClientConfig{
		{
			Name:     "p1",
			Client:   llm.NewClient(srv.URL, "k"),
			Models:   map[string]string{"default": "my-model-v1"},
			Priority: 1,
		},
	}, llm.MultiClientConfig{}, testLogger())

	_, err := mc.Chat(context.Background(), llm.ChatRequest{
		Model:    "default",
		Messages: []llm.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receivedModel != "my-model-v1" {
		t.Errorf("expected 'my-model-v1', got %q", receivedModel)
	}
}
