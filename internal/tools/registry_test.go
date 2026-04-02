package tools_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/tools"
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
