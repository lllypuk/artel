// Package transport defines the common Transport abstraction used by the agent core.
// Implementations (Telegram, Mattermost) live in their own packages and are selected
// at startup by cmd/agent/main.go based on config.transport.type.
package transport

import (
	"context"
)

// Transport is the platform-neutral message transport used by the agent core.
type Transport interface {
	// Run starts the transport's receive loop and blocks until the context is cancelled.
	Run(ctx context.Context) error
	// Send delivers an outbound message to the given target (chat/channel/DM).
	Send(ctx context.Context, m Message) error
	// Messages returns a channel of inbound messages from the platform.
	Messages() <-chan InboundMessage
	// Close shuts down the transport and releases resources.
	Close() error
}

// Message is an outbound message to be delivered by a transport.
type Message struct {
	// TargetID is the chat/channel/user ID to send to.
	TargetID string
	// Text is the plain text or markdown-formatted message body.
	Text string
	// ThreadID optionally identifies a thread/topic to reply in (platform specific).
	ThreadID string
	// ReplyTo optionally identifies a message to reply to.
	ReplyTo string
}

// InboundMessage is a normalized message coming from any transport.
type InboundMessage struct {
	// ChatID/ChannelID is the unique identifier of the conversation/channel.
	ChatID string
	// ThreadID identifies a thread or topic, if any.
	ThreadID string
	// SenderID is the unique identifier of the sender.
	SenderID string
	// SenderName is the human-readable name of the sender.
	SenderName string
	// IsBot is true when the sender is a bot account.
	IsBot bool
	// Text is the message text with markup removed/normalized.
	Text string
	// Mentions lists accounts mentioned in the message (without the leading @).
	Mentions []string
	// IsDM is true when the message is a direct/private message.
	IsDM bool
	// ReplyTo optionally identifies the message this replies to.
	ReplyTo string
}
