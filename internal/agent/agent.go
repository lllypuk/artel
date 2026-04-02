package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

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

Поиск и работа с интернетом:
- Для поиска информации используй tool "web_search" — он вернёт список результатов с заголовками, сниппетами и URL
- Если нужно прочитать конкретную страницу подробнее — используй tool "web_fetch" с нужным URL
- После получения результатов поиска ВСЕГДА делай краткое резюме (суммаризацию) найденной информации на русском языке
- Если результаты поиска недостаточны — используй web_fetch для чтения наиболее релевантной страницы

Доработка и обновление агента:
- Используй tool "pi_coder" для изменения собственного исходного кода
- Формулируй задачу подробно: что добавить/изменить/исправить, какое поведение ожидается
- После успешного изменения кода можно передать rebuild=true для пересборки бинарника
- Если пересборка прошла успешно — сообщи пользователю и предложи перезапустить агента (make restart)
- Для перезапуска агента после сборки можно использовать shell tool (если включён)

Доступные возможности:
- Создание и поиск заметок
- Поиск в интернете и чтение веб-страниц
- Доработка собственного кода через pi_coder (если включено)
- Выполнение безопасных shell-команд (если включено)
- Общий диалог и помощь
`

// Agent is the core agent implementing the ReAct loop.
type Agent struct {
	llmClient     *llm.Client
	model         string
	maxIterations int
	tools         *tools.Registry
	db            *storage.DB
	logger        *slog.Logger
	notify        tools.Notifier
}

// New creates a new Agent.
func New(llmClient *llm.Client, model string, maxIterations int, toolRegistry *tools.Registry, db *storage.DB, logger *slog.Logger) *Agent {
	if maxIterations <= 0 {
		maxIterations = 10
	}
	return &Agent{
		llmClient:     llmClient,
		model:         model,
		maxIterations: maxIterations,
		tools:         toolRegistry,
		db:            db,
		logger:        logger,
	}
}

// SetNotifier sets the notification callback for long-running operations.
func (a *Agent) SetNotifier(notify tools.Notifier) {
	a.notify = notify
	a.tools.SetNotifier(notify)
}

// Notify sends a notification to the user about a tool operation.
// This is used by external components (like Telegram bot) to send notifications.
func (a *Agent) Notify(chatID int64, toolName string) error {
	if a.notify != nil {
		a.notify(chatID, toolName)
		return nil
	}
	return fmt.Errorf("no notifier configured")
}

// Response from the agent.
type Response struct {
	Text  string
	Usage *llm.Usage
}

// HandleMessage processes a user message and returns the agent's response.
// It implements the ReAct loop: LLM → tool calls → LLM → ... → final text.
func (a *Agent) HandleMessage(ctx context.Context, chatID int64, userMessage string) (*Response, error) {
	// Add chatID to context for notifications
	ctx = context.WithValue(ctx, tools.ChatIDKey, chatID)

	// Get or create conversation
	conv, err := a.db.GetActiveConversation(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv == nil {
		convID, err := a.db.CreateConversation(ctx, chatID, a.model)
		if err != nil {
			return nil, fmt.Errorf("create conversation: %w", err)
		}
		conv = &storage.Conversation{ID: convID, ChatID: chatID, Model: a.model}
	}

	// Load conversation history
	storedMsgs, err := a.db.GetMessages(ctx, conv.ID)
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
	if err := a.db.AddMessage(ctx, conv.ID, "user", userMessage, "", ""); err != nil {
		return nil, fmt.Errorf("store user message: %w", err)
	}

	// Build tool definitions
	toolDefs := a.buildToolDefs()

	// ReAct loop (max iterations to prevent infinite loops)
	var totalUsage llm.Usage
	for i := range a.maxIterations {
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
			if err := a.db.AddMessage(ctx, conv.ID, "assistant", text, "", ""); err != nil {
				a.logger.Error("store assistant message", "error", err)
			}
			return &Response{Text: text, Usage: &totalUsage}, nil
		}

		// Store assistant message with tool calls
		toolCallsJSON, _ := json.Marshal(choice.Message.ToolCalls)
		if err := a.db.AddMessage(ctx, conv.ID, "assistant", choice.Message.Content, "", string(toolCallsJSON)); err != nil {
			a.logger.Error("store assistant tool call message", "error", err)
		}
		messages = append(messages, choice.Message)

		// Execute tool calls
		for _, tc := range choice.Message.ToolCalls {
			cleanArgs := sanitizeToolArgs(json.RawMessage(tc.Function.Arguments))
			a.logger.Info("tool call", "tool", tc.Function.Name, "args", string(cleanArgs))

			result, err := a.tools.ExecuteWithNotify(ctx, tc.Function.Name, cleanArgs)
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
			if err := a.db.AddMessage(ctx, conv.ID, "tool", result, tc.ID, ""); err != nil {
				a.logger.Error("store tool result", "error", err)
			}
		}
	}

	return &Response{Text: "Превышен лимит итераций агента. Попробуй упростить запрос.", Usage: &totalUsage}, nil
}

// NewConversation starts a new conversation for the chat.
func (a *Agent) NewConversation(ctx context.Context, chatID int64) error {
	_, err := a.db.CreateConversation(ctx, chatID, a.model)
	return err
}

// sanitizeToolArgs cleans up malformed JSON that GLM sometimes generates.
// GLM occasionally encodes extra arguments inside the action string using
// XML-like tags: {"action":"discover<arg_key>category</arg_key><arg_value>sports"}.
// It also handles truncated output that ends with </tool_call>.
func sanitizeToolArgs(raw json.RawMessage) json.RawMessage {
	s := strings.TrimSpace(string(raw))

	// Fast path: no XML artifacts → nothing to fix.
	if !strings.Contains(s, "<arg_key>") && !strings.Contains(s, "</tool_call>") {
		return raw
	}

	// Fix truncated JSON that ends with </tool_call> (model stopped mid-string).
	if idx := strings.Index(s, "</tool_call>"); idx >= 0 {
		s = s[:idx]
		// Close the open JSON string and object if needed.
		s = strings.TrimRight(s, " \t") + `"}`
	}

	// Parse into a generic map to manipulate fields.
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		// Can't fix, return original so the tool returns a proper error.
		return raw
	}

	// Check whether the "action" field embeds XML-encoded extra args.
	actionVal, _ := m["action"].(string)
	if strings.Contains(actionVal, "<arg_key>") {
		// Trim the clean action name.
		cleanIdx := strings.Index(actionVal, "<")
		if cleanIdx < 0 {
			cleanIdx = len(actionVal)
		}
		m["action"] = actionVal[:cleanIdx]
		remaining := actionVal[cleanIdx:]

		// Extract <arg_key>key</arg_key><arg_value>value</arg_value> pairs.
		for strings.Contains(remaining, "<arg_key>") {
			ks := strings.Index(remaining, "<arg_key>") + len("<arg_key>")
			ke := strings.Index(remaining, "</arg_key>")
			if ks < 0 || ke < ks {
				break
			}
			key := remaining[ks:ke]
			remaining = remaining[ke+len("</arg_key>"):]

			vs := strings.Index(remaining, "<arg_value>") + len("<arg_value>")
			ve := strings.Index(remaining, "</arg_value>")
			if vs < 0 || ve < vs {
				break
			}
			val := remaining[vs:ve]
			remaining = remaining[ve+len("</arg_value>"):]

			// Only fill in if the field isn't already present.
			if _, exists := m[key]; !exists {
				m[key] = val
			}
		}
	}

	fixed, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return fixed
}

func (a *Agent) buildToolDefs() []llm.ToolDef {
	allTools := a.tools.All()
	defs := make([]llm.ToolDef, 0, len(allTools))
	for _, t := range allTools {
		fn := t["function"].(map[string]any)
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
