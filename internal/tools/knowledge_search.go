package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lllypuk/artel/internal/storage"
)

// KnowledgeSearchTool performs unified full-text search across wiki and reading list.
type KnowledgeSearchTool struct {
	db *storage.DB
}

// NewKnowledgeSearchTool creates a new knowledge search tool.
func NewKnowledgeSearchTool(db *storage.DB) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{db: db}
}

func (k *KnowledgeSearchTool) Name() string { return "knowledge_search" }

func (k *KnowledgeSearchTool) Description() string {
	return "Search across wiki pages and reading list simultaneously. " +
		"Returns matching results from both sources with labels. " +
		"Uses FTS5 full-text search for wiki when available, LIKE fallback otherwise."
}

func (k *KnowledgeSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (required).",
			},
		},
		"required": []string{"query"},
	}
}

type knowledgeSearchArgs struct {
	Query string `json:"query"`
}

func (k *KnowledgeSearchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a knowledgeSearchArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	chatID, _ := ctx.Value(ChatIDKey).(int64)

	wikiResults, wikiErr := k.db.SearchWikiPages(ctx, chatID, a.Query)
	articleResults, articleErr := k.db.SearchReadingItems(ctx, chatID, a.Query)

	if wikiErr != nil && articleErr != nil {
		return "", fmt.Errorf("search failed: wiki: %v, articles: %v", wikiErr, articleErr)
	}

	total := len(wikiResults) + len(articleResults)
	if total == 0 {
		return fmt.Sprintf("No results found for: %q", a.Query), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Search results for %q (%d total):\n\n", a.Query, total)

	writeWikiResults(&sb, wikiResults, wikiErr)
	writeArticleResults(&sb, articleResults, articleErr)

	return sb.String(), nil
}

func writeWikiResults(sb *strings.Builder, pages []*storage.WikiPage, err error) {
	if err != nil {
		fmt.Fprintf(sb, "[wiki] search error: %v\n\n", err)
		return
	}
	if len(pages) == 0 {
		return
	}
	fmt.Fprintf(sb, "Wiki pages (%d):\n", len(pages))
	for _, p := range pages {
		snippet := buildSnippet(p.Content, 120)
		fmt.Fprintf(sb, "  [wiki] %s — %s\n", p.Path, p.Title)
		if snippet != "" {
			fmt.Fprintf(sb, "         %s\n", snippet)
		}
	}
	sb.WriteByte('\n')
}

func writeArticleResults(sb *strings.Builder, items []*storage.ReadingItem, err error) {
	if err != nil {
		fmt.Fprintf(sb, "[article] search error: %v\n\n", err)
		return
	}
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "Articles (%d):\n", len(items))
	for _, item := range items {
		snippet := buildSnippet(item.Summary+item.Content, 120)
		fmt.Fprintf(sb, "  [article:%d] %s [%s]\n", item.ID, item.Title, item.Status)
		if item.URL != "" {
			fmt.Fprintf(sb, "               %s\n", item.URL)
		}
		if snippet != "" {
			fmt.Fprintf(sb, "               %s\n", snippet)
		}
	}
	sb.WriteByte('\n')
}

// buildSnippet returns a trimmed snippet from text, up to maxLen chars.
func buildSnippet(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	// Take first non-empty line.
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 5 {
			if len(line) > maxLen {
				return line[:maxLen] + "…"
			}
			return line
		}
	}
	return ""
}
