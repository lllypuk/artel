package mattermost

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lllypuk/agent-shatrov/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractMentions(t *testing.T) {
	assert.Nil(t, extractMentions("hello"))
	assert.Equal(t, []string{"alice", "bob"}, extractMentions("hi @alice and @bob"))
	assert.Equal(t, []string{"dev_1"}, extractMentions("@dev_1 ping"))
}

func TestTransportEndToEnd(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	var mu sync.Mutex
	var posts []map[string]string
	wsConnected := make(chan struct{})
	upgradeCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/websocket":
			conn, err := upgrader.Upgrade(w, r, nil)
			require.NoError(t, err)
			upgradeCh <- conn

			var auth wsAuthChallenge
			require.NoError(t, conn.ReadJSON(&auth))
			require.NoError(t, conn.WriteJSON(map[string]any{"status": "OK", "seq_reply": 1}))
			close(wsConnected)

			postJSON, _ := json.Marshal(Post{ID: "p1", ChannelID: "c1", RootID: "r1", UserID: "u1", Message: "hello @bot"})
			event := wsEvent{
				Event: "posted",
				Seq:   1,
				Data: json.RawMessage(func() []byte {
					b, _ := json.Marshal(map[string]string{
						"post":         string(postJSON),
						"channel_type": ChannelTypeDirect,
						"sender_name":  "user",
					})
					return b
				}()),
			}
			require.NoError(t, conn.WriteJSON(event))

			ctx := r.Context()
			<-ctx.Done()
			return
		case "/api/v4/users/me":
			require.NoError(t, json.NewEncoder(w).Encode(User{ID: "self", Username: "bot", IsBot: true}))
		case "/api/v4/posts":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_ = r.Body.Close()
			mu.Lock()
			posts = append(posts, body)
			mu.Unlock()
			require.NoError(t, json.NewEncoder(w).Encode(Post{ID: "reply", ChannelID: body["channel_id"], RootID: body["root_id"]}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	tr, err := NewTransport(server.URL, "token", "", logger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_ = tr.Run(ctx)
	}()

	select {
	case <-wsConnected:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket did not connect")
	}

	// Read inbound message.
	var msg transport.InboundMessage
	select {
	case msg = <-tr.Messages():
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive inbound message")
	}

	assert.Equal(t, "c1", msg.ChatID)
	assert.Equal(t, "r1", msg.ThreadID)
	assert.Equal(t, "u1", msg.SenderID)
	assert.Equal(t, "user", msg.SenderName)
	assert.True(t, msg.IsDM)
	assert.Equal(t, "hello @bot", msg.Text)
	assert.Equal(t, []string{"bot"}, msg.Mentions)

	// Send outbound message.
	require.NoError(t, tr.Send(ctx, transport.Message{TargetID: "c1", Text: "hi back", ThreadID: "r1"}))
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(posts) > 0
	}, 2*time.Second, 50*time.Millisecond)

	mu.Lock()
	require.Len(t, posts, 1)
	assert.Equal(t, "c1", posts[0]["channel_id"])
	assert.Equal(t, "r1", posts[0]["root_id"])
	assert.Equal(t, "hi back", posts[0]["message"])
	mu.Unlock()

	cancel()
	conn := <-upgradeCh
	_ = conn.Close()
	_ = tr.Close()
}

func TestTransportSendRequiresTarget(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tr, err := NewTransport("http://localhost:9999", "token", "", logger)
	require.NoError(t, err)

	err = tr.Send(context.Background(), transport.Message{Text: "hello"})
	assert.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "target channel id is required"))
}
