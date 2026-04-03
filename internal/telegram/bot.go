package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
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
	browserTool  *tools.BrowserTool
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

// SetBrowserTool sets the browser tool for the bot (used for /screenshot command).
func (b *Bot) SetBrowserTool(tool *tools.BrowserTool) {
	b.browserTool = tool
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

// GetNotifier returns a Notifier function backed by this bot's sendText.
func (b *Bot) GetNotifier() tools.Notifier {
	return func(chatID int64, message string) {
		if message != "" {
			b.sendText(chatID, message)
		}
	}
}

// SetNewsTool updates the news tool reference after bot creation.
func (b *Bot) SetNewsTool(nt *tools.NewsTool) {
	b.newsTool = nt
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

const helpText = "\U0001F916 **Agent Shatrov**\n\n" +
	"Просто напиши что нужно — агент сам выберет инструмент.\n\n" +
	"**Диалог:**\n" +
	"/new — начать новый диалог (сброс контекста)\n" +
	"/model — показать / сменить модель LLM\n" +
	"/context — размер и статистика текущего контекста\n" +
	"/help — эта справка\n\n" +
	"**Напоминания:**\n" +
	"/reminders — список активных напоминаний\n" +
	"Через агента: 'напомни через 2 часа сделать X', 'напомни каждый день в 9:00'\n\n" +
	"**Скриншоты:**\n" +
	"/screenshot <URL> — сделать скриншот страницы\n" +
	"/open <URL> — открыть страницу и сделать скриншот\n\n" +
	"**Новости:**\n" +
	"/news [запрос] — последние новости (или поиск по запросу)\n" +
	"/news_more [N] — следующие N новостей (по умолчанию 10)\n" +
	"/unread — непрочитанные новости\n" +
	"/saved — сохранённые новости\n\n" +
	"**Что умеет агент (через чат):**\n" +
	"\U0001F4DD Заметки — создать, найти, прочитать, редактировать, удалить\n" +
	"⏰ Напоминания — создать, список, отменить (разовые и повторяющиеся)\n" +
	"\U0001F50D Поиск в интернете (Brave / Serper / DuckDuckGo)\n" +
	"\U0001F310 Чтение веб-страниц\n" +
	"\U0001F6E0 Изменение собственного кода через pi_coder\n" +
	"\U0001F4C3 Новости — чтение, поиск, избранное\n" +
	"\U0001F4F8 Скриншоты веб-страниц"

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
	case "/context":
		b.handleContextCommand(ctx, chatID)
	case "/reminders":
		b.handleRemindersCommand(ctx, chatID)
	case "/help":
		b.sendText(chatID, helpText)

	// --- Screenshot commands ---
	case "/screenshot", "/open":
		b.handleScreenshotCommand(ctx, chatID, cmd, parts)

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
		fmt.Fprintf(&lines, "🤖 Текущая модель: `%s`", current)
		if len(models) > 0 {
			lines.WriteString("\n\nДоступные алиасы:")
			for alias, name := range models {
				marker := ""
				if name == current {
					marker = " ✔️"
				}
				fmt.Fprintf(&lines, "\n• `%s` → %s%s", alias, name, marker)
			}
			lines.WriteString("\n\nИспользование: `/model fast` или `/model default`")
		}
		b.sendText(chatID, lines.String())
		return
	}

	alias := parts[1]
	modelName, ok := models[alias]
	if !ok {
		// Maybe the user passed a raw model name — allow it directly
		modelName = alias
	}
	b.agent.SetModel(modelName)
	b.sendText(chatID, fmt.Sprintf("✅ Модель переключена: `%s`", modelName))

	b.logger.Info("model switched", "chat_id", chatID, "model", modelName)
}

func (b *Bot) handleContextCommand(ctx context.Context, chatID int64) {
	info, err := b.agent.GetContextInfo(ctx, chatID)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}

	var sb strings.Builder
	sb.WriteString("📊 **Контекст диалога**\n\n")

	if info.ConversationID == 0 {
		sb.WriteString("Диалог ещё не начат.")
		b.sendText(chatID, sb.String())
		return
	}

	fmt.Fprintf(&sb, "Всего сообщений в БД: **%d**\n", info.TotalMessages)
	fmt.Fprintf(&sb, "В LLM-контексте: **%d**\n", info.ContextMessages)

	if info.MaxMessages > 0 {
		fmt.Fprintf(&sb, "Лимит окна: **%d** сообщений\n", info.MaxMessages)
	} else {
		sb.WriteString("Лимит окна: ∞ (без ограничений)\n")
	}

	if info.HasSummary {
		fmt.Fprintf(&sb, "Суммаризация: ✅ (%d симв.)\n", info.SummaryLength)
	} else {
		sb.WriteString("Суммаризация: ❌ (ещё не требовалась)\n")
	}

	fmt.Fprintf(&sb, "Оценка токенов: ~%d\n", info.EstimatedTokens)

	b.sendText(chatID, sb.String())
}

