package tools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func newTestWebFetchTool() *tools.WebFetchTool {
	return tools.NewWebFetchTool(tools.WebFetchConfig{
		TimeoutSeconds: 5,
		MaxSizeKB:      100,
	})
}

func fetchExec(t *testing.T, tool *tools.WebFetchTool, rawURL string) (string, error) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"url": rawURL})
	return tool.Execute(context.Background(), args)
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestWebFetchName(t *testing.T) {
	if newTestWebFetchTool().Name() != "web_fetch" {
		t.Errorf("expected 'web_fetch'")
	}
}

func TestWebFetchDescription(t *testing.T) {
	if newTestWebFetchTool().Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestWebFetchParameters(t *testing.T) {
	params := newTestWebFetchTool().Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
}

// ── Happy path ────────────────────────────────────────────────────────────────

func TestWebFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><body><p>Hello, world!</p></body></html>`)) //nolint:errcheck
	}))
	defer srv.Close()

	result, err := fetchExec(t, newTestWebFetchTool(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello, world!") {
		t.Errorf("expected 'Hello, world!' in result, got %q", result)
	}
	if !strings.Contains(result, srv.URL) {
		t.Errorf("expected URL in result preamble, got %q", result)
	}
}

func TestWebFetchHTMLStripping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>
<head>
  <script>alert('xss')</script>
  <style>.x { color: red }</style>
</head>
<body>
  <nav>Top Navigation</nav>
  <header>Page Header</header>
  <article><p>Real content here</p></article>
  <footer>Page Footer</footer>
</body>
</html>`)) //nolint:errcheck
	}))
	defer srv.Close()

	result, err := fetchExec(t, newTestWebFetchTool(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "alert") {
		t.Errorf("script content should be stripped, got: %q", result)
	}
	if strings.Contains(result, "color: red") {
		t.Errorf("style content should be stripped, got: %q", result)
	}
	if strings.Contains(result, "Top Navigation") {
		t.Errorf("nav content should be stripped, got: %q", result)
	}
	if strings.Contains(result, "Page Footer") {
		t.Errorf("footer content should be stripped, got: %q", result)
	}
	if !strings.Contains(result, "Real content here") {
		t.Errorf("expected article content to be present, got: %q", result)
	}
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestWebFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := fetchExec(t, newTestWebFetchTool(), srv.URL)
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected '404' in error, got: %v", err)
	}
}

func TestWebFetchEmptyURL(t *testing.T) {
	_, err := fetchExec(t, newTestWebFetchTool(), "")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestWebFetchInvalidJSON(t *testing.T) {
	_, err := newTestWebFetchTool().Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWebFetchDefaultConfig(t *testing.T) {
	tool := tools.NewWebFetchTool(tools.WebFetchConfig{})
	if tool == nil {
		t.Fatal("expected non-nil tool with zero config")
	}
}

// ── Size limiting ─────────────────────────────────────────────────────────────

func TestWebFetchTruncatesLargeOutput(t *testing.T) {
	// Serve a page large enough to exceed the 8 000-char output cap.
	bigText := strings.Repeat("word ", 2000) // ~10 000 chars
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><p>" + bigText + "</p></body></html>")) //nolint:errcheck
	}))
	defer srv.Close()

	result, err := fetchExec(t, newTestWebFetchTool(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation notice in result, got %q", result[:200])
	}
}
