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
// It receives the chat ID and tool name and should send an appropriate message to the user.
type Notifier func(chatID int64, toolName string)

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
			r.notify(chatID, name)
		}
	}

	return t.Execute(ctx, args)
}

// shouldNotify returns true if the tool should trigger a notification.
func (r *Registry) shouldNotify(name string) bool {
	switch name {
	case "web_search", "web_fetch", "pi_coder", "notes":
		return true
	default:
		return false
	}
}

// NotificationMessages returns user-friendly messages for tool operations.
func NotificationMessages() map[string]string {
	return map[string]string{
		"web_search": "🔍 Ищу информацию в интернете...",
		"web_fetch":  "📖 Читаю страницу...",
		"pi_coder":   "⚙️ Выполняю доработку кода агента...",
		"notes":      "📝 Работаю с заметками...",
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