func (b *Bot) handleRemindersCommand(ctx context.Context, chatID int64) {
	// Proxy to the reminders tool directly for a quick list.
	tool, ok := b.agent.ToolRegistry().Get("reminders")
	if !ok {
		b.sendText(chatID, "❌ Инструмент reminders недоступен.")
		return
	}
	args, _ := json.Marshal(map[string]any{"action": "list"})
	ctxWithID := context.WithValue(ctx, tools.ChatIDKey, chatID)
	result, err := tool.Execute(ctxWithID, args)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
		return
	}
	b.sendText(chatID, result)
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
	// Remove any HTML tags from LLM responses before converting to Telegram format
	cleanText := SanitizeResponse(text)
	b.sendHTML(chatID, markdownToHTML(cleanText))
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

func (b *Bot) handleScreenshotCommand(ctx context.Context, chatID int64, cmd string, parts []string) {
	if b.browserTool == nil {
		b.sendText(chatID, "❌ Браузер отключён в конфигурации.")
		return
	}

	if len(parts) < 2 {
		b.sendText(chatID, "❌ Укажите URL. Например: /screenshot https://example.com")
		return
	}

	targetURL := parts[1]

	// Show typing action while processing
	typing := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	_, _ = b.api.Send(typing)

	toolCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Get browser tool from agent registry
	tool, ok := b.agent.ToolRegistry().Get("browser")
	if !ok {
		b.sendText(chatID, "❌ Инструмент browser недоступен.")
		return
	}

	// Step 1: open the page
	openArgs, _ := json.Marshal(map[string]any{"command": "open", "url": targetURL})
	ctxWithChatID := context.WithValue(toolCtx, tools.ChatIDKey, chatID)
	if _, err := tool.Execute(ctxWithChatID, openArgs); err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка открытия страницы: %s", err.Error()))
		return
	}

	// Step 2: wait for page to settle
	waitArgs, _ := json.Marshal(map[string]any{"command": "wait", "wait_ms": 2000})
	_, _ = tool.Execute(ctxWithChatID, waitArgs)

	// Step 3: take screenshot
	screenshotArgs, _ := json.Marshal(map[string]any{"command": "screenshot", "full_page": false})
	result, err := tool.Execute(ctxWithChatID, screenshotArgs)
	if err != nil {
		b.sendText(chatID, fmt.Sprintf("❌ Ошибка создания скриншота: %s", err.Error()))
		return
	}

	// Extract screenshot path from result
	// Result format: "screenshot_path: /path/to/screenshot-xxx.png\n..."
	var screenshotPath string
	for line := range strings.SplitSeq(result, "\n") {
		if after, ok0 := strings.CutPrefix(line, "screenshot_path: "); ok0 {
			screenshotPath = after
			break
		}
	}

	if screenshotPath == "" {
		b.sendText(chatID, fmt.Sprintf("❌ Не удалось определить путь к скриншоту.\n\n%s", result))
		return
	}

	if _, err := os.Stat(screenshotPath); os.IsNotExist(err) {
		b.sendText(chatID, fmt.Sprintf("❌ Файл скриншота не найден: %s", screenshotPath))
		return
	}

	b.sendPhoto(chatID, screenshotPath, targetURL)
}

func (b *Bot) sendPhoto(chatID int64, filePath, caption string) {
	photo := tgbotapi.NewPhoto(chatID, tgbotapi.FilePath(filePath))
	if caption != "" {
		photo.Caption = fmt.Sprintf("📸 %s", caption)
	}

	if _, err := b.api.Send(photo); err != nil {
		b.logger.Error("send photo", "error", err, "file", filePath)
		b.sendText(chatID, fmt.Sprintf("❌ Не удалось отправить скриншот: %s\n\nФайл: %s", err.Error(), filePath))
	}
}
