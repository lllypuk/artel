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

func TestRegistryNotify(t *testing.T) {
	logger := silentLogger()
	r := tools.NewRegistry(logger)

	var notifiedMsg string
	var notifiedChatID int64

	r.SetNotifier(func(chatID int64, message string) {
		notifiedChatID = chatID
		notifiedMsg = message
	})

	r.Register(&mockTool{name: "web_search", result: "search results"})
	r.Register(&mockTool{name: "test_tool", result: "ok"})

	// web_search with query — notifications are disabled for silent operation
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, int64(12345))
	_, err := r.ExecuteWithNotify(ctx, "web_search", []byte(`{"query":"golang channels"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Notifications should be disabled
	if notifiedMsg != "" {
		t.Errorf("expected no notification (notifications disabled), got %q", notifiedMsg)
	}
	if notifiedChatID != 0 {
		t.Errorf("expected chat ID 0 (no notification), got %d", notifiedChatID)
	}

	// test_tool must NOT trigger a notification
	notifiedMsg = ""
	notifiedChatID = 0
	_, err = r.ExecuteWithNotify(ctx, "test_tool", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notifiedMsg != "" {
		t.Errorf("expected no notification for test_tool, got %q", notifiedMsg)
	}
}

func TestRegistryNotifyWebFetch(t *testing.T) {
	logger := silentLogger()
	r := tools.NewRegistry(logger)

	var notifiedMsg string
	r.SetNotifier(func(chatID int64, message string) {
		notifiedMsg = message
	})

	r.Register(&mockTool{name: "web_fetch", result: "page content"})

	ctx := context.WithValue(context.Background(), tools.ChatIDKey, int64(1))
	_, err := r.ExecuteWithNotify(ctx, "web_fetch", []byte(`{"url":"https://example.com"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Notifications should be disabled
	if notifiedMsg != "" {
		t.Errorf("expected no notification (notifications disabled), got %q", notifiedMsg)
	}
}

func TestRegistryNotifyWithoutChatID(t *testing.T) {
	logger := silentLogger()
	r := tools.NewRegistry(logger)

	var notified bool
	r.SetNotifier(func(chatID int64, message string) {
		notified = true
	})

	r.Register(&mockTool{name: "web_search", result: "search results"})

	// No chatID in context — notification must NOT fire (and they're disabled anyway)
	_, err := r.ExecuteWithNotify(context.Background(), "web_search", []byte(`{"query":"test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notified {
		t.Error("expected no notification (notifications disabled)")
	}
}
