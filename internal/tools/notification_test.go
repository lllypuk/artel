package tools_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNotificationMessages(t *testing.T) {
	msgs := tools.NotificationMessages()

	expected := map[string]string{
		"web_search": "🔍 Ищу информацию в интернете...",
		"web_fetch":  "📖 Читаю страницу...",
		"pi_coder":   "⚙️ Выполняю доработку кода агента...",
		"notes":      "📝 Работаю с заметками...",
	}

	for tool, expectedMsg := range expected {
		if msgs[tool] != expectedMsg {
			t.Errorf("tool %s: expected %q, got %q", tool, expectedMsg, msgs[tool])
		}
	}
}

func TestRegistryNotify(t *testing.T) {
	logger := silentLogger()
	r := tools.NewRegistry(logger)

	// Track notifications
	var notifiedTool string
	var notifiedChatID int64

	r.SetNotifier(func(chatID int64, toolName string) {
		notifiedChatID = chatID
		notifiedTool = toolName
	})

	// Register mock tools with names that should trigger notifications
	r.Register(&mockTool{name: "web_search", result: "search results"})
	r.Register(&mockTool{name: "test_tool", result: "ok"})

	// Test that notification is sent for web_search
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, int64(12345))
	_, err := r.ExecuteWithNotify(ctx, "web_search", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notifiedTool != "web_search" {
		t.Errorf("expected notified tool 'web_search', got %q", notifiedTool)
	}
	if notifiedChatID != 12345 {
		t.Errorf("expected chat ID 12345, got %d", notifiedChatID)
	}

	// Test that notification is NOT sent for unknown tool (mock)
	notifiedTool = ""
	notifiedChatID = 0
	_, err = r.ExecuteWithNotify(ctx, "test_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if notifiedTool != "" {
		t.Errorf("expected no notification for test_tool, got %q", notifiedTool)
	}
}

func TestRegistryNotifyWithoutChatID(t *testing.T) {
	logger := silentLogger()
	r := tools.NewRegistry(logger)

	var notified bool
	r.SetNotifier(func(chatID int64, toolName string) {
		notified = true
	})

	// Register web_search mock
	r.Register(&mockTool{name: "web_search", result: "search results"})

	// Execute without chatID in context
	_, err := r.ExecuteWithNotify(context.Background(), "web_search", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Notification should NOT be sent if chatID is not in context
	if notified {
		t.Error("expected no notification when chatID is not in context")
	}
}
