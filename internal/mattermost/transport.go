package mattermost

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lllypuk/agent-shatrov/internal/transport"
)

// mentionRe matches Mattermost @-mentions. Mention names consist of letters,
// digits, dots, dashes and underscores and stop on a word boundary.
var mentionRe = regexp.MustCompile(`\B@[A-Za-z0-9_.\-]+\b`)

// Transport implements transport.Transport for Mattermost.
type Transport struct {
	rest      *RESTClient
	ws        *WSClient
	selfID    string
	selfName  string
	mu        sync.Mutex
	userCache map[string]*User
	messages  chan transport.InboundMessage
	logger    *slog.Logger
	closeOnce sync.Once
}

// NewTransport creates a Mattermost transport backed by the given server URL
// and bot token. The proxy URL is optional.
func NewTransport(serverURL, token, proxyURL string, logger *slog.Logger) (*Transport, error) {
	httpClient, err := newHTTPClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("mattermost transport: create http client: %w", err)
	}
	rest := NewRESTClient(serverURL, token, httpClient)

	dialer := newWebsocketDialer(proxyURL)
	ws := NewWSClient(serverURL, token, logger, dialer)

	return &Transport{
		rest:      rest,
		ws:        ws,
		messages:  make(chan transport.InboundMessage, 64),
		userCache: make(map[string]*User),
		logger:    logger,
	}, nil
}

// Identify fetches the bot's own Mattermost user record. It is idempotent and
// safe to call before Run so the rest of the application can know SelfID and
// SelfUsername (for auth/safeguards) before the WebSocket loop starts.
func (t *Transport) Identify(ctx context.Context) error {
	if t.selfID != "" {
		return nil
	}

	me, err := t.rest.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("mattermost transport: identify self: %w", err)
	}
	t.selfID = me.ID
	t.selfName = me.Username
	t.cacheUser(me)

	t.logger.Info("mattermost transport authorized", "username", me.Username, "bot", me.IsBot)
	return nil
}

// Run starts the WebSocket read loop and blocks until the context is cancelled.
func (t *Transport) Run(ctx context.Context) error {
	defer t.Close() //nolint:errcheck

	if err := t.Identify(ctx); err != nil {
		return err
	}

	t.ws.OnPosted(func(ev PostedEvent) {
		select {
		case t.messages <- t.adaptInbound(ctx, ev):
		case <-ctx.Done():
		}
	})

	if err := t.ws.Connect(ctx); err != nil {
		return err
	}

	<-ctx.Done()
	return ctx.Err()
}

// Send delivers a text message to a Mattermost channel or thread.
func (t *Transport) Send(ctx context.Context, m transport.Message) error {
	if m.TargetID == "" {
		return fmt.Errorf("mattermost transport: target channel id is required")
	}
	rootID := m.ThreadID
	if rootID == "" && m.ReplyTo != "" {
		rootID = m.ReplyTo
	}
	_, err := t.rest.PostMessage(ctx, m.TargetID, m.Text, rootID)
	return err
}

// Messages returns inbound messages in the transport-neutral format.
func (t *Transport) Messages() <-chan transport.InboundMessage {
	return t.messages
}

// Close shuts down the WebSocket connection and the inbound messages channel.
func (t *Transport) Close() error {
	var err error
	t.closeOnce.Do(func() {
		err = t.ws.Close()
		close(t.messages)
	})
	return err
}

// adaptInbound converts a Mattermost posted event to the transport-neutral
// format. It caches user metadata to avoid repeating REST calls. If the user
// lookup fails, IsBot remains false; callers with StrictBotWhitelist will
// reject unverified team-bot-looking senders in the auth gate.
func (t *Transport) adaptInbound(ctx context.Context, ev PostedEvent) transport.InboundMessage {
	u := t.userInfo(ctx, ev.UserID)
	// WebSocket posted events sometimes include a leading '@' in the sender
	// name, while config values and REST user records use the bare username.
	senderName := strings.TrimPrefix(ev.Username, "@")
	isBot := false
	if u != nil {
		// Prefer the authoritative username from the REST profile.
		senderName = u.Username
		isBot = u.IsBot
	}

	return transport.InboundMessage{
		ChatID:     ev.ChannelID,
		ThreadID:   ev.RootID,
		SenderID:   ev.UserID,
		SenderName: senderName,
		IsBot:      isBot,
		Text:       strings.TrimSpace(ev.Message),
		Mentions:   extractMentions(ev.Message),
		IsDM:       ev.ChannelType == ChannelTypeDirect,
	}
}

func (t *Transport) cacheUser(u *User) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if u != nil {
		t.userCache[u.ID] = u
	}
}

func (t *Transport) userInfo(ctx context.Context, userID string) *User {
	t.mu.Lock()
	if u, ok := t.userCache[userID]; ok {
		t.mu.Unlock()
		return u
	}
	t.mu.Unlock()

	u, err := t.rest.GetUser(ctx, userID)
	if err != nil {
		t.logger.Debug("mattermost: failed to fetch user info", "user_id", userID, "error", err)
		return nil
	}
	t.cacheUser(u)
	return u
}

// SelfID returns the transport's own Mattermost user ID.
func (t *Transport) SelfID() string {
	return t.selfID
}

// SelfUsername returns the transport's own Mattermost username.
func (t *Transport) SelfUsername() string {
	return t.selfName
}

// extractMentions returns account names mentioned in the message without the leading '@'.
func extractMentions(message string) []string {
	matches := mentionRe.FindAllString(message, -1)
	if len(matches) == 0 {
		return nil
	}
	mentions := make([]string, 0, len(matches))
	for _, m := range matches {
		mentions = append(mentions, strings.TrimPrefix(m, "@"))
	}
	return mentions
}

func newHTTPClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{}, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("parse proxy url %q: %w", proxyURL, err)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed)},
	}, nil
}

func newWebsocketDialer(proxyURL string) *websocket.Dialer {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	if proxyURL == "" {
		return dialer
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return dialer
	}
	dialer.Proxy = http.ProxyURL(parsed)
	return dialer
}
