package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	Action string `json:"action"` // list, search, unread, saved
	ChatID int64  `json:"chat_id"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

// Name returns the tool name.
func (n *NewsTool) Name() string {
	return "news"
}

// Description returns the tool description.
func (n *NewsTool) Description() string {
	return "Read and search news articles. Actions: list (show latest news), search (search by query), unread (unread news), saved (saved articles)."
}

// Parameters returns the JSON Schema for parameters.
func (n *NewsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "search", "unread", "saved"},
				"description": "Action to perform",
			},
			"chat_id": map[string]any{
				"type":        "integer",
				"description": "User chat ID (optional, taken from context automatically)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of results (default: 10, max: 50)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Pagination offset (default: 0)",
			},
		},
		"required": []string{"action"},
	}
}

// Execute runs the tool with given arguments.
func (n *NewsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a newsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	// Use chatID from context if not provided in args
	if a.ChatID == 0 {
		if chatID, ok := ctx.Value(ChatIDKey).(int64); ok {
			a.ChatID = chatID
		}
	}

	if a.Limit == 0 {
		a.Limit = 10
	}
	if a.Limit > 50 {
		a.Limit = 50
	}

	switch a.Action {
	case "list":
		return n.ListNews(ctx, a.ChatID, a.Limit, a.Offset)
	case "search":
		return n.SearchNews(ctx, a.ChatID, a.Query, a.Limit, a.Offset)
	case "unread":
		return n.UnreadNews(ctx, a.ChatID, a.Limit, a.Offset)
	case "saved":
		return n.SavedNews(ctx, a.ChatID, a.Limit, a.Offset)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

// ListNews returns the latest news for a user.
func (n *NewsTool) ListNews(ctx context.Context, chatID int64, limit, offset int) (string, error) {
	filter := news.NewsFilter{Limit: limit, Offset: offset}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get news: %w", err)
	}
	return formatNewsItems(items, "📰 Последние новости", limit, offset), nil
}

// SearchNews searches news by query.
func (n *NewsTool) SearchNews(ctx context.Context, chatID int64, query string, limit, offset int) (string, error) {
	filter := news.NewsFilter{Limit: limit, Offset: offset, SearchQuery: query}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("search news: %w", err)
	}
	return formatNewsItems(items, fmt.Sprintf("🔍 Поиск: %s", query), limit, offset), nil
}

// UnreadNews returns unread news for a user.
func (n *NewsTool) UnreadNews(ctx context.Context, chatID int64, limit, offset int) (string, error) {
	filter := news.NewsFilter{Limit: limit, Offset: offset, UnreadOnly: true}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get unread: %w", err)
	}
	return formatNewsItems(items, "🆕 Непрочитанные новости", limit, offset), nil
}

// SavedNews returns saved articles for a user.
func (n *NewsTool) SavedNews(ctx context.Context, chatID int64, limit, offset int) (string, error) {
	filter := news.NewsFilter{Limit: limit, Offset: offset, SavedOnly: true}
	items, err := n.manager.GetNews(ctx, chatID, filter)
	if err != nil {
		return "", fmt.Errorf("get saved: %w", err)
	}
	return formatNewsItems(items, "⭐ Сохранённые статьи", limit, offset), nil
}

// formatNewsItems formats a list of news items for display.
func formatNewsItems(items []news.ItemWithScore, title string, limit, offset int) string {
	var buf strings.Builder

	if len(items) == 0 {
		fmt.Fprintf(&buf, "%s\n\nНовостей нет.", title)
		if offset > 0 {
			fmt.Fprintf(&buf, " (страница %d)", offset/limit+1)
		}
		return buf.String()
	}

	fmt.Fprintf(&buf, "%s (%d)\n", title, len(items))
	if offset > 0 {
		fmt.Fprintf(&buf, "Страница %d\n", offset/limit+1)
	}
	buf.WriteString(strings.Repeat("─", 30))
	buf.WriteString("\n")

	for _, iw := range items {
		// Status badges
		var badges []string
		if !iw.Read {
			badges = append(badges, "🆕")
		}
		if iw.Saved {
			badges = append(badges, "⭐")
		}
		if iw.Item.Category != "" {
			badges = append(badges, categoryIcon(iw.Item.Category))
		}

		badgeStr := ""
		if len(badges) > 0 {
			badgeStr = strings.Join(badges, "") + " "
		}

		fmt.Fprintf(&buf, "%s*%s*\n", badgeStr, escapeMarkdown(iw.Item.Title))
		fmt.Fprintf(&buf, "🔗 %s\n", iw.Item.Link)

		// Age display
		age := time.Since(iw.Item.PublishedAt)
		ageStr := formatAge(age)
		fmt.Fprintf(&buf, "🕐 %s", ageStr)

		if iw.Item.Category != "" {
			fmt.Fprintf(&buf, " | 📁 %s", iw.Item.Category)
		}
		fmt.Fprintf(&buf, " | #%d\n", iw.Item.ID)

		buf.WriteString(strings.Repeat("─", 30))
		buf.WriteString("\n")
	}

	// Pagination hint
	if len(items) == limit {
		nextOffset := offset + limit
		fmt.Fprintf(&buf, "\n➡️ Следующая страница: /news_more %d", nextOffset)
	}

	return buf.String()
}

// formatAge returns a human-readable age string.
func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%d мин назад", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%d ч назад", h)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%d дн назад", days)
	default:
		return ""
	}
}

// categoryIcon returns an emoji for a category.
func categoryIcon(category string) string {
	switch strings.ToLower(category) {
	case "tech", "technology":
		return "💻"
	case "golang", "go":
		return "🐹"
	case "opensource", "open source":
		return "🔓"
	case "ai", "ml":
		return "🤖"
	case "science":
		return "🔬"
	case "news":
		return "📰"
	default:
		return "📌"
	}
}

// escapeMarkdown escapes special Markdown characters.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"*", "\\*",
		"_", "\\_",
		"`", "\\`",
		"[", "\\[",
	)
	return replacer.Replace(s)
}
