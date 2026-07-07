package transport

import (
	"testing"
)

func TestTransportTypes(t *testing.T) {
	msg := Message{
		TargetID: "123",
		Text:     "hello",
		ThreadID: "t1",
		ReplyTo:  "m1",
	}
	if msg.TargetID != "123" {
		t.Errorf("TargetID mismatch: %s", msg.TargetID)
	}

	in := InboundMessage{
		ChatID:     "c1",
		ThreadID:   "t1",
		SenderID:   "s1",
		SenderName: "Tester",
		IsBot:      false,
		Text:       "hi",
		Mentions:   []string{"agent"},
		IsDM:       true,
		ReplyTo:    "m1",
	}
	if in.ChatID != "c1" || !in.IsDM {
		t.Errorf("InboundMessage field mismatch")
	}
}
