package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebSearchTool searches the web using a configured provider.
type WebSearchTool struct {
	client     *http.Client
	maxResults int
	provider   string
	apiKey     string
}

// WebSearchConfig holds config for the web search tool.
type WebSearchConfig struct {
	Provider       string // "brave", "serper", "duckduckgo"
	APIKey         string
	MaxResults     int
	TimeoutSeconds int
}

// NewWebSearchTool creates a new web search tool.
func NewWebSearchTool(cfg WebSearchConfig) *WebSearchTool {
	if cfg.MaxResults == 0 {
		cfg.MaxResults = 5
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	provider := cfg.Provider
	if provider == "" {
		if cfg.APIKey != "" {
			provider = "brave"
		} else {
			provider = "duckduckgo"
		}
	}
	return &WebSearchTool{
		client:     &http.Client{Timeout: timeout},
		maxResults: cfg.MaxResults,
		provider:   provider,
		apiKey:     cfg.APIKey,
	}
}

type webSearchArgs struct {
	Query string `json:"query"`
}

func (w *WebSearchTool) Name() string { return "web_search" }
func (w *WebSearchTool) Description() string {
	return "Search the internet for information. Returns top results with titles, snippets and URLs. Use this to find current news, facts, documentation, or any web content."
}

func (w *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query to look up on the internet",
			},
		},
		"required": []string{"query"},
	}
}

func (w *WebSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a webSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}

	var (
		results []searchResult
		err     error
	)
	switch w.provider {
	case "brave":
		results, err = w.searchBrave(ctx, a.Query)
	case "serper":
		results, err = w.searchSerper(ctx, a.Query)
	default:
		results, err = w.searchDDG(ctx, a.Query)
	}
	if err != nil {
		return "", fmt.Errorf("search (%s): %w", w.provider, err)
	}
	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %q", a.Query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %q\n\n", a.Query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("   URL: %s\n", r.URL))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

// ─── Brave Search API ────────────────────────────────────────────────────────

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

func (w *WebSearchTool) searchBrave(ctx context.Context, query string) ([]searchResult, error) {
	u := "https://api.search.brave.com/res/v1/web/search?q=" +
		url.QueryEscape(query) +
		fmt.Sprintf("&count=%d", w.maxResults)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("X-Subscription-Token", w.apiKey)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("brave API returned %d: %s", resp.StatusCode, string(body))
	}

	var data braveSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode brave response: %w", err)
	}

	var results []searchResult
	for _, r := range data.Web.Results {
		results = append(results, searchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Description,
		})
	}
	return results, nil
}

// ─── Serper.dev API (Google results) ─────────────────────────────────────────

type serperSearchResponse struct {
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"organic"`
}

func (w *WebSearchTool) searchSerper(ctx context.Context, query string) ([]searchResult, error) {
	payload := fmt.Sprintf(`{"q":%s,"num":%d}`,
		jsonString(query), w.maxResults)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://google.serper.dev/search",
		strings.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-KEY", w.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("serper API returned %d: %s", resp.StatusCode, string(body))
	}

	var data serperSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode serper response: %w", err)
	}

	var results []searchResult
	for _, r := range data.Organic {
		results = append(results, searchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}
	return results, nil
}

// ─── DuckDuckGo HTML (fallback, may be blocked) ──────────────────────────────

func (w *WebSearchTool) searchDDG(ctx context.Context, query string) ([]searchResult, error) {
	searchURL := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:124.0) Gecko/20100101 Firefox/124.0")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	content := string(body)
	if strings.Contains(content, "anomaly-modal") {
		return nil, fmt.Errorf("DuckDuckGo requires CAPTCHA — set web.provider to 'brave' or 'serper' with an API key")
	}

	return parseDDGResults(content, w.maxResults), nil
}

// parseDDGResults parses DuckDuckGo HTML search results.
func parseDDGResults(htmlContent string, maxResults int) []searchResult {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return nil
	}

	var results []searchResult

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if len(results) >= maxResults {
			return
		}
		if n.Type == html.ElementNode && n.Data == "div" {
			classes := getAttr(n, "class")
			if strings.Contains(classes, "result") && strings.Contains(classes, "web-result") {
				if r := extractResult(n); r.Title != "" && r.URL != "" {
					results = append(results, r)
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(doc)

	return results
}

func extractResult(n *html.Node) searchResult {
	var r searchResult

	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.ElementNode {
			classes := getAttr(node, "class")
			if node.Data == "a" && strings.Contains(classes, "result__a") {
				r.Title = strings.TrimSpace(textContent(node))
				href := getAttr(node, "href")
				if href != "" {
					r.URL = cleanDDGURL(href)
				}
			}
			if strings.Contains(classes, "result__snippet") {
				r.Snippet = strings.TrimSpace(textContent(node))
			}
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(n)

	return r
}

// cleanDDGURL extracts the real destination URL from DuckDuckGo redirect.
func cleanDDGURL(href string) string {
	if strings.Contains(href, "duckduckgo.com/l/") || strings.HasPrefix(href, "/l/") {
		base := href
		if strings.HasPrefix(base, "//") {
			base = "https:" + base
		} else if strings.HasPrefix(base, "/") {
			base = "https://duckduckgo.com" + base
		}
		u, err := url.Parse(base)
		if err == nil {
			if uddg := u.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
	}
	return href
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}
	traverse(n)
	return sb.String()
}

// jsonString returns a JSON-encoded string value.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
