package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	newsOffsets  map[int64]int // chatID -> last offset for news pagination
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
		newsOffsets:  make(map[int64]int),
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

const helpText = "\U0001F916 <b>Agent Shatrov</b>\n\n" +
	"Просто напиши что нужно — агент сам выберет инструмент.\n\n" +
	"<b>Диалог:</b>\n" +
	"/new — начать новый диалог (сброс контекста)\n" +
	"/model — показать / сменить модель LLM\n" +
	"/help — эта справка\n\n" +
	"<b>Новости:</b>\n" +
	"/news [запрос] — последние новости (или поиск по запросу)\n" +
	"/news_more [N] — следующие N новостей (по умолчанию 10)\n" +
	"/unread — непрочитанные новости\n" +
	"/saved — сохранённые новости\n\n" +
	"<b>Что умеет агент (через чат):</b>\n" +
	"\U0001F4DD Заметки — создать, найти, прочитать, редактировать, удалить\n" +
	"\U0001F50D Поиск в интернете (Brave / Serper / DuckDuckGo)\n" +
	"\U0001F310 Чтение веб-страниц\n" +
	"\U0001F6E0 Изменение собственного кода через pi_coder\n" +
	"\U0001F4C3 Новости — чтение, поиск, избранное"

func (b *Bot) handleCommand(ctx context.Context, chatID int64, text string) {
	parts := strings.Fields(text)
	cmd := parts[0]

	switch cmd {
	case "/start":
		b.sendText(chatID, "👋 Привет! Я твой персональный AI-агент.\n\n"+
			"Просто напиши что нужно — найду, запомню, выполню.\n\n"+
			"/help — полная справка\n"+
			"/new — начать новый диалог\n"+
			"/news — последние новости")
	case "/new":
		if err := b.agent.NewConversation(ctx, chatID); err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
			return
		}
		b.sendText(chatID, "🆕 Новый диалог начат.")
	case "/model":
		b.handleModelCommand(chatID, parts)
	case "/help":
		b.sendHTML(chatID, helpText)

	// --- News commands ---
	case "/news", "/unread", "/saved":
		b.handleNewsCommand(ctx, chatID, cmd, parts)
	case "/news_more":
		b.handleNewsMoreCommand(ctx, chatID, parts)

	default:
		b.sendText(chatID, "Неизвестная команда. /help для справки.")
	}
}

func (b *Bot) handleModelCommand(chatID int64, parts []string) {
	current := b.agent.GetModel()
	models := b.agent.Models()

	if len(parts) == 1 {
		// Show current model and available aliases
		var lines strings.Builder
		fmt.Fprintf(&lines, "🤖 Текущая модель: <code>%s</code>", escapeHTML(current))
		if len(models) > 0 {
			lines.WriteString("\n\nДоступные алиасы:")
			for alias, name := range models {
				marker := ""
				if name == current {
					marker = " ✔️"
				}
				fmt.Fprintf(&lines, "\n• <code>%s</code> → %s%s", escapeHTML(alias), escapeHTML(name), marker)
			}
			lines.WriteString("\n\nИспользование: <code>/model fast</code> или <code>/model default</code>")
		}
		b.sendHTML(chatID, lines.String())
		return
	}

	alias := parts[1]
	modelName, ok := models[alias]
	if !ok {
		// Maybe the user passed a raw model name — allow it directly
		modelName = alias
	}
	b.agent.SetModel(modelName)
	b.sendHTML(chatID, fmt.Sprintf("✅ Модель переключена: <code>%s</code>", escapeHTML(modelName)))

	b.logger.Info("model switched", "chat_id", chatID, "model", modelName)
}

func (b *Bot) handleNewsCommand(ctx context.Context, chatID int64, cmd string, parts []string) {
	if b.newsTool == nil {
		b.sendText(chatID, "❌ Новости отключены в конфигурации.")
		return
	}

	// Reset offset when starting a new news command
	b.newsOffsets[chatID] = 0

	toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var result string
	var err error

	switch cmd {
	case "/news":
		if len(parts) > 1 {
			query := strings.Join(parts[1:], " ")
			result, err = b.newsTool.SearchNews(toolCtx, chatID, query, 10, 0)
		} else {
			result, err = b.newsTool.ListNews(toolCtx, chatID, 10, 0)
		}
	case "/unread":
		result, err = b.newsTool.UnreadNews(toolCtx, chatID, 10, 0)
	case "/saved":
		result, err = b.newsTool.SavedNews(toolCtx, chatID, 10, 0)
	}

	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}
	b.sendLongText(chatID, result)
}

func (b *Bot) handleNewsMoreCommand(ctx context.Context, chatID int64, parts []string) {
	if b.newsTool == nil {
		b.sendText(chatID, "❌ Новости отключены в конфигурации.")
		return
	}

	// Get limit from argument or default to 10
	limit := 10
	if len(parts) > 1 {
		if l, err := fmt.Sscanf(parts[1], "%d", &limit); l == 1 && err == nil {
			if limit < 1 {
				limit = 10
			}
			if limit > 50 {
				limit = 50
			}
		}
	}

	// Get current offset (default to 0)
	offset := b.newsOffsets[chatID]

	toolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := b.newsTool.ListNews(toolCtx, chatID, limit, offset)

	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}

	// Update offset for next page
	b.newsOffsets[chatID] = offset + limit

	b.sendLongText(chatID, result)
}

func (b *Bot) sendText(chatID int64, text string) {
	b.sendHTML(chatID, markdownToHTML(text))
}

func (b *Bot) sendHTML(chatID int64, html string) {
	msg := tgbotapi.NewMessage(chatID, html)
	msg.ParseMode = "HTML"
	if _, err := b.api.Send(msg); err != nil {
		// Retry as plain text if HTML parsing fails
		msg.Text = html
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
