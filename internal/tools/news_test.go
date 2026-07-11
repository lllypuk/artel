package tools_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/news"
	"github.com/lllypuk/artel/internal/storage"
	"github.com/lllypuk/artel/internal/tools"
)

// ── static HTTP fetcher for news tool tests ───────────────────────────────────

type staticFetcher struct {
	data []byte
	err  error
}

func (f *staticFetcher) Fetch(_ context.Context, _, _ string) ([]byte, error) {
	return f.data, f.err
}

// validRSSFeed is a minimal RSS feed accepted by the gofeed parser.
const validRSSFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Test Feed</title>
    <link>https://testfeed.example.com</link>
    <description>A test feed</description>
  </channel>
</rss>`

// ── helpers ───────────────────────────────────────────────────────────────────

func newTestNewsTool(t *testing.T, fetcher news.Fetcher) (*tools.NewsTool, storage.NewsRepository) {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck

	repo := storage.NewNewsRepository(db)
	parser := news.NewGofeedParser()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	mgr := news.NewNewsManager(repo, parser, fetcher, "", logger)
	return tools.NewNewsTool(mgr), repo
}

func newsExec(t *testing.T, tool *tools.NewsTool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return tool.Execute(context.Background(), raw)
}

func newsExecWithChatID(t *testing.T, tool *tools.NewsTool, chatID int64, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, chatID)
	return tool.Execute(ctx, raw)
}

func createNewsTestFeed(t *testing.T, repo storage.NewsRepository, url, title string) int64 {
	t.Helper()
	id, err := repo.CreateFeed(context.Background(), &news.Feed{
		URL:           url,
		Title:         title,
		Type:          news.FeedTypeRSS,
		Enabled:       true,
		FetchInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	return id
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestNewsTool_Name(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{data: []byte(validRSSFeed)})
	if tool.Name() != "news" {
		t.Errorf("expected 'news', got %q", tool.Name())
	}
}

func TestNewsTool_Description(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{data: []byte(validRSSFeed)})
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestNewsTool_Parameters(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{data: []byte(validRSSFeed)})
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	for _, required := range []string{"action", "chat_id", "query", "limit"} {
		if _, ok := props[required]; !ok {
			t.Errorf("expected property %q in parameters", required)
		}
	}
	// Check that subscription-related properties are removed
	for _, removed := range []string{"feed_id", "url", "item_id", "category"} {
		if _, ok := props[removed]; ok {
			t.Errorf("property %q should not exist in parameters", removed)
		}
	}
}

func TestNewsTool_Parameters_Required(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	params := tool.Parameters()
	req, ok := params["required"].([]string)
	if !ok {
		t.Fatal("expected required to be []string")
	}
	if len(req) == 0 || req[0] != "action" {
		t.Errorf("expected 'action' to be required, got %v", req)
	}
}

// ── Error cases ───────────────────────────────────────────────────────────────

func TestNewsTool_InvalidJSON(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	_, err := tool.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNewsTool_UnknownAction(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	_, err := newsExec(t, tool, map[string]any{"action": "fly", "chat_id": 1})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action' in error, got: %v", err)
	}
}

// ── list ──────────────────────────────────────────────────────────────────────

func TestNewsTool_List_EmptyNoFeeds(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Новостей нет") {
		t.Errorf("expected empty message, got %q", result)
	}
}

func TestNewsTool_List_WithItems(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})

	feedID := createNewsTestFeed(t, repo, "https://list.example.com/", "My Feed")
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID:      feedID,
		GUID:        "list-g1",
		Title:       "Interesting Article",
		Link:        "https://example.com/interesting",
		PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 200})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Interesting Article") {
		t.Errorf("expected article title in result, got %q", result)
	}
}

func TestNewsTool_List_DisabledFeed_Excluded(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})

	// Create a disabled feed directly (enabled: false)
	feedID, err := repo.CreateFeed(context.Background(), &news.Feed{
		URL:           "https://disabled.example.com/",
		Title:         "Disabled Feed",
		Type:          news.FeedTypeRSS,
		Enabled:       false,
		FetchInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID:      feedID,
		GUID:        "disabled-g1",
		Title:       "Should Not Appear",
		Link:        "https://example.com/disabled",
		PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 201})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "Should Not Appear") {
		t.Errorf("did not expect article from disabled feed in result, got %q", result)
	}
}

// ── list with ChatID from context ─────────────────────────────────────────────

func TestNewsTool_List_ChatIDFromContext(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://ctx.example.com/", "Context Feed")
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "ctx-g1", Title: "Context Article",
		Link: "https://x.com/1", PublishedAt: time.Now(),
	})

	// No chat_id in args, but set via context.
	result, err := newsExecWithChatID(t, tool, 300, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Context Article") {
		t.Errorf("expected article in result, got %q", result)
	}
}

// ── search ────────────────────────────────────────────────────────────────────

func TestNewsTool_Search(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://search.example.com/", "Search Feed")
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "srch-g1", Title: "Golang Tips",
		Link: "https://x.com/srch1", Description: "tips for golang devs", PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{
		"action": "search", "query": "Golang", "chat_id": 400,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Result should mention the search query in the header.
	if !strings.Contains(result, "Golang") {
		t.Errorf("expected query in result header, got %q", result)
	}
}

func TestNewsTool_Search_EmptyResult(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	result, err := newsExec(t, tool, map[string]any{
		"action": "search", "query": "noresults", "chat_id": 500,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Новостей нет") {
		t.Errorf("expected empty message, got %q", result)
	}
}

// ── unread / saved ────────────────────────────────────────────────────────────

func TestNewsTool_Unread_Empty(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	result, err := newsExec(t, tool, map[string]any{"action": "unread", "chat_id": 600})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Новостей нет") {
		t.Errorf("expected empty message for unread, got %q", result)
	}
}

func TestNewsTool_Saved_Empty(t *testing.T) {
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	result, err := newsExec(t, tool, map[string]any{"action": "saved", "chat_id": 700})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Новостей нет") {
		t.Errorf("expected empty message for saved, got %q", result)
	}
}

// ── limit clamping ────────────────────────────────────────────────────────────

func TestNewsTool_Limit_Default(t *testing.T) {
	// When limit is 0 in args, should default to 10 without error.
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	_, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 1900})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewsTool_Limit_Clamped(t *testing.T) {
	// Limit > 50 should be silently clamped to 50.
	tool, _ := newTestNewsTool(t, &staticFetcher{})
	_, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2000, "limit": 9999})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── output format helpers (tested indirectly via Execute) ─────────────────────

func TestNewsTool_FormatAge_Recent(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://age.example.com/", "Age Feed")
	// Item published ~30 seconds ago → "только что".
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "age-g1", Title: "Fresh Article",
		Link: "https://x.com/age1", PublishedAt: time.Now().Add(-30 * time.Second),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "только что") {
		t.Errorf("expected 'только что' for very recent item, got %q", result)
	}
}

func TestNewsTool_FormatAge_Hours(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://age2.example.com/", "Age Feed 2")
	// Item published 3 hours ago.
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "age2-g1", Title: "3h Article",
		Link: "https://x.com/age2", PublishedAt: time.Now().Add(-3 * time.Hour),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2200})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "ч назад") {
		t.Errorf("expected 'ч назад' for hours-old item, got %q", result)
	}
}

func TestNewsTool_FormatAge_Days(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://age3.example.com/", "Age Feed 3")
	// Item published 3 days ago.
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "age3-g1", Title: "3d Article",
		Link: "https://x.com/age3", PublishedAt: time.Now().Add(-3 * 24 * time.Hour),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2300})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "дн назад") {
		t.Errorf("expected 'дн назад' for days-old item, got %q", result)
	}
}

func TestNewsTool_EscapeMarkdown_InTitle(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://esc.example.com/", "Esc Feed")
	// Title has asterisks and underscores that should be escaped.
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "esc-g1", Title: "Article *bold* _italic_",
		Link: "https://x.com/esc1", PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2400})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, `\*bold\*`) {
		t.Errorf("expected escaped asterisks in result, got %q", result)
	}
}

func TestNewsTool_CategoryIcon_Tech(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://icon.example.com/", "Icon Feed")
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "icon-g1", Title: "Tech Article",
		Category: "tech", Link: "https://x.com/icon1", PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2500})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "💻") {
		t.Errorf("expected tech icon 💻 for tech category, got %q", result)
	}
}

func TestNewsTool_UnreadBadge(t *testing.T) {
	tool, repo := newTestNewsTool(t, &staticFetcher{})
	feedID := createNewsTestFeed(t, repo, "https://badge.example.com/", "Badge Feed")
	repo.CreateItem(context.Background(), &news.Item{ //nolint:errcheck
		FeedID: feedID, GUID: "badge-g1", Title: "Unread Article",
		Link: "https://x.com/badge1", PublishedAt: time.Now(),
	})

	result, err := newsExec(t, tool, map[string]any{"action": "list", "chat_id": 2600})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unread items should have to 🆕 badge.
	if !strings.Contains(result, "🆕") {
		t.Errorf("expected 🆕 badge for unread item, got %q", result)
	}
}
