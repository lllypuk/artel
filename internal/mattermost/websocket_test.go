package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePostedEvent(t *testing.T) {
	makeData := func(post Post, channelType string) json.RawMessage {
		postJSON, _ := json.Marshal(post)
		data, _ := json.Marshal(map[string]string{
			"post":         string(postJSON),
			"channel_type": channelType,
			"sender_name":  "alice",
		})
		return data
	}

	ev, err := parsePostedEvent(makeData(Post{
		ID:        "post1",
		ChannelID: "ch1",
		RootID:    "root1",
		UserID:    "user1",
		Message:   "hello @bob",
	}, ChannelTypePublic))
	require.NoError(t, err)
	assert.Equal(t, "post1", ev.PostID)
	assert.Equal(t, "ch1", ev.ChannelID)
	assert.Equal(t, ChannelTypePublic, ev.ChannelType)
	assert.Equal(t, "root1", ev.RootID)
	assert.Equal(t, "user1", ev.UserID)
	assert.Equal(t, "alice", ev.Username)
	assert.Equal(t, "hello @bob", ev.Message)
}

func TestWSClientConnect(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	connected := make(chan struct{})
	var mu sync.Mutex
	var authOK bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/websocket" {
			conn, err := upgrader.Upgrade(w, r, nil)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()

			// Read auth challenge.
			var auth wsAuthChallenge
			require.NoError(t, conn.ReadJSON(&auth))
			assert.Equal(t, "authentication_challenge", auth.Action)
			require.NoError(t, conn.WriteJSON(map[string]any{"status": "OK", "seq_reply": 1}))

			mu.Lock()
			authOK = true
			mu.Unlock()
			close(connected)

			// Keep open until context ends.
			ctx := r.Context()
			<-ctx.Done()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ws := NewWSClient(server.URL, "token", nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	require.NoError(t, ws.Connect(ctx))

	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("websocket did not connect")
	}

	mu.Lock()
	assert.True(t, authOK)
	mu.Unlock()
	_ = ws.Close()
}

func TestWSClientReceivesPostedEvent(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	upgradeCh := make(chan *websocket.Conn, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/websocket" {
			conn, err := upgrader.Upgrade(w, r, nil)
			require.NoError(t, err)
			upgradeCh <- conn

			var auth wsAuthChallenge
			require.NoError(t, conn.ReadJSON(&auth))
			require.NoError(t, conn.WriteJSON(map[string]any{"status": "OK", "seq_reply": 1}))

			// Send posted event.
			postJSON, _ := json.Marshal(Post{ID: "p1", ChannelID: "c1", UserID: "u1", RootID: "", Message: "hi @dev"})
			event := wsEvent{
				Event: "posted",
				Seq:   1,
				Data: json.RawMessage(func() []byte {
					b, _ := json.Marshal(map[string]string{
						"post":         string(postJSON),
						"channel_type": ChannelTypePublic,
						"sender_name":  "alice",
					})
					return b
				}()),
			}
			require.NoError(t, conn.WriteJSON(event))

			ctx := r.Context()
			<-ctx.Done()
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ws := NewWSClient(server.URL, "token", nil, nil)
	var received PostedEvent
	ws.OnPosted(func(ev PostedEvent) { received = ev })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, ws.Connect(ctx))

	require.Eventually(t, func() bool { return received.PostID != "" }, 2*time.Second, 50*time.Millisecond)
	assert.Equal(t, "p1", received.PostID)
	assert.Equal(t, "c1", received.ChannelID)
	assert.Equal(t, "u1", received.UserID)
	assert.Equal(t, "hi @dev", received.Message)
	assert.Equal(t, "alice", received.Username)

	conn := <-upgradeCh
	_ = conn.Close()
	_ = ws.Close()
}
