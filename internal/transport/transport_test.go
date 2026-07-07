package transport

import (
	"context"
	"testing"
)

// mockTransport is a minimal implementation used to verify the Transport
// interface contract and message field propagation.
type mockTransport struct {
	sent []Message
}

func (m *mockTransport) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (m *mockTransport) Send(_ context.Context, msg Message) error {
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mockTransport) Messages() <-chan InboundMessage {
	ch := make(chan InboundMessage, 1)
	ch <- InboundMessage{ChatID: "chat-1", Text: "hello"}
	close(ch)
	return ch
}

func (m *mockTransport) Close() error {
	return nil
}

func TestTransportInterfaceContract(t *testing.T) {
	var tr Transport = &mockTransport{}
	mock := tr.(*mockTransport)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tr.Run(ctx); err != nil && err != context.Canceled {
		t.Fatalf("expected ctx cancel error, got %v", err)
	}

	if err := tr.Send(ctx, Message{TargetID: "chat-1", Text: "reply", ThreadID: "t1"}); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if len(mock.sent) != 1 || mock.sent[0].TargetID != "chat-1" {
		t.Fatalf("sent message not recorded: %+v", mock.sent)
	}

	msg := <-tr.Messages()
	if msg.ChatID != "chat-1" || msg.Text != "hello" {
		t.Fatalf("unexpected inbound message: %+v", msg)
	}
}
