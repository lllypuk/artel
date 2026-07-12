package telegram

import (
	"context"
	"testing"

	"github.com/lllypuk/artel/internal/transport"
)

func TestParseChatID(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		{"123", 123, false},
		{"-456", -456, false},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseChatID(tt.input)
			if (err != nil) != tt.err {
				t.Fatalf("parseChatID(%q) error = %v, wantErr %v", tt.input, err, tt.err)
			}
			if got != tt.want {
				t.Errorf("parseChatID(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestToInboundMessage(t *testing.T) {
	m := Message{
		ChatID:     "123",
		SenderID:   "456",
		SenderName: "tester",
		IsBot:      false,
		Text:       "hello",
		Mentions:   []string{"agent"},
		IsDM:       true,
		ReplyTo:    "42",
	}
	got := toInboundMessage(m)
	if got.ChatID != m.ChatID || got.IsDM != m.IsDM || got.ReplyTo != "42" {
		t.Errorf("toInboundMessage field mismatch: %+v", got)
	}
}

func TestTelegramTransportSend_InvalidTarget(t *testing.T) {
	// A nil bot cannot be created; build a minimal transport manually.
	tr := NewTransport(nil)
	if err := tr.Send(context.Background(), transport.Message{TargetID: "", Text: "hi"}); err == nil {
		t.Error("expected error for empty target")
	}
}
