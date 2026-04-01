// Package tools — internal white-box tests for web_search.go.
// These tests live in package tools (not tools_test) so they can access
// unexported struct fields and helper functions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ── HTTP mock helpers ─────────────────────────────────────────────────────────

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func mockSearchClient(statusCode int, body string) *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: statusCode,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestWebSearchToolName(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{Provider: "duckduckgo"})
	if s.Name() != "web_search" {
		t.Errorf("expected 'web_search', got %q", s.Name())
	}
}

func TestWebSearchToolDescription(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{})
	if s.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestWebSearchToolParameters(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{})
	params := s.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
}

// ── Constructor defaults ──────────────────────────────────────────────────────

func TestWebSearchDefaultMaxResults(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{})
	if s.maxResults != 5 {
		t.Errorf("expected default maxResults=5, got %d", s.maxResults)
	}
}

func TestWebSearchDefaultProviderNOKey(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{})
	if s.provider != "duckduckgo" {
		t.Errorf("expected 'duckduckgo' when no API key, got %q", s.provider)
	}
}

func TestWebSearchDefaultProviderWithKey(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{APIKey: "mykey"})
	if s.provider != "brave" {
		t.Errorf("expected 'brave' when API key present, got %q", s.provider)
	}
}

func TestWebSearchExplicitProvider(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{Provider: "serper", APIKey: "k"})
	if s.provider != "serper" {
		t.Errorf("expected 'serper', got %q", s.provider)
	}
}

// ── Input validation ──────────────────────────────────────────────────────────

func TestWebSearchEmptyQuery(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{Provider: "duckduckgo"})
	args, _ := json.Marshal(map[string]string{"query": ""})
	_, err := s.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestWebSearchInvalidJSON(t *testing.T) {
	s := NewWebSearchTool(WebSearchConfig{Provider: "duckduckgo"})
	_, err := s.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON args")
	}
}

// ── Brave provider ────────────────────────────────────────────────────────────

func TestWebSearchBraveSuccess(t *testing.T) {
	const braveResp = `{
		"web": {
			"results": [
				{"title":"Brave Result 1","url":"https://example.com/1","description":"Snippet 1"},
				{"title":"Brave Result 2","url":"https://example.com/2","description":"Snippet 2"}
			]
		}
	}`
	s := &WebSearchTool{
		client:     mockSearchClient(200, braveResp),
		maxResults: 5,
		provider:   "brave",
		apiKey:     "test-key",
	}

	args, _ := json.Marshal(map[string]string{"query": "test"})
	result, err := s.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Brave Result 1") {
		t.Errorf("expected 'Brave Result 1' in result, got %q", result)
	}
	if !strings.Contains(result, "example.com/1") {
		t.Errorf("expected URL in result, got %q", result)
	}
	if !strings.Contains(result, "Snippet 1") {
		t.Errorf("expected snippet in result, got %q", result)
	}
}

func TestWebSearchBraveAPIError(t *testing.T) {
	s := &WebSearchTool{
		client:   mockSearchClient(401, `{"error":"unauthorized"}`),
		provider: "brave",
		apiKey:   "bad",
	}
	args, _ := json.Marshal(map[string]string{"query": "test"})
	_, err := s.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for 401 API response")
	}
	if !strings.Contains(err.Error(), "brave") {
		t.Errorf("expected provider name in error, got: %v", err)
	}
}

func TestWebSearchBraveNoResults(t *testing.T) {
	s := &WebSearchTool{
		client:     mockSearchClient(200, `{"web":{"results":[]}}`),
		maxResults: 5,
		provider:   "brave",
		apiKey:     "k",
	}
	args, _ := json.Marshal(map[string]string{"query": "very obscure"})
	result, err := s.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "No results") {
		t.Errorf("expected 'No results' in result, got %q", result)
	}
}

// ── Serper provider ───────────────────────────────────────────────────────────

func TestWebSearchSerperSuccess(t *testing.T) {
	const serperResp = `{
		"organic": [
			{"title":"Serper Result","link":"https://serper.example.com","snippet":"Serper snippet"}
		]
	}`
	s := &WebSearchTool{
		client:     mockSearchClient(200, serperResp),
		maxResults: 5,
		provider:   "serper",
		apiKey:     "test-key",
	}

	args, _ := json.Marshal(map[string]string{"query": "test"})
	result, err := s.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Serper Result") {
		t.Errorf("expected 'Serper Result' in result, got %q", result)
	}
	if !strings.Contains(result, "serper.example.com") {
		t.Errorf("expected URL in result, got %q", result)
	}
}

func TestWebSearchSerperAPIError(t *testing.T) {
	s := &WebSearchTool{
		client:   mockSearchClient(403, "forbidden"),
		provider: "serper",
		apiKey:   "bad",
	}
	args, _ := json.Marshal(map[string]string{"query": "test"})
	_, err := s.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("expected error for 403 API response")
	}
}

// ── parseDDGResults ───────────────────────────────────────────────────────────

func TestParseDDGResultsEmpty(t *testing.T) {
	results := parseDDGResults("<html><body></body></html>", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestParseDDGResultsInvalidHTML(t *testing.T) {
	// Should not panic on broken HTML.
	results := parseDDGResults("<<<not html at all>>>", 5)
	_ = results
}

func TestParseDDGResultsRespectsLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := range 10 {
		fmt.Fprintf(&sb,
			`<div class="result results_links web-result">
				<h2><a class="result__a" href="https://example%d.com">Title %d</a></h2>
				<div class="result__snippet">Snippet %d</div>
			</div>`, i, i, i)
	}
	sb.WriteString("</body></html>")

	results := parseDDGResults(sb.String(), 3)
	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

// ── Output formatting ─────────────────────────────────────────────────────────

func TestWebSearchOutputContainsQuery(t *testing.T) {
	s := &WebSearchTool{
		client:     mockSearchClient(200, `{"web":{"results":[{"title":"T","url":"https://x.com","description":"D"}]}}`),
		maxResults: 5,
		provider:   "brave",
		apiKey:     "k",
	}
	args, _ := json.Marshal(map[string]string{"query": "my search query"})
	result, err := s.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "my search query") {
		t.Errorf("expected query in result header, got %q", result)
	}
}
