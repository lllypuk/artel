package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// ReadingListTool manages the reading list and saved articles.
type ReadingListTool struct {
	db    *storage.DB
	fetch *WebFetchTool
}

// NewReadingListTool creates a new reading list tool.
func NewReadingListTool(db *storage.DB, fetch *WebFetchTool) *ReadingListTool {
	return &ReadingListTool{db: db, fetch: fetch}
}

func (r *ReadingListTool) Name() string { return "reading_list" }

func (r *ReadingListTool) Description() string {
	return "Manage reading list and saved articles. " +
		"Actions: " +
		"save (fetch URL content and save to reading list with optional tags/priority), " +
		"add (add a bookmark without fetching — just URL and title), " +
		"list (list items, optionally filtered by status/priority/tags), " +
		"get (get full content of a saved article by id), " +
		"update (update status/title/priority/tags of an item by id), " +
		"delete (delete an item by id), " +
		"digest (summary of unread items grouped by tags). " +
		"Statuses: to-read, reading, done. Priorities: high, medium, low."
}

func (r *ReadingListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"save", "add", "list", "get", "update", "delete", "digest"},
				"description": "Action to perform",
			},
			"id": map[string]any{
				"type":        "integer",
				"description": "Item ID (for get, update, delete).",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "URL to save or add (for save, add).",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Title (for add; optional override for save).",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"to-read", "reading", "done"},
				"description": "Reading status (for list filter or update).",
			},
			"priority": map[string]any{
				"type":        "string",
				"enum":        []string{"high", "medium", "low"},
				"description": "Priority (for save, add, list filter, update).",
			},
			"tags": map[string]any{
				"type":        "string",
				"description": "Comma-separated tags (for save, add, list filter, update).",
			},
		},
		"required": []string{"action"},
	}
}

