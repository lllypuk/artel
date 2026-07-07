package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxPostLength = 3900 // Mattermost allows ~4000 chars; leave margin.

	// maxErrBody limits how many bytes we read from a failed HTTP response so
	// that a malicious or broken server cannot cause an OOM.
	maxErrBody = 4096
	// defaultHTTPTimeout is used when the caller passes an http.Client with no
	// explicit timeout.
	defaultHTTPTimeout = 30 * time.Second
)

// RESTClient is a Mattermost REST API client.
type RESTClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewRESTClient creates a new Mattermost REST client.
func NewRESTClient(baseURL, token string, httpClient *http.Client) *RESTClient {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	if httpClient.Timeout == 0 {
		httpClient.Timeout = defaultHTTPTimeout
	}
	return &RESTClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		httpClient: httpClient,
	}
}

// doJSON makes an HTTP request, marshaling body as JSON and decoding the
// response into dst. dst may be nil to discard the response body.
func (c *RESTClient) doJSON(ctx context.Context, method, urlPath string, body, dst any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("mattermost: marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+urlPath, reqBody)
	if err != nil {
		return fmt.Errorf("mattermost: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost: request %s %s: %w", method, urlPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// Read a small prefix of the body for diagnostics, but cap it to avoid
		// OOM if the server returns a huge error payload.
		limited := io.LimitReader(resp.Body, maxErrBody)
		_, _ = io.Copy(io.Discard, limited)
		return fmt.Errorf("mattermost: %s %s returned %d", method, urlPath, resp.StatusCode)
	}

	if dst != nil {
		if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
			return fmt.Errorf("mattermost: decode response: %w", err)
		}
	}
	return nil
}

// GetMe returns the authenticated bot user.
func (c *RESTClient) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.doJSON(ctx, http.MethodGet, "/api/v4/users/me", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUser returns a Mattermost user by ID.
func (c *RESTClient) GetUser(ctx context.Context, userID string) (*User, error) {
	var u User
	path := fmt.Sprintf("/api/v4/users/%s", url.PathEscape(userID))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// getChannel returns a channel by ID, including its type field.
func (c *RESTClient) getChannel(ctx context.Context, channelID string) (*Channel, error) {
	var ch Channel
	path := fmt.Sprintf("/api/v4/channels/%s", url.PathEscape(channelID))
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// PostMessage creates a post in a channel. If rootID is non-empty it is a
// thread reply. Long messages are split into multiple posts inside the same
// thread. Returns the ID of the first created post.
func (c *RESTClient) PostMessage(ctx context.Context, channelID, message, rootID string) (string, error) {
	chunks := splitMessage(message, maxPostLength)

	var firstID string
	currentRootID := rootID
	for i, chunk := range chunks {
		body := map[string]string{
			"channel_id": channelID,
			"message":    chunk,
		}
		if currentRootID != "" {
			body["root_id"] = currentRootID
		}

		var post Post
		if err := c.doJSON(ctx, http.MethodPost, "/api/v4/posts", body, &post); err != nil {
			return firstID, fmt.Errorf("mattermost: post chunk %d: %w", i, err)
		}
		if i == 0 {
			firstID = post.ID
			if currentRootID == "" {
				// First post in a new thread — subsequent chunks reply to it.
				currentRootID = post.ID
			}
		}
	}
	return firstID, nil
}

// splitMessage splits a message into chunks of at most maxLen runes.
func splitMessage(msg string, maxLen int) []string {
	runes := []rune(msg)
	if len(runes) <= maxLen {
		return []string{msg}
	}
	var chunks []string
	for len(runes) > 0 {
		end := min(maxLen, len(runes))
		chunks = append(chunks, string(runes[:end]))
		runes = runes[end:]
	}
	return chunks
}
