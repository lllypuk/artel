package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// WebFetchTool fetches and returns readable text content of a URL.
type WebFetchTool struct {
	client  *http.Client
	maxSize int
}

// WebFetchConfig holds config for the web fetch tool.
type WebFetchConfig struct {
	TimeoutSeconds int
	MaxSizeKB      int
}

// NewWebFetchTool creates a new web fetch tool.
func NewWebFetchTool(cfg WebFetchConfig) *WebFetchTool {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	maxSize := cfg.MaxSizeKB * 1024
	if maxSize == 0 {
		maxSize = 100 * 1024 // 100 KB default
	}
	return &WebFetchTool{
		client:  &http.Client{Timeout: timeout},
		maxSize: maxSize,
	}
}

type webFetchArgs struct {
	URL string `json:"url"`
}

func (w *WebFetchTool) Name() string { return "web_fetch" }
func (w *WebFetchTool) Description() string {
	return "Fetch and read the text content of a specific web page by URL. Use after web_search to read full articles, documentation or any specific page for deeper analysis."
}

func (w *WebFetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The full URL of the web page to fetch and read",
			},
		},
		"required": []string{"url"},
	}
}

func (w *WebFetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a webFetchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.URL == "" {
		return "", fmt.Errorf("url is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:124.0) Gecko/20100101 Firefox/124.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch URL: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(w.maxSize)))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	text := htmlToText(string(body))

	const maxOutputLen = 8000
	if len(text) > maxOutputLen {
		text = text[:maxOutputLen] + "\n... (truncated, use a more specific query or narrower URL)"
	}

	return fmt.Sprintf("Content from %s:\n\n%s", a.URL, text), nil
}

// htmlToText converts HTML to readable plain text.
func htmlToText(content string) string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return content
	}

	var sb strings.Builder

	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "noscript", "nav", "footer",
				"aside", "header", "iframe", "svg", "form", "button":
				return // skip non-content elements
			case "p", "div", "section", "article", "main",
				"h1", "h2", "h3", "h4", "h5", "h6",
				"li", "tr", "br", "blockquote", "pre":
				sb.WriteString("\n")
			}
		}
		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text != "" {
				sb.WriteString(text)
				sb.WriteString(" ")
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteString("\n")
			}
		}
	}
	traverse(doc)

	// Collapse excessive blank lines
	result := sb.String()
	for strings.Contains(result, "\n\n\n") {
		result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(result)
}
