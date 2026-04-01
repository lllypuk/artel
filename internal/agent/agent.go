package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

const systemPrompt = `Ты — персональный AI-ассистент Саши. Твоё имя — Agent.
Ты работаешь на локальном компьютере и можешь выполнять задачи через доступные инструменты.

Правила:
- Отвечай кратко и по делу
- Используй русский язык по умолчанию, если пользователь не пишет на другом
- Если нужно выполнить действие — используй доступные инструменты
- Для заметок используй tool "notes"
- Будь полезным и проактивным

Доступные возможности:
- Создание и поиск заметок
- Выполнение безопасных shell-команд (если включено)
- Общий диалог и помощь
`

// Agent is the core agent implementing the ReAct loop.
type Agent struct {
	llmClient *llm.Client
	model     string
	tools     *tools.Registry
	db        *storage.DB
	logger    *slog.Logger
}

// New creates a new Agent.
func New(llmClient *llm.Client, model string, toolRegistry *tools.Registry, db *storage.DB, logger *slog.Logger) *Agent {
	return &Agent{
		llmClient: llmClient,
		model:     model,
		tools:     toolRegistry,
		db:        db,
		logger:    logger,
	}
}

// Response from the agent.
type Response struct {
	Text  string
	Usage *llm.Usage
}

// HandleMessage processes a user message and returns the agent's response.
// It implements the ReAct loop: LLM → tool calls → LLM → ... → final text.
func (a *Agent) HandleMessage(ctx context.Context, chatID int64, userMessage string) (*Response, error) {
	// Get or create conversation
	conv, err := a.db.GetActiveConversation(chatID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil {
		convID, err := a.db.CreateConversation(chatID, a.model)
		if err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
		conv = &storage.Conversation{ID: convID, ChatID: chatID, Model: a.model}
	}

	// Load conversation history
	storedMsgs, err := a.db.GetMessages(conv.ID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	// Build messages for LLM
	messages := []llm.Message{{Role: "system", Content: systemPrompt}}
	for _, m := range storedMsgs {
		msg := llm.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		if m.ToolCallsJSON != "" {
			_ = json.Unmarshal([]byte(m.ToolCallsJSON), &msg.ToolCalls)
		}
		messages = append(messages, msg)
	}

	// Add user message
	messages = append(messages, llm.Message{Role: "user", Content: userMessage})
	if err := a.db.AddMessage(conv.ID, "user", userMessage, "", ""); err != nil {
		return nil, fmt.Errorf("store user message: %w", err)
	}

	// Build tool definitions
	toolDefs := a.buildToolDefs()

	// ReAct loop (max 10 iterations to prevent infinite loops)
	var totalUsage llm.Usage
	for i := 0; i < 10; i++ {
		a.logger.Debug("LLM request", "iteration", i, "messages", len(messages))

		req := llm.ChatRequest{
			Model:       a.model,
			Messages:    messages,
			MaxTokens:   4096,
			Temperature: 0.7,
		}
		if len(toolDefs) > 0 {
			req.Tools = toolDefs
		}

		resp, err := a.llmClient.Chat(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("LLM call: %w", err)
		}

		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("no choices in LLM response")
		}

		choice := resp.Choices[0]

		// If no tool calls — return the text response
		if len(choice.Message.ToolCalls) == 0 {
			text := choice.Message.Content
			if err := a.db.AddMessage(conv.ID, "assistant", text, "", ""); err != nil {
				a.logger.Error("store assistant message", "error", err)
			}
			return &Response{Text: text, Usage: &totalUsage}, nil
		}

		// Store assistant message with tool calls
		toolCallsJSON, _ := json.Marshal(choice.Message.ToolCalls)
		if err := a.db.AddMessage(conv.ID, "assistant", choice.Message.Content, "", string(toolCallsJSON)); err != nil {
			a.logger.Error("store assistant tool call message", "error", err)
		}
		messages = append(messages, choice.Message)

		// Execute tool calls
		for _, tc := range choice.Message.ToolCalls {
			a.logger.Info("tool call", "tool", tc.Function.Name, "args", tc.Function.Arguments)

			result, err := a.tools.Execute(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
			if err != nil {
				result = fmt.Sprintf("Error: %s", err.Error())
				a.logger.Error("tool execution error", "tool", tc.Function.Name, "error", err)
			}

			toolMsg := llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			}
			messages = append(messages, toolMsg)
			if err := a.db.AddMessage(conv.ID, "tool", result, tc.ID, ""); err != nil {
				a.logger.Error("store tool result", "error", err)
			}
		}
	}

	return &Response{Text: "Превышен лимит итераций агента. Попробуй упростить запрос.", Usage: &totalUsage}, nil
}

// NewConversation starts a new conversation for the chat.
func (a *Agent) NewConversation(chatID int64) error {
	_, err := a.db.CreateConversation(chatID, a.model)
	return err
}

func (a *Agent) buildToolDefs() []llm.ToolDef {
	allTools := a.tools.All()
	defs := make([]llm.ToolDef, 0, len(allTools))
	for _, t := range allTools {
		fn := t["function"].(map[string]interface{})
		defs = append(defs, llm.ToolDef{
			Type: "function",
			Function: llm.ToolFuncDef{
				Name:        fn["name"].(string),
				Description: fn["description"].(string),
				Parameters:  fn["parameters"],
			},
		})
	}
	return defs
}
