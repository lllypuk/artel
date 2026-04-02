package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lllypuk/agent-shatrov/internal/news"
)

// NewsTool manages news subscriptions and reading.
type NewsTool struct {
	manager *news.NewsManager
}

// NewNewsTool creates a new NewsTool.
func NewNewsTool(manager *news.NewsManager) *NewsTool {
	return &NewsTool{manager: manager}
}

type newsArgs struct {
	Action   string `json:"action"` // list, subscribe, unsubscribe, search, unread, saved, discover, addfeed, markread, save, unsave
	ChatID   int64  `json:"chat_id"`
	Query    string `json:"query"`
	FeedID   int64  `json:"feed_id"`
	URL      string `json:"url"`
	ItemID   int64  `json:"item_id"`
	Limit    int    `json:"limit"`
	Category string `json:"category"`
}

// Name returns the tool name.
func (n *NewsTool) Name() string {
	return "news"
}

// Description returns the tool description.
func (n *NewsTool) Description() string {
	return "Manage news subscriptions and read articles. Actions: list (show news), search (search by query), unread (unread news), saved (saved articles), subscribe (to feed), unsubscribe (from feed), discover (find feeds), addfeed (add custom RSS), markread (mark as read), save (save to favorites), unsave (remove from favorites)."
}

// Parameters returns the JSON Schema for parameters.
func (n *NewsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "search", "unread", "saved", "subscribe", "unsubscribe", "discover", "addfeed", "markread", "save", "unsave"},
				"description": "Action to perform",
			},
			"chat_id": map[string]any{
				"type":        "integer",
				"description": "User chat ID",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search/discover)",
			},
			"feed_id": map[string]any{
				"type":        "integer",
				"description": "Feed ID (for subscribe/unsubscribe)",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Feed URL (for addfeed)",
			},
			"item_id": map[string]any{
				"type":        "integer",
				"description": "Item ID (for markread/save/unsave)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of results (default: 10)",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Category filter (for discover)",
			},
		},
		"required": []string{"action", "chat_id"},
	}
}

// Execute runs the tool with given arguments.
func (n *NewsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a newsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	if a.Limit == 0 {
		a.Limit = 10
	}

	switch a.Action {
	case "list":
		return n.listNews(ctx, a.ChatID, a.Limit)

	case "search":
		return n.searchNews(ctx, a.ChatID, a.Query, a.Limit)

	case "unread":
		return n.unreadNews(ctx, a.ChatID, a.Limit)

	case "saved":
		return n.savedNews(ctx, a.ChatID, a.Limit)

	case "subscribe":
		return n.subscribe(ctx, a.ChatID, a.FeedID)

	case "unsubscribe":
		return n.unsubscribe(ctx, a.ChatID, a.FeedID)

	case "discover":
		return n.discoverFeeds(ctx, a.Category)

	case "addfeed":
		return n.addFeed(ctx, a.ChatID, a.URL)

	case "markread":
		return n.markRead(ctx, a.ChatID, a.ItemID)

	case "save":
		return n.saveItem(ctx, a.ChatID, a.ItemID)

	case "unsave":
		return n.unsaveItem(ctx, a.ChatID, a.ItemID)

	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (n *NewsTool) listNews(ctx context.Context, chatID int64, limit int) (string, error) {
	filter := news.NewsFilter{Limit: limit}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get news: %w", err)
	}
	return formatNewsItems(items, "Latest news"), nil
}

func (n *NewsTool) searchNews(ctx context.Context, chatID int64, query string, limit int) (string, error) {
	filter := news.NewsFilter{Limit: limit, SearchQuery: query}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("search news: %w", err)
	}
	return formatNewsItems(items, fmt.Sprintf("Search: %s", query)), nil
}

func (n *NewsTool) unreadNews(ctx context.Context, chatID int64, limit int) (string, error) {
	filter := news.NewsFilter{Limit: limit}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get unread: %w", err)
	}

	// Filter only unread
	var unread []news.ItemWithScore
	for _, item := range items {
		if !item.Read {
			unread = append(unread, item)
		}
	}

	return formatNewsItems(unread, "Unread news"), nil
}

