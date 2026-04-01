package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/lllypuk/agent-shatrov/internal/agent"
)

// Bot is the Telegram bot interface.
type Bot struct {
	api          *tgbotapi.BotAPI
	agent        *agent.Agent
	allowedUsers map[int64]bool
	logger       *slog.Logger
}

// New creates a new Telegram bot.
func New(token string, allowedUsers []int64, ag *agent.Agent, logger *slog.Logger) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
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
		allowedUsers: allowed,
		logger:       logger,
	}, nil
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
	b.api.Send(typing)

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
		b.sendText(chatID, "👋 Привет! Я твой персональный AI-агент.\n\nКоманды:\n/new — новый диалог\n/help — помощь")
	case "/new":
		if err := b.agent.NewConversation(chatID); err != nil {
			b.sendText(chatID, fmt.Sprintf("❌ Ошибка: %s", err.Error()))
			return
		}
		b.sendText(chatID, "🆕 Новый диалог начат.")
	case "/help":
		b.sendText(chatID, "🤖 *Agent Shatrov*\n\n"+
			"Просто пиши мне сообщения — я отвечу.\n\n"+
			"Команды:\n"+
			"/new — начать новый диалог\n"+
			"/help — эта справка\n")
	default:
		b.sendText(chatID, "Неизвестная команда. /help для справки.")
	}
}

func (b *Bot) sendText(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := b.api.Send(msg); err != nil {
		// Retry without markdown if parsing fails
		msg.ParseMode = ""
		b.api.Send(msg)
	}
}

func (b *Bot) sendLongText(chatID int64, text string) {
	const maxLen = 4000
	if len(text) <= maxLen {
		b.sendText(chatID, text)
		return
	}

	// Split by paragraphs
	for len(text) > 0 {
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

func truncateLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
