package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// Bot is the Telegram bot interface.
type Bot struct {
	api          *tgbotapi.BotAPI
	agent        *agent.Agent
	newsTool     *tools.NewsTool
	allowedUsers map[int64]bool
	logger       *slog.Logger
}

// newHTTPClient создаёт http.Client с прокси (если задан) или без него.
func newHTTPClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{}, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url %q: %w", proxyURL, err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(parsed),
		},
	}, nil
}

// New creates a new Telegram bot.
func New(token, proxyURL string, allowedUsers []int64, ag *agent.Agent, newsTool *tools.NewsTool, logger *slog.Logger) (*Bot, error) {
	httpClient, err := newHTTPClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create http client: %w", err)
	}
	api, err := tgbotapi.NewBotAPIWithClient(token, tgbotapi.APIEndpoint, httpClient)
	if err != nil {
		return nil, fmt.Errorf("create bot: %w", err)
	}

	allowed := make(map[int64]bool)
	for _, uid := range allowedUsers {
		allowed[uid] = true
	}

	logger.Info("Telegram bot authorized", "username", api.Self.UserName)

	return &Bot{
		api:          api,
		agent:        ag,
		newsTool:     newsTool,
		allowedUsers: allowed,
		logger:       logger,
	}, nil
}

// SetNotificationHandler sets up the agent to send notifications through this bot.
// This should be called after Bot creation.
func (b *Bot) SetNotificationHandler() {
	b.agent.SetNotifier(func(chatID int64, message string) {
		if message != "" {
			b.sendText(chatID, message)
		}
	})
}

// Run starts the long-polling loop.
func (b *Bot) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	b.logger.Info("Bot started, waiting for messages...")

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Bot stopping...")
			b.api.StopReceivingUpdates()
			return ctx.Err()
		case update := <-updates:
			if update.Message == nil {
				continue
			}
			go b.handleUpdate(ctx, update)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	msg := update.Message
	chatID := msg.Chat.ID

	// Auth check
	if !b.allowedUsers[chatID] {
		b.logger.Warn("unauthorized access attempt", "chat_id", chatID, "username", msg.From.UserName)
		b.sendText(chatID, "⛔ Доступ запрещён.")
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	b.logger.Info("message received", "chat_id", chatID, "text", truncateLog(text, 100))

	// Handle commands
	if strings.HasPrefix(text, "/") {
		b.handleCommand(ctx, chatID, text)
		return
	}

	// Send "typing" action
	typing := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	_, _ = b.api.Send(typing)

	// Process through agent
	resp, err := b.agent.HandleMessage(ctx, chatID, text)
	if err != nil {
		b.logger.Error("agent error", "error", err)
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}

	// Send response (split if too long for Telegram)
	b.sendLongText(chatID, resp.Text)

	if resp.Usage != nil {
		b.logger.Info("usage",
			"prompt_tokens", resp.Usage.PromptTokens,
			"completion_tokens", resp.Usage.CompletionTokens,
			"total_tokens", resp.Usage.TotalTokens,
		)
	}
}

func (b *Bot) handleCommand(ctx context.Context, chatID int64, text string) {
	parts := strings.Fields(text)
	cmd := parts[0]

	switch cmd {
	case "/start":
		b.sendText(chatID, "👋 Привет! Я твой персональный AI-агент.\n\n"+
			"/help — справка\n"+
			"/news — последние новости\n"+
			"/new — новый диалог")
	case "/new":
		if err := b.agent.NewConversation(ctx, chatID); err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
			return
		}
		b.sendText(chatID, "🆕 Новый диалог начат.")
	case "/help":
		b.sendText(chatID, "🤖 *Agent Shatrov*\n\n"+
			"Просто пиши мне сообщения — я отвечу.\n\n"+
			"*Основные команды:*\n"+
			"/new — начать новый диалог\n"+
			"/help — эта справка\n\n"+
			"*Новости:*\n"+
			"/news [запрос] — новости (или поиск)\n"+
			"/unread — непрочитанные\n"+
			"/saved — сохранённые\n"+
			"/feeds — список фидов\n"+
			"/discover [категория] — найти фиды\n"+
			"/subscribe <ID> — подписаться\n"+
			"/unsubscribe <ID> — отписаться\n")

	// --- News commands ---
	case "/news", "/unread", "/saved", "/feeds", "/discover", "/subscribe", "/unsubscribe":
		b.handleNewsCommand(ctx, chatID, cmd, parts)

	default:
		b.sendText(chatID, "Неизвестная команда. /help для справки.")
	}
}

func (b *Bot) handleNewsCommand(ctx context.Context, chatID int64, cmd string, parts []string) {
	if b.newsTool == nil {
		b.sendText(chatID, "❌ Новости отключены в конфигурации.")
		return
	}

	var result string
	var err error

	switch cmd {
	case "/news":
		if len(parts) > 1 {
			query := strings.Join(parts[1:], " ")
			result, err = b.newsTool.SearchNews(b.ctxWithTimeout(), chatID, query, 10, 0)
		} else {
			result, err = b.newsTool.ListNews(b.ctxWithTimeout(), chatID, 10, 0)
		}
	case "/unread":
		result, err = b.newsTool.UnreadNews(b.ctxWithTimeout(), chatID, 10, 0)
	case "/saved":
		result, err = b.newsTool.SavedNews(b.ctxWithTimeout(), chatID, 10, 0)
	case "/feeds", "/discover":
		category := ""
		if len(parts) > 1 {
			category = parts[1]
		}
		result, err = b.newsTool.DiscoverFeeds(b.ctxWithTimeout(), category)
	case "/subscribe":
		if len(parts) < 2 {
			b.sendText(chatID, "Использование: /subscribe <feed_id>")
			return
		}
		feedID, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil {
			b.sendText(chatID, "❌ Неверный ID фида.")
			return
		}
		result, err = b.newsTool.Subscribe(b.ctxWithTimeout(), chatID, feedID)
	case "/unsubscribe":
		if len(parts) < 2 {
			b.sendText(chatID, "Использование: /unsubscribe <feed_id>")
			return
		}
		feedID, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil {
			b.sendText(chatID, "❌ Неверный ID фида.")
			return
		}
		result, err = b.newsTool.Unsubscribe(b.ctxWithTimeout(), chatID, feedID)
	}

	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}
	b.sendLongText(chatID, result)
}

// ctxWithTimeout returns a background context with a 30s timeout for direct tool calls.
func (b *Bot) ctxWithTimeout() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 30*time.Second) //nolint:govet
	return ctx
}

func (b *Bot) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		// Retry without markdown if parsing fails
		msg.ParseMode = ""
		_, _ = b.api.Send(msg)
	}
}

func (b *Bot) sendLongText(chatID int64, text string) {
	const maxLen = 4000
	if len(text) <= maxLen {
		b.sendText(chatID, text)
		return
	}

	// Split by paragraphs
	for text != "" {
		end := maxLen
		if end > len(text) {
			end = len(text)
		} else {
			// Try to split at a newline
			if idx := strings.LastIndex(text[:end], "\n"); idx > 0 {
				end = idx + 1
			}
		}
		b.sendText(chatID, text[:end])
		text = text[end:]
	}
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
