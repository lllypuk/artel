package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

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

// Registry holds all registered tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
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
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
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
