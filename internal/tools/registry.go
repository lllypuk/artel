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
	tools      map[string]Tool
	validators map[string]*compiledSchema // tool name → pre-compiled JSON Schema
	notify     Notifier
	logger     *slog.Logger
}

// NewRegistry creates a new tool registry.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{
		tools:      make(map[string]Tool),
		validators: make(map[string]*compiledSchema),
		logger:     logger,
	}
}

// SetNotifier sets the notification callback for long-running operations.
func (r *Registry) SetNotifier(notify Notifier) {
	r.notify = notify
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
	// Pre-compile the JSON Schema for fast validation on every Execute call.
	params := t.Parameters()
	if params != nil {
		cs, err := compileSchema(params)
		if err != nil {
			r.logger.Warn("failed to compile JSON Schema for tool", "tool", t.Name(), "error", err)
		} else {
			r.validators[t.Name()] = cs
		}
	}
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

// ExecuteWithNotify finds and executes a tool by name.
// Validates arguments against the tool's JSON Schema before execution.
func (r *Registry) ExecuteWithNotify(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}

	// Validate arguments against JSON Schema if available.
	if cs, hasSchema := r.validators[name]; hasSchema && len(args) > 0 {
		if err := validateArgs(cs, args); err != nil {
			return "", fmt.Errorf("invalid args for tool %s: %w", name, err)
		}
	}

	return t.Execute(ctx, args)
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
