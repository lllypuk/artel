package mattermost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// wsPongWait is how long we wait for a pong before declaring the
	// connection dead and triggering a reconnect.
	wsPongWait = 60 * time.Second
	// wsPingPeriod is how often we send a ping. Must be < wsPongWait.
	wsPingPeriod = 30 * time.Second
	// wsWriteWait bounds individual write operations.
	wsWriteWait = 10 * time.Second
	// wsReadLimit caps the size of a single WebSocket message.
	wsReadLimit = 1 << 20 // 1 MiB
	// wsAuthTimeout bounds the authentication handshake.
	wsAuthTimeout = 10 * time.Second
	// wsInitialReconnectDelay is the base backoff between reconnects.
	wsInitialReconnectDelay = 1 * time.Second
	// wsMaxReconnectDelay caps the exponential backoff.
	wsMaxReconnectDelay = 30 * time.Second
)

// PostedEvent represents a new message event from Mattermost WebSocket.
type PostedEvent struct {
	PostID      string
	ChannelID   string
	ChannelType string
	UserID      string
	Username    string
	RootID      string
	Message     string
}

// EventHandler is called for each new posted event.
type EventHandler func(PostedEvent)

// wsEvent is the raw WebSocket event envelope from Mattermost.
type wsEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
	Seq   int             `json:"seq"`
}

// wsAuthChallenge is sent to authenticate the WebSocket connection.
type wsAuthChallenge struct {
	Seq    int               `json:"seq"`
	Action string            `json:"action"`
	Data   map[string]string `json:"data"`
}

// WSClient manages a Mattermost WebSocket connection with automatic reconnect.
type WSClient struct {
	baseURL string
	token   string
	logger  *slog.Logger
	dialer  *websocket.Dialer
	handler EventHandler
	mu      sync.Mutex
	conn    *websocket.Conn
	closed  bool
}

// NewWSClient creates a new Mattermost WebSocket client.
func NewWSClient(baseURL, token string, logger *slog.Logger, dialer *websocket.Dialer) *WSClient {
	if logger == nil {
		logger = slog.Default()
	}
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	return &WSClient{
		baseURL: baseURL,
		token:   token,
		logger:  logger,
		dialer:  dialer,
	}
}

// OnPosted registers a handler for new post events.
func (w *WSClient) OnPosted(h EventHandler) {
	w.handler = h
}

// Connect establishes the WebSocket connection and starts the read loop with
// automatic reconnect. It returns once the initial handshake succeeds; the
// read loop continues in a goroutine until ctx is cancelled or Close() is
// called.
func (w *WSClient) Connect(ctx context.Context) error {
	conn, err := w.dialAndAuth(ctx)
	if err != nil {
		return err
	}

	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()

	w.logger.Info("mattermost: websocket connected", "url", toWSURL(w.baseURL))
	go w.runWithReconnect(ctx)
	return nil
}

// runWithReconnect runs the read loop and automatically reconnects with
// exponential backoff. It returns when ctx is cancelled or Close() is called.
func (w *WSClient) runWithReconnect(ctx context.Context) {
	delay := wsInitialReconnectDelay
	for {
		w.mu.Lock()
		conn := w.conn
		closed := w.closed
		w.mu.Unlock()

		if closed {
			return
		}
		if conn == nil {
			w.logger.Info("mattermost: websocket reconnecting", "delay", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			newConn, err := w.dialAndAuth(ctx)
			if err != nil {
				w.logger.Warn("mattermost: websocket reconnect failed", "error", err, "retry_in", delay)
				delay = nextBackoff(delay)
				continue
			}
			w.mu.Lock()
			w.conn = newConn
			w.mu.Unlock()
			conn = newConn
			delay = wsInitialReconnectDelay
			w.logger.Info("mattermost: websocket reconnected")
		}

		w.readLoop(ctx, conn)

		w.mu.Lock()
		alreadyClosed := w.closed
		if w.conn == conn {
			w.conn = nil
		}
		w.mu.Unlock()
		if alreadyClosed || ctx.Err() != nil {
			return
		}
	}
}

// nextBackoff doubles delay, capped at wsMaxReconnectDelay.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > wsMaxReconnectDelay {
		return wsMaxReconnectDelay
	}
	return d
}

