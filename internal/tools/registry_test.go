package tools_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/lllypuk/artel/internal/tools"
)

// mockTool is a test double for the Tool interface, shared across all
// *_test.go files in package tools_test within this directory.
type mockTool struct {
	name   string
	result string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return "mock: " + m.name }
func (m *mockTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (m *mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return m.result, nil
}

// ── Registry ──────────────────────────────────────────────────────────────────

func TestNewRegistry(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

func TestRegisterAndGet(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&mockTool{name: "tool1"})

	got, ok := r.Get("tool1")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Name() != "tool1" {
		t.Errorf("expected name 'tool1', got %q", got.Name())
	}
}

func TestGetUnknown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected not to find nonexistent tool")
	}
}

func TestExecuteKnownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&mockTool{name: "greeter", result: "hello!"})

	res, err := r.Execute(context.Background(), "greeter", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "hello!" {
		t.Errorf("expected 'hello!', got %q", res)
	}
}

func TestExecuteUnknownTool(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	_, err := r.Execute(context.Background(), "missing", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestAllEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	if all := r.All(); len(all) != 0 {
		t.Errorf("expected 0 tools, got %d", len(all))
	}
}

func TestAllWithTools(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&mockTool{name: "alpha"})
	r.Register(&mockTool{name: "beta"})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(all))
	}

	names := make(map[string]bool)
	for _, def := range all {
		if def["type"] != "function" {
			t.Errorf("expected type 'function', got %v", def["type"])
		}
		fn, ok := def["function"].(map[string]any)
		if !ok {
			t.Fatal("function field should be map[string]any")
		}
		name, _ := fn["name"].(string)
		names[name] = true
	}
	if !names["alpha"] || !names["beta"] {
		t.Errorf("expected both 'alpha' and 'beta', got %v", names)
	}
}

func TestRegisterOverwrite(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&mockTool{name: "dup", result: "first"})
	r.Register(&mockTool{name: "dup", result: "second"})

	res, err := r.Execute(context.Background(), "dup", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "second" {
		t.Errorf("expected 'second' (last registration wins), got %q", res)
	}
}

// ── JSON Schema validation ───────────────────────────────────────────────────

// strictMockTool has a JSON Schema with required fields and typed properties.
type strictMockTool struct {
	name string
}

func (m *strictMockTool) Name() string        { return m.name }
func (m *strictMockTool) Description() string { return "strict tool: " + m.name }
func (m *strictMockTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"go", "stop"},
			},
			"count": map[string]any{
				"type": "integer",
			},
		},
		"required": []string{"action"},
	}
}
func (m *strictMockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "ok", nil
}

func TestRegistryValidPayload(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&strictMockTool{name: "strict"})

	// Valid: has required action + optional count (correct type).
	_, err := r.Execute(context.Background(), "strict", json.RawMessage(`{"action":"go","count":5}`))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestRegistryInvalidPayloadWrongType(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&strictMockTool{name: "strict"})

	// count should be integer, not string.
	_, err := r.Execute(context.Background(), "strict", json.RawMessage(`{"action":"go","count":"abc"}`))
	if err == nil {
		t.Fatal("expected validation error for wrong type")
	}
	if !contains(err.Error(), "invalid args") {
		t.Errorf("expected 'invalid args' in error, got: %s", err.Error())
	}
}

func TestRegistryMissingRequiredField(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&strictMockTool{name: "strict"})

	// Missing required "action" field.
	_, err := r.Execute(context.Background(), "strict", json.RawMessage(`{"count":1}`))
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	if !contains(err.Error(), "action") && !contains(err.Error(), "required") {
		t.Errorf("expected error about missing required field 'action', got: %s", err.Error())
	}
}

func TestRegistryValidPayloadMinimal(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&strictMockTool{name: "strict"})

	// Valid: only required field.
	_, err := r.Execute(context.Background(), "strict", json.RawMessage(`{"action":"stop"}`))
	if err != nil {
		t.Fatalf("expected no error for minimal valid payload, got: %v", err)
	}
}

func TestRegistryInvalidEnumValue(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	r.Register(&strictMockTool{name: "strict"})

	// "action" must be "go" or "stop", not "invalid".
	_, err := r.Execute(context.Background(), "strict", json.RawMessage(`{"action":"invalid"}`))
	if err == nil {
		t.Fatal("expected validation error for invalid enum value")
	}
}

func TestRegistryNoSchemaNoValidation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	r := tools.NewRegistry(logger)
	// mockTool returns {"type": "object"} with no properties/required — everything passes.
	r.Register(&mockTool{name: "lenient"})

	_, err := r.Execute(context.Background(), "lenient", json.RawMessage(`anything`))
	// mockTool schema says "object", so non-object JSON fails.
	if err == nil {
		t.Log("no validation error — tool with lenient schema")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
