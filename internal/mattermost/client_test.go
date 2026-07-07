package mattermost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToWSURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"https", "https://mm.example.com", "wss://mm.example.com/api/v4/websocket"},
		{"http", "http://localhost:8065", "ws://localhost:8065/api/v4/websocket"},
		{"trailing slash", "https://mm.example.com/", "wss://mm.example.com/api/v4/websocket"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toWSURL(tt.baseURL))
		})
	}
}

func TestSplitMessage(t *testing.T) {
	tests := []struct {
		name   string
		msg    string
		maxLen int
		want   []string
	}{
		{"short", "hello world", 100, []string{"hello world"}},
		{"exact length", "abcde", 5, []string{"abcde"}},
		{"two chunks", "abcdef", 3, []string{"abc", "def"}},
		{"unicode", "привет мир", 6, []string{"привет", " мир"}},
		{"empty", "", 10, []string{""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitMessage(tt.msg, tt.maxLen)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.msg, strings.Join(got, ""))
		})
	}
}

func TestRESTClientPostMessage(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v4/posts", r.URL.Path)
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_ = r.Body.Close()
		calls++

		resp := Post{ID: "post-" + body["message"], ChannelID: body["channel_id"], RootID: body["root_id"]}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "test-token", server.Client())
	id, err := client.PostMessage(context.Background(), "ch1", "hello", "root1")
	require.NoError(t, err)
	assert.Equal(t, "post-hello", id)
	assert.Equal(t, 1, calls)
}

func TestRESTClientGetMe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/me", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(User{ID: "user-1", Username: "bot", IsBot: true}))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "token", server.Client())
	u, err := client.GetMe(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "user-1", u.ID)
	assert.True(t, u.IsBot)
}

func TestRESTClientGetUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/user-1", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(User{ID: "user-1", Username: "alice", IsBot: false}))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "token", server.Client())
	u, err := client.GetUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, "user-1", u.ID)
	assert.Equal(t, "alice", u.Username)
	assert.False(t, u.IsBot)
}

func TestRESTClientGetChannel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/channels/ch1", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(Channel{ID: "ch1", Type: ChannelTypeDirect}))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "token", server.Client())
	ch, err := client.getChannel(context.Background(), "ch1")
	require.NoError(t, err)
	assert.Equal(t, ChannelTypeDirect, ch.Type)
}

func TestRESTClientPathEscape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v4/users/user%2Fpath", r.URL.EscapedPath())
		require.NoError(t, json.NewEncoder(w).Encode(User{ID: "user/path", Username: "alice"}))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "token", server.Client())
	u, err := client.GetUser(context.Background(), "user/path")
	require.NoError(t, err)
	assert.Equal(t, "user/path", u.ID)
}

func TestRESTClientErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("{\"status\": \"Unauthorized\"}"))
	}))
	defer server.Close()

	client := NewRESTClient(server.URL, "token", server.Client())
	_, err := client.GetMe(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 401")
}