// dialAndAuth opens a WebSocket, performs the authentication handshake, and
// configures connection-level limits.
func (w *WSClient) dialAndAuth(ctx context.Context) (*websocket.Conn, error) {
	wsURL := toWSURL(w.baseURL)

	conn, resp, err := w.dialer.DialContext(ctx, wsURL, http.Header{})
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("mattermost: websocket dial %s: %w", wsURL, err)
	}

	conn.SetReadLimit(wsReadLimit)

	auth := wsAuthChallenge{
		Seq:    1,
		Action: "authentication_challenge",
		Data:   map[string]string{"token": w.token},
	}
	if err := conn.WriteJSON(auth); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mattermost: websocket auth challenge: %w", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(wsAuthTimeout)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mattermost: set auth read deadline: %w", err)
	}
	for {
		var authResp map[string]any
		if err := conn.ReadJSON(&authResp); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("mattermost: websocket auth response: %w", err)
		}
		status, ok := authResp["status"].(string)
		if !ok {
			continue
		}
		if status != "OK" {
			_ = conn.Close()
			return nil, fmt.Errorf("mattermost: websocket auth failed: status=%q", status)
		}
		break
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mattermost: clear auth read deadline: %w", err)
	}

	return conn, nil
}

// readLoop consumes events from a single connection.
func (w *WSClient) readLoop(ctx context.Context, conn *websocket.Conn) {
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(wsPongWait))
	})

	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		ticker := time.NewTicker(wsPingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(wsWriteWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	defer func() {
		<-pingDone
		_ = conn.Close()
	}()

	_ = conn.SetReadDeadline(time.Now().Add(wsPongWait))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var ev wsEvent
		if err := conn.ReadJSON(&ev); err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			w.mu.Lock()
			alreadyClosed := w.closed
			w.mu.Unlock()
			if alreadyClosed {
				return
			}
			w.logger.Warn("mattermost: websocket read error (will reconnect)", "error", err)
			return
		}

		if ev.Event != "posted" {
			continue
		}

		posted, err := parsePostedEvent(ev.Data)
		if err != nil {
			w.logger.Warn("mattermost: parse posted event", "error", err)
			continue
		}

		if w.handler != nil {
			p := posted // capture for goroutine
			go w.handler(p)
		}
	}
}

// parsePostedEvent extracts a PostedEvent from the raw "data" field of a
// "posted" WebSocket event. Mattermost sends data.post as a JSON-encoded
// string.
func parsePostedEvent(data json.RawMessage) (PostedEvent, error) {
	var raw struct {
		Post        string `json:"post"`
		ChannelType string `json:"channel_type"`
		SenderName  string `json:"sender_name"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return PostedEvent{}, fmt.Errorf("mattermost: unmarshal posted data: %w", err)
	}

	var post Post
	if err := json.Unmarshal([]byte(raw.Post), &post); err != nil {
		return PostedEvent{}, fmt.Errorf("mattermost: unmarshal post: %w", err)
	}

	return PostedEvent{
		PostID:      post.ID,
		ChannelID:   post.ChannelID,
		ChannelType: raw.ChannelType,
		UserID:      post.UserID,
		Username:    raw.SenderName,
		RootID:      post.RootID,
		Message:     post.Message,
	}, nil
}

// toWSURL converts an HTTP(S) URL to its WebSocket equivalent.
func toWSURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return base + "/api/v4/websocket"
}

// Close closes the WebSocket connection.
func (w *WSClient) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.conn != nil {
		return w.conn.Close()
	}
	return nil
}
