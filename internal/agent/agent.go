package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"

	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

const systemPrompt = `Ты — персональный AI-ассистент Саши. Твоё имя — Agent.
Ты работаешь на локальном компьютере и можешь выполнять задачи через доступные инструменты.

КРИТИЧЕСКИЕ ПРАВИЛА:
- ВСЕГДА отвечай максимально кратко: 1-2 предложения для ответов
- НЕ пиши "Ищу...", "Выполняю...", "Готово..." и любые другие уведомления о прогрессе
- НЕ пиши "Вот результат", "Результат поиска" и подобное — сразу выдавай суть
- Пиши только то, что действительно нужно пользователю

Процесс работы:
1. Если запрос понятен и достаточно информации — кратко сообщи что сделаешь (1-2 предложения), затем выполняй
2. Если не хватает информации — задай ТОЛЬКО один-два вопроса максимально кратко
3. После выполнения дай краткий результат (1-2 предложения)

Поиск и работа с интернетом:
- Используй tool "web_search" для поиска, "web_fetch" для чтения страниц
- После поиска — краткое резюме на русском (2-3 предложения)

Доработка агента:
- Используй tool "pi_coder" для изменения кода
- Формулируй задачу точно: что изменить, какое поведение ожидается
- rebuild=true — пересобрать, restart=true — перезапустить после сборки

Заметки, напоминания, новости:
- notes: создание, поиск, редактирование
- reminders: create, list, delete, done (форматы: «через 2 часа», «завтра в 10:00», repeat="daily")
- news: подписки, чтение, поиск

Важно:
- Chat_id доступен автоматически — НЕ спрашивай его
- Файлы и фото присылай прямо в чат — агент сам их обработает
`

// Agent is the core agent implementing the ReAct loop.
type Agent struct {
	llmClient          llm.Provider
	model              string
	mu                 sync.RWMutex
	models             map[string]string // alias → model name, e.g. "fast" → "glm-4.7-flash"
	maxIterations      int
	maxContextMessages int // sliding context window (0 = no limit)
	tools              *tools.Registry
	db                 *storage.DB
	logger             *slog.Logger
	notify             tools.Notifier
}

// New creates a new Agent.
func New(llmClient llm.Provider, model string, maxIterations int, toolRegistry *tools.Registry, db *storage.DB, logger *slog.Logger) *Agent {
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

// GetModel returns the model currently used by the agent.
func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetModel sets the active model.
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
}

// SetModels configures the map of named model aliases (e.g. "fast" → "glm-4.7-flash").
// Should be called once after New().
func (a *Agent) SetModels(models map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.models = models
}

// SetMaxContextMessages sets the sliding context window size.
// When n > 0, messages older than the last n are summarised automatically.
// Use 0 to disable context management (load full history, legacy behaviour).
func (a *Agent) SetMaxContextMessages(n int) {
	if n < 0 {
		n = 0
	}
	a.maxContextMessages = n
}

// Models returns a copy of the named model aliases map.
func (a *Agent) Models() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make(map[string]string, len(a.models))
	maps.Copy(result, a.models)
	return result
}

// SetNotifier sets the notification callback for long-running operations.
func (a *Agent) SetNotifier(notify tools.Notifier) {
	a.notify = notify
	a.tools.SetNotifier(notify)
}

// ToolRegistry returns the tool registry.
func (a *Agent) ToolRegistry() *tools.Registry {
	return a.tools
}

// ProviderStatusEntry describes the health of a single LLM provider.
type ProviderStatusEntry struct {
	Name     string
	Priority int
	Healthy  bool
}

// GetProviderStatus returns the health status of all LLM providers.
// Returns nil when a single-provider Client is used (no fallback configured).
func (a *Agent) GetProviderStatus() []ProviderStatusEntry {
	mc, ok := a.llmClient.(*llm.MultiClient)
	if !ok {
		return nil
	}
	statuses := mc.ProviderStatus()
	result := make([]ProviderStatusEntry, len(statuses))
	for i, s := range statuses {
		result[i] = ProviderStatusEntry{
			Name:     s.Name,
			Priority: s.Priority,
			Healthy:  s.Healthy,
		}
	}
	return result
}

// FileAttachment represents a binary file (typically an image) to include
// in the LLM user message as base64-encoded data.
type FileAttachment struct {
	Data     []byte
	MimeType string // e.g. "image/jpeg", "image/png"
	Filename string
}

// Response from the agent.
type Response struct {
	Text  string
	Usage *llm.Usage
}

// HandleMessage processes a text-only user message.
func (a *Agent) HandleMessage(ctx context.Context, chatID int64, userMessage string) (*Response, error) {
	return a.handleMessage(ctx, chatID, userMessage, nil)
}

// HandleMessageWithAttachments processes a user message with optional image
// attachments (sent to the LLM as base64 data URLs when vision is supported).
func (a *Agent) HandleMessageWithAttachments(ctx context.Context, chatID int64, userMessage string, attachments []FileAttachment) (*Response, error) {
	return a.handleMessage(ctx, chatID, userMessage, attachments)
}

// handleMessage is the core ReAct-loop implementation used by both public entry points.
func (a *Agent) handleMessage(ctx context.Context, chatID int64, userMessage string, attachments []FileAttachment) (*Response, error) {
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

	// Load conversation history and apply context window management.
	contextMsgs, err := a.manageContext(ctx, conv)
	if err != nil {
		return nil, fmt.Errorf("build context: %w", err)
	}

	// Build messages for LLM
	messages := make([]llm.Message, 0, len(contextMsgs)+2)
	messages = append(messages, llm.Message{Role: "system", Content: systemPrompt})
	messages = append(messages, contextMsgs...)

	// Add user message (multi-modal when image attachments are present)
	messages = append(messages, buildUserMessage(userMessage, attachments))
	if err := a.db.AddMessage(ctx, conv.ID, "user", userMessage, "", ""); err != nil {
		return nil, fmt.Errorf("store user message: %w", err)
	}

	// Build tool definitions
	toolDefs := a.buildToolDefs()

	// ReAct loop (max iterations to prevent infinite loops)
	var totalUsage llm.Usage
	for i := 0; i < a.maxIterations; i++ {
		a.mu.RLock()
		currentModel := a.model
		a.mu.RUnlock()

		req := llm.ChatRequest{
			Model:       currentModel,
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

		// Persist usage statistics for /status reporting.
		if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
			if err := a.db.RecordUsage(ctx, resp.Usage.PromptTokens, resp.Usage.CompletionTokens); err != nil {
				a.logger.Warn("record usage", "error", err)
			}
		}

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

// buildUserMessage constructs a user llm.Message.
// When attachments are provided the content becomes a multi-modal array
// with text + base64-encoded image data URLs.
func buildUserMessage(text string, attachments []FileAttachment) llm.Message {
	if len(attachments) == 0 {
		return llm.Message{Role: "user", Content: text}
	}
	parts := []llm.ContentPart{{Type: "text", Text: text}}
	for _, att := range attachments {
		b64 := base64.StdEncoding.EncodeToString(att.Data)
		dataURL := fmt.Sprintf("data:%s;base64,%s", att.MimeType, b64)
		parts = append(parts, llm.ContentPart{
			Type:     "image_url",
			ImageURL: &llm.ImageURLContent{URL: dataURL},
		})
	}
	return llm.Message{Role: "user", ContentParts: parts}
}