func (n *NewsTool) savedNews(ctx context.Context, chatID int64, limit int) (string, error) {
	filter := news.NewsFilter{Limit: limit}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get saved: %w", err)
	}

	// Filter only saved
	var saved []news.ItemWithScore
	for _, item := range items {
		if item.Saved {
			saved = append(saved, item)
		}
	}

	return formatNewsItems(saved, "Saved articles"), nil
}

func (n *NewsTool) subscribe(ctx context.Context, chatID, feedID int64) (string, error) {
	if err := n.manager.Subscribe(ctx, chatID, feedID); err != nil {
		return "", fmt.Errorf("subscribe: %w", err)
	}
	return fmt.Sprintf("✓ Subscribed to feed %d", feedID), nil
}

func (n *NewsTool) unsubscribe(ctx context.Context, chatID, feedID int64) (string, error) {
	if err := n.manager.Unsubscribe(ctx, chatID, feedID); err != nil {
		return "", fmt.Errorf("unsubscribe: %w", err)
	}
	return fmt.Sprintf("✓ Unsubscribed from feed %d", feedID), nil
}

func (n *NewsTool) discoverFeeds(ctx context.Context, category string) (string, error) {
	feeds, err := n.manager.ListFeeds(ctx, 100, 0)
	if err != nil {
		return "", fmt.Errorf("list feeds: %w", err)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "📰 Available feeds (%d)\n\n", len(feeds))

	for i, feed := range feeds {
		if category != "" && feed.Category != category {
			continue
		}
		fmt.Fprintf(&buf, "%d. %s\n", i+1, feed.Title)
		fmt.Fprintf(&buf, "   URL: %s\n", feed.URL)
		if feed.Category != "" {
			fmt.Fprintf(&buf, "   📁 %s", feed.Category)
		}
		if feed.Enabled {
			buf.WriteString(" ✅")
		} else {
			buf.WriteString(" ❌")
		}
		buf.WriteByte('\n')
	}

	return buf.String(), nil
}

func (n *NewsTool) addFeed(ctx context.Context, chatID int64, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("URL is required")
	}

	feed, err := n.manager.AddFeed(ctx, url, "")
	if err != nil {
		return "", fmt.Errorf("add feed: %w", err)
	}

	// Auto-subscribe
	if err := n.manager.Subscribe(ctx, chatID, feed.ID); err != nil {
		return "", fmt.Errorf("subscribe: %w", err)
	}

	return fmt.Sprintf("✓ Feed added and subscribed: %s (ID: %d)", feed.Title, feed.ID), nil
}

func (n *NewsTool) markRead(ctx context.Context, chatID, itemID int64) (string, error) {
	if err := n.manager.MarkAsRead(ctx, chatID, itemID); err != nil {
		return "", fmt.Errorf("mark as read: %w", err)
	}
	return fmt.Sprintf("✓ Marked as read: item %d", itemID), nil
}

func (n *NewsTool) saveItem(ctx context.Context, chatID, itemID int64) (string, error) {
	if err := n.manager.SaveItem(ctx, chatID, itemID); err != nil {
		return "", fmt.Errorf("save item: %w", err)
	}
	return fmt.Sprintf("✓ Saved: item %d", itemID), nil
}

func (n *NewsTool) unsaveItem(ctx context.Context, chatID, itemID int64) (string, error) {
	if err := n.manager.UnsaveItem(ctx, chatID, itemID); err != nil {
		return "", fmt.Errorf("unsave item: %w", err)
	}
	return fmt.Sprintf("✓ Removed from favorites: item %d", itemID), nil
}

func formatNewsItems(items []news.ItemWithScore, title string) string {
	if len(items) == 0 {
		return fmt.Sprintf("%s\nNo items found.", title)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "%s (%d items)\n\n", title, len(items))

	for i, iw := range items {
		fmt.Fprintf(&buf, "%d. %s\n", i+1, iw.Item.Title)
		if !iw.Read {
			buf.WriteString("   🆕 ")
		}
		fmt.Fprintf(&buf, "   %s\n", iw.Item.Link)
		fmt.Fprintf(&buf, "   %s", iw.Item.PublishedAt.Format("2006-01-02 15:04"))
		if iw.Item.Category != "" {
			fmt.Fprintf(&buf, " | 📁 %s", iw.Item.Category)
		}
		buf.WriteString("\n\n")
	}

	return buf.String()
}
