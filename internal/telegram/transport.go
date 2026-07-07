package telegram

import (
	"context"
	"fmt"

	"github.com/lllypuk/agent-shatrov/internal/transport"
)

// Transport wraps Bot to implement the common transport.Transport interface.
// It keeps all Telegram-specific behavior inside the telegram package.
type TelegramTransport struct {
	bot *Bot
}

// NewTransport creates a transport.Transport backed by the Telegram bot.
func NewTransport(bot *Bot) *TelegramTransport {
	return &TelegramTransport{bot: bot}
}

// Run starts the underlying bot update loop.
func (t *TelegramTransport) Run(ctx context.Context) error {
	return t.bot.Run(ctx)
}

// Send delivers a text message to a Telegram chat.
func (t *TelegramTransport) Send(ctx context.Context, m transport.Message) error {
	if m.TargetID == "" {
		return fmt.Errorf("target chat id is required")
	}
	chatID, err := parseChatID(m.TargetID)
	if err != nil {
		return err
	}
	t.bot.sendLongText(chatID, m.Text)
	return nil
}

// Messages returns inbound messages adapted to the transport format.
func (t *TelegramTransport) Messages() <-chan transport.InboundMessage {
	out := make(chan transport.InboundMessage)
	go t.forwardUpdates(out)
	return out
}

// Close stops the underlying bot update loop.
func (t *TelegramTransport) Close() error {
	return nil
}

func (t *TelegramTransport) forwardUpdates(out chan<- transport.InboundMessage) {
	in := t.bot.Messages()
	for msg := range in {
		out <- toInboundMessage(msg)
	}
}

func parseChatID(s string) (int64, error) {
	var chatID int64
	if _, err := fmt.Sscanf(s, "%d", &chatID); err != nil {
		return 0, fmt.Errorf("invalid chat id %q: %w", s, err)
	}
	return chatID, nil
}

func toInboundMessage(m Message) transport.InboundMessage {
	return transport.InboundMessage{
		ChatID:     m.ChatID,
		SenderID:   m.SenderID,
		SenderName: m.SenderName,
		IsBot:      m.IsBot,
		Text:       m.Text,
		Mentions:   m.Mentions,
		IsDM:       m.IsDM,
		ReplyTo:    m.ReplyTo,
	}
}
