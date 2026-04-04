package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// ContextInfo holds statistics about the current conversation context.
type ContextInfo struct {
	ConversationID  int64
	TotalMessages   int  // total messages stored in DB
	ContextMessages int  // messages included in LLM context (after summary boundary)
	HasSummary      bool // whether a rolling summary exists
	SummaryLength   int  // character length of the summary
	EstimatedTokens int  // rough token estimate (~4 chars per token)
	MaxMessages     int  // configured limit (0 = no limit)
}

const summarizeSystemPrompt = `Ты суммаризируешь историю диалога AI-ассистента с пользователем.
Создай краткое, но информативное резюме разговора, сохранив:
- ключевые факты и решения
- важный контекст и предпочтения пользователя
- результаты выполненных задач и инструментов
- незавершённые вопросы или ожидающие действия

Пиши на русском языке. Будь лаконичен — не более 500 слов.`

// manageContext loads conversation history with sliding-window context management.
// It returns messages ready to be appended after the main system prompt — the
// main system prompt itself is NOT included.
//
// When the number of messages since the last summary boundary exceeds
// maxContextMessages, the oldest ones are summarised via a separate LLM call
// and the result is persisted in the DB for future requests.
func (a *Agent) manageContext(ctx context.Context, conv *storage.Conversation) ([]llm.Message, error) {
	// Load messages recorded after the previous summary boundary.
	msgs, err := a.db.GetMessagesSince(ctx, conv.ID, conv.SummarizedUntilID)
	if err != nil {
		return nil, fmt.Errorf("get messages since: %w", err)
	}

	// Summarise when the window is exceeded (summarisation must be enabled).
	if a.maxContextMessages > 0 && len(msgs) > a.maxContextMessages {
		toSummarize := msgs[:len(msgs)-a.maxContextMessages]
		recent := msgs[len(msgs)-a.maxContextMessages:]

		a.logger.Info("context window exceeded, summarizing",
			"total", len(msgs),
			"summarizing", len(toSummarize),
			"keeping", len(recent),
		)

		newSummary, sumErr := a.summarizeMessages(ctx, conv.SummaryText, toSummarize)
		if sumErr != nil {
			// Non-fatal: fall back to the truncated recent window without a summary.
			a.logger.Warn("summarization failed, keeping recent messages only", "error", sumErr)
			msgs = recent
		} else {
			newUntilID := toSummarize[len(toSummarize)-1].ID
			if dbErr := a.db.UpdateConversationSummary(ctx, conv.ID, newSummary, newUntilID); dbErr != nil {
				a.logger.Error("persist summary", "error", dbErr)
			}
			// Update the in-memory copy so the current request uses the new summary.
			conv.SummaryText = newSummary
			conv.SummarizedUntilID = newUntilID
			msgs = recent
		}
	}

	// Build the slice of llm.Message values.
	result := make([]llm.Message, 0, len(msgs)+1)

	// Inject the rolling summary as an additional system-level context block.
	if conv.SummaryText != "" {
		result = append(result, llm.Message{
			Role:    "system",
			Content: "Краткое резюме предыдущих сообщений диалога:\n" + conv.SummaryText,
		})
	}

	for i := range msgs {
		m := &msgs[i]
		msg := llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if m.ToolCallsJSON != "" {
			_ = json.Unmarshal([]byte(m.ToolCallsJSON), &msg.ToolCalls)
		}
		result = append(result, msg)
	}

	return result, nil
}

// summarizeMessages calls the LLM to produce a rolling summary.
// existingSummary is incorporated when a previous summary already exists.
func (a *Agent) summarizeMessages(ctx context.Context, existingSummary string, msgs []storage.StoredMessage) (string, error) {
	var sb strings.Builder

	if existingSummary != "" {
		sb.WriteString("=== ПРЕДЫДУЩЕЕ РЕЗЮМЕ ===\n")
		sb.WriteString(existingSummary)
		sb.WriteString("\n\n")
	}

	sb.WriteString("=== НОВЫЕ СООБЩЕНИЯ ===\n")
	for i := range msgs {
		m := &msgs[i]
		switch m.Role {
		case "user":
			sb.WriteString("Пользователь: ")
		case "assistant":
			sb.WriteString("Ассистент: ")
		case "tool":
			sb.WriteString("[Инструмент]: ")
		case "system", "summary":
			continue // skip meta-messages
		default:
			sb.WriteString(m.Role + ": ")
		}
		content := m.Content
		if len(content) > 600 {
			content = content[:600] + "…"
		}
		sb.WriteString(content)
		sb.WriteString("\n")
	}

	req := llm.ChatRequest{
		Model: a.getSummaryModel(),
		Messages: []llm.Message{
			{Role: "system", Content: summarizeSystemPrompt},
			{Role: "user", Content: sb.String()},
		},
		MaxTokens:   1024,
		Temperature: 0.3,
	}

	sumCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	resp, err := a.llmClient.Chat(sumCtx, req)
	if err != nil {
		return "", fmt.Errorf("LLM summarize call: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM summarizer")
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

// getSummaryModel returns the model used for summarisation.
// Prefers the "fast" alias to keep costs down.
func (a *Agent) getSummaryModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if fast, ok := a.models["fast"]; ok && fast != "" {
		return fast
	}
	return a.model
}

// GetContextInfo returns statistics about the current conversation context
// for the given chatID. Returns a zero-value ContextInfo when no conversation
// exists yet.
func (a *Agent) GetContextInfo(ctx context.Context, chatID int64) (*ContextInfo, error) {
	conv, err := a.db.GetActiveConversation(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil {
		return &ContextInfo{MaxMessages: a.maxContextMessages}, nil
	}

	totalMsgs, err := a.db.GetMessageCount(ctx, conv.ID)
	if err != nil {
		return nil, fmt.Errorf("get message count: %w", err)
	}

	recent, err := a.db.GetMessagesSince(ctx, conv.ID, conv.SummarizedUntilID)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}

	// Rough token estimate: ~4 chars per token for mixed Russian/English.
	totalChars := len(conv.SummaryText)
	for i := range recent {
		totalChars += len(recent[i].Content)
	}

	return &ContextInfo{
		ConversationID:  conv.ID,
		TotalMessages:   totalMsgs,
		ContextMessages: len(recent),
		HasSummary:      conv.SummaryText != "",
		SummaryLength:   len(conv.SummaryText),
		EstimatedTokens: totalChars / 4,
		MaxMessages:     a.maxContextMessages,
	}, nil
}
