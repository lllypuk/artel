package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/safeguards"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/transport"
)

const platformMattermost = "mattermost"

// mattermostNotifier returns a Notifier that converts an internal integer chat
// ID back into a Mattermost channel ID and posts a message.
func mattermostNotifier(tr transport.Transport, db *storage.DB, logger *slog.Logger) func(chatID int64, message string) {
	return func(chatID int64, message string) {
		if message == "" {
			return
		}
		platform, channelID, err := db.ResolveChatID(context.Background(), chatID)
		if err != nil {
			logger.Warn("mattermost notifier: resolve chat id failed", "chat_id", chatID, "error", err)
			return
		}
		if platform != platformMattermost {
			logger.Warn("mattermost notifier: chat id platform mismatch", "chat_id", chatID, "platform", platform)
			return
		}
		if err := tr.Send(context.Background(), transport.Message{TargetID: channelID, Text: message}); err != nil {
			logger.Warn("mattermost notifier: send failed", "chat_id", chatID, "error", err)
		}
	}
}

// runMattermostDispatcher consumes inbound Mattermost messages, applies
// authorization and safeguards, runs the agent, and sends the reply back.
func runMattermostDispatcher(ctx context.Context, tr transport.Transport, ag *agent.Agent, gate *transport.AuthGate, guard *safeguards.Guard, db *storage.DB, logger *slog.Logger) {
	msgs := tr.Messages()
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			dispatchMattermostMessage(ctx, tr, ag, gate, guard, db, logger, m)
		}
	}
}

func dispatchMattermostMessage(ctx context.Context, tr transport.Transport, ag *agent.Agent, gate *transport.AuthGate, guard *safeguards.Guard, db *storage.DB, logger *slog.Logger, m transport.InboundMessage) {
	now := time.Now()

	allowed, reason := gate.Allowed(m)
	if !allowed {
		logger.Debug("auth gate rejected message", "reason", reason, "sender_id", m.SenderID, "sender_name", m.SenderName, "chat_id", m.ChatID)
		return
	}

	decision := guard.BeforeHandle(m, now)
	if !decision.Allowed {
		logger.Warn("safeguard blocked inbound message", "reason", decision.Reason, "chat_id", m.ChatID, "thread_id", m.ThreadID)
		if decision.Escalate {
			err := tr.Send(ctx, transport.Message{
				TargetID: m.ChatID,
				Text:     safeguards.EscalationText,
				ThreadID: m.ThreadID,
			})
			if err != nil {
				logger.Warn("failed to send escalation message", "chat_id", m.ChatID, "thread_id", m.ThreadID, "error", err)
			}
			guard.RecordResponse(m, time.Now())
		}
		return
	}
	if decision.Warn {
		logger.Warn("loop warning: approaching bot-message limit", "chat_id", m.ChatID, "thread_id", m.ThreadID)
	}

	chatID, err := db.GetOrCreateChatID(ctx, platformMattermost, m.ChatID)
	if err != nil {
		logger.Error("chat id mapping failed", "error", err, "chat_id", m.ChatID)
		return
	}

	// Rate-limit applies to replies, not to inbound handling. Check before
	// invoking the agent so a throttled thread receives no model calls.
	replyDecision := guard.AllowReply(m, now)
	if !replyDecision.Allowed {
		logger.Warn("safeguard rate-limited reply", "reason", replyDecision.Reason, "chat_id", m.ChatID, "thread_id", m.ThreadID)
		return
	}

	resp, err := ag.HandleMessage(ctx, chatID, m.Text)
	if err != nil {
		logger.Error("agent error", "error", err, "chat_id", m.ChatID, "thread_id", m.ThreadID)
		_ = tr.Send(ctx, transport.Message{
			TargetID: m.ChatID,
			Text:     fmt.Sprintf("❌ Ошибка: %s", err.Error()),
			ThreadID: m.ThreadID,
		})
		guard.RecordResponse(m, time.Now())
		return
	}

	if resp.Text == "" {
		return
	}
	if err := tr.Send(ctx, transport.Message{
		TargetID: m.ChatID,
		Text:     resp.Text,
		ThreadID: m.ThreadID,
	}); err != nil {
		logger.Warn("failed to send reply", "chat_id", m.ChatID, "thread_id", m.ThreadID, "error", err)
		return
	}
	guard.RecordResponse(m, time.Now())
}