type readingListArgs struct {
	Action   string `json:"action"`
	ID       int64  `json:"id"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority string `json:"priority"`
	Tags     string `json:"tags"`
}

func (r *ReadingListTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a readingListArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	chatID, _ := ctx.Value(ChatIDKey).(int64)

	switch a.Action {
	case "save":
		return r.execSave(ctx, chatID, a)
	case "add":
		return r.execAdd(ctx, chatID, a)
	case "list":
		return r.execList(ctx, chatID, a)
	case "get":
		return r.execGet(ctx, chatID, a)
	case "update":
		return r.execUpdate(ctx, chatID, a)
	case "delete":
		return r.execDeleteItem(ctx, chatID, a)
	case "digest":
		return r.execDigest(ctx, chatID)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (r *ReadingListTool) execSave(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	if a.URL == "" {
		return "", fmt.Errorf("url is required for save")
	}

	fetchArgs, err := json.Marshal(map[string]any{"url": a.URL})
	if err != nil {
		return "", fmt.Errorf("marshal fetch args: %w", err)
	}

	content, fetchErr := r.fetch.Execute(ctx, fetchArgs)
	if fetchErr != nil {
		content = ""
	} else {
		// Strip "Content from <url>:\n\n" wrapper added by WebFetchTool.
		content = stripFetchPrefix(content)
	}

	title := a.Title
	if title == "" {
		title = extractTitleFromContent(content, a.URL)
	}

	// Store a trimmed version to avoid huge DB entries (keep first 50KB at a rune boundary).
	const maxContentBytes = 50 * 1024
	storedContent := truncateAtRuneBoundary(content, maxContentBytes)

	id, err := r.db.CreateReadingItem(ctx, chatID, a.URL, title, "", storedContent, "to-read", a.Priority, a.Tags)
	if err != nil {
		return "", fmt.Errorf("save reading item: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Saved to reading list (id: %d)\n", id)
	fmt.Fprintf(&sb, "Title: %s\n", title)
	fmt.Fprintf(&sb, "URL: %s\n", a.URL)
	if fetchErr != nil {
		fmt.Fprintf(&sb, "Note: could not fetch content (%v)\n", fetchErr)
	} else {
		fmt.Fprintf(&sb, "Content saved: %d chars\n", len(storedContent))
	}
	return sb.String(), nil
}

// stripFetchPrefix removes the "Content from <url>:\n\n" header added by WebFetchTool.
func stripFetchPrefix(content string) string {
	const marker = "Content from "
	if !strings.HasPrefix(content, marker) {
		return content
	}
	if _, after, ok := strings.Cut(content, "\n\n"); ok {
		return after
	}
	if _, after, ok := strings.Cut(content, "\n"); ok {
		return after
	}
	return content
}

// truncateAtRuneBoundary truncates s to at most maxBytes bytes, ensuring no
// multi-byte UTF-8 rune is split.
func truncateAtRuneBoundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes until we find the start of a rune.
	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func extractTitleFromContent(content, fallbackURL string) string {
	if content == "" {
		return fallbackURL
	}
	// Look for the first non-empty line as a rough title.
	lines := strings.SplitN(content, "\n", 10)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if len(line) > 3 && len(line) <= 200 {
			return line
		}
	}
	return fallbackURL
}

func (r *ReadingListTool) execAdd(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	if a.Title == "" && a.URL == "" {
		return "", fmt.Errorf("title or url is required for add")
	}
	title := a.Title
	if title == "" {
		title = a.URL
	}
	id, err := r.db.CreateReadingItem(ctx, chatID, a.URL, title, "", "", "to-read", a.Priority, a.Tags)
	if err != nil {
		return "", fmt.Errorf("add reading item: %w", err)
	}
	return fmt.Sprintf("Added to reading list (id: %d): %s", id, title), nil
}

func (r *ReadingListTool) execList(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	items, err := r.db.ListReadingItems(ctx, chatID, a.Status, a.Priority, a.Tags)
	if err != nil {
		return "", fmt.Errorf("list reading items: %w", err)
	}
	if len(items) == 0 {
		return "Reading list is empty.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Reading list (%d items):\n", len(items))
	for _, item := range items {
		fmt.Fprintf(&sb, "  [%d] %s", item.ID, item.Title)
		fmt.Fprintf(&sb, " [%s/%s]", item.Status, item.Priority)
		if item.Tags != "" {
			fmt.Fprintf(&sb, " (%s)", item.Tags)
		}
		if item.URL != "" {
			fmt.Fprintf(&sb, "\n       %s", item.URL)
		}
		fmt.Fprintf(&sb, "\n       saved: %s\n", item.SavedAt.Format("2006-01-02"))
	}
	return sb.String(), nil
}

func (r *ReadingListTool) execGet(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for get")
	}
	item, err := r.db.GetReadingItem(ctx, chatID, a.ID)
	if err != nil {
		return "", fmt.Errorf("get reading item: %w", err)
	}
	if item == nil {
		return fmt.Sprintf("Item %d not found.", a.ID), nil
	}
	return formatReadingItem(item), nil
}

func formatReadingItem(item *storage.ReadingItem) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", item.Title)
	fmt.Fprintf(&sb, "ID: %d | Status: %s | Priority: %s\n", item.ID, item.Status, item.Priority)
	if item.URL != "" {
		fmt.Fprintf(&sb, "URL: %s\n", item.URL)
	}
	if item.Tags != "" {
		fmt.Fprintf(&sb, "Tags: %s\n", item.Tags)
	}
	fmt.Fprintf(&sb, "Saved: %s\n", item.SavedAt.Format("2006-01-02 15:04"))
	if item.ReadAt != nil {
		fmt.Fprintf(&sb, "Read: %s\n", item.ReadAt.Format("2006-01-02 15:04"))
	}
	if item.Summary != "" {
		fmt.Fprintf(&sb, "\nSummary:\n%s\n", item.Summary)
	}
	if item.Content != "" {
		fmt.Fprintf(&sb, "\nContent:\n%s\n", item.Content)
	}
	return sb.String()
}

func (r *ReadingListTool) execUpdate(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for update")
	}
	markRead := a.Status == "done"
	if err := r.db.UpdateReadingItem(ctx, chatID, a.ID, a.Title, a.Status, a.Priority, a.Tags, markRead); err != nil {
		return "", fmt.Errorf("update reading item: %w", err)
	}
	return fmt.Sprintf("Reading item %d updated.", a.ID), nil
}

func (r *ReadingListTool) execDeleteItem(ctx context.Context, chatID int64, a readingListArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for delete")
	}
	if err := r.db.DeleteReadingItem(ctx, chatID, a.ID); err != nil {
		return "", fmt.Errorf("delete reading item: %w", err)
	}
	return fmt.Sprintf("Reading item %d deleted.", a.ID), nil
}

func (r *ReadingListTool) execDigest(ctx context.Context, chatID int64) (string, error) {
	counts, err := r.db.ReadingListDigest(ctx, chatID)
	if err != nil {
		return "", fmt.Errorf("reading list digest: %w", err)
	}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 {
		return "Nothing to read — reading list is empty or all done.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Unread items: %d total\n", total)
	// Sort tags for stable, readable output.
	keys := make([]string, 0, len(counts))
	for tag := range counts {
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	for _, tag := range keys {
		fmt.Fprintf(&sb, "  %s: %d\n", tag, counts[tag])
	}
	return sb.String(), nil
}
