package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// contextKey is the type for context keys.
type contextKey struct{}

// ChatIDKey is the context key for storing the Telegram chat ID.
var ChatIDKey = contextKey{}

// Tool is the interface all agent tools implement.
type Tool interface {
	// Name returns the tool name for LLM function calling.
	Name() string
	// Description returns a human-readable description.
	Description() string
	// Parameters returns the JSON Schema for parameters.
	Parameters() map[string]any
	// Execute runs the tool with given JSON arguments.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Notifier is a callback for sending operation status notifications.
// It receives the chat ID and a ready-to-send message string.
type Notifier func(chatID int64, message string)

// Registry holds all registered tools.
type Registry struct {
	tools  map[string]Tool
	notify Notifier
	logger *slog.Logger
}

// NewRegistry creates a new tool registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		tools:  make(map[string]Tool),
		logger: logger,
	}
}

// SetNotifier sets the notification callback for long-running operations.
func (r *Registry) SetNotifier(notify Notifier) {
	r.notify = notify
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Execute finds and executes a tool by name.
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (string, error) {
	return r.ExecuteWithNotify(ctx, name, args)
}

// ExecuteWithNotify finds and executes a tool by name, sending a notification first.
func (r *Registry) ExecuteWithNotify(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// Send notification before executing long-running tools
	if r.notify != nil && r.shouldNotify(name) {
		if chatID, ok := ctx.Value(ChatIDKey).(int64); ok {
			r.notify(chatID, buildNotificationMessage(name, args))
		}
	}

	return t.Execute(ctx, args)
}

// shouldNotify returns true if the tool should trigger a notification.
func (r *Registry) shouldNotify(name string) bool {
	switch name {
	case "web_search", "web_fetch", "pi_coder", "notes", "web_screenshot":
		return true
	default:
		return false
	}
}

// buildNotificationMessage builds a context-aware notification message for a tool call.
func buildNotificationMessage(name string, args json.RawMessage) string {
	switch name {
	case "web_search":
		var a struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(args, &a); err == nil && a.Query != "" {
			return "🔍 Ищу: " + a.Query + "..."
		}
		return "🔍 Ищу информацию в интернете..."
	case "web_fetch":
		var a struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(args, &a); err == nil && a.URL != "" {
			return "📖 Читаю страницу: " + a.URL + "..."
		}
		return "📖 Читаю страницу..."
	case "web_screenshot":
		var a struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(args, &a); err == nil && a.URL != "" {
			return "📸 Делаю скриншот: " + a.URL + "..."
		}
		return "📸 Делаю скриншот..."
	case "pi_coder":
		return "⚙️ Выполняю доработку кода агента..."
	case "notes":
		return "📝 Работаю с заметками..."
	case "code_runner":
		var a struct {
			Language string `json:"language"`
		}
		if err := json.Unmarshal(args, &a); err == nil && a.Language != "" {
			return "⚙️ Выполняю код (" + a.Language + ")..."
		}
		return "⚙️ Выполняю код..."
	default:
		return ""
	}
}

// All returns all registered tools as LLM tool definitions.
func (r *Registry) All() []map[string]any {
	var defs []map[string]any
	for _, t := range r.tools {
		defs = append(defs, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  t.Parameters(),
			},
		})
	}
	return defs
}
