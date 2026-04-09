package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ContentPart represents one element of a multi-modal content array.
type ContentPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *ImageURLContent `json:"image_url,omitempty"`
}

// ImageURLContent is the image reference within a ContentPart.
type ImageURLContent struct {
	URL string `json:"url"`
}

// Message represents a chat message.
// Content holds plain-text content; ContentParts holds multi-modal content
// (e.g. text + images). When ContentParts is non-empty it takes precedence
// during JSON serialisation.
type Message struct {
	Role         string        `json:"-"`
	Content      string        `json:"-"`
	ContentParts []ContentPart `json:"-"`
	ToolCalls    []ToolCall    `json:"-"`
	ToolCallID   string        `json:"-"`
}

// msgWire is the flat wire representation used for JSON marshal/unmarshal.
type msgWire struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// MarshalJSON serialises the message. When ContentParts is set the content
// field is an array (vision / multi-modal); otherwise it is a plain string.
func (m Message) MarshalJSON() ([]byte, error) {
	w := msgWire{
		Role:       m.Role,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	}
	if len(m.ContentParts) > 0 {
		b, err := json.Marshal(m.ContentParts)
		if err != nil {
			return nil, err
		}
		w.Content = b
	} else if m.Content != "" {
		b, err := json.Marshal(m.Content)
		if err != nil {
			return nil, err
		}
		w.Content = b
	}
	return json.Marshal(w)
}

// UnmarshalJSON deserialises the message. The content field from API responses
// is always a plain string; ContentParts is preserved for round-trip use.
func (m *Message) UnmarshalJSON(data []byte) error {
	var w msgWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	if len(w.Content) > 0 && string(w.Content) != "null" {
		// Try plain string first (normal API response).
		var s string
		if err := json.Unmarshal(w.Content, &s); err == nil {
			m.Content = s
		} else {
			// Fall back to array of content parts (unlikely from server, but safe).
			var parts []ContentPart
			if err := json.Unmarshal(w.Content, &parts); err == nil {
				m.ContentParts = parts
			}
		}
	}
	return nil
}

// ToolCall represents a function call from the LLM.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall contains the function name and arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool available to the LLM.
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef describes the function schema.
type ToolFuncDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ChatRequest is the request payload for chat completions.
type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

// ChatResponse is the response from chat completions.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents one completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamDelta is called for each streaming chunk.
type StreamDelta struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Usage     *Usage
}

// Client is the LLM API client.
type Client struct {
	baseURL      string
	apiKey       string
	httpClient   *http.Client
	extraHeaders map[string]string
}

// ClientOption configures optional Client parameters.
type ClientOption func(*Client, *http.Transport)

// WithExtraHeaders sets additional HTTP headers sent with every request.
// Useful for OpenRouter (HTTP-Referer, X-Title) and other providers.
func WithExtraHeaders(headers map[string]string) ClientOption {
	return func(c *Client, _ *http.Transport) {
		c.extraHeaders = headers
	}
}

// WithTimeout overrides the default HTTP client timeout (120s).
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client, _ *http.Transport) {
		c.httpClient.Timeout = d
	}
}

// WithProxy sets an HTTP proxy for the client.
func WithProxy(proxyURL string) ClientOption {
	return func(_ *Client, tr *http.Transport) {
		if proxyURL == "" {
			return
		}
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			tr.Proxy = http.ProxyURL(parsed)
		}
	}
}

// NewClient creates a new LLM client with optional configuration.
func NewClient(baseURL, apiKey string, opts ...ClientOption) *Client {
	tr := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}

	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout:   120 * time.Second,
			Transport: tr,
		},
	}

	for _, opt := range opts {
		opt(c, tr)
	}

	return c
}

// Chat sends a non-streaming chat completion request.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, v := range c.extraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &chatResp, nil
}

// ChatStream sends a streaming chat completion request.
func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onDelta func(StreamDelta)) error {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	for k, v := range c.extraHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			onDelta(StreamDelta{Done: true})
			return nil
		}

		var chunk ChatResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 {
			delta := StreamDelta{
				Content:   chunk.Choices[0].Delta.Content,
				ToolCalls: chunk.Choices[0].Delta.ToolCalls,
			}
			if chunk.Usage.TotalTokens > 0 {
				delta.Usage = &chunk.Usage
			}
			onDelta(delta)
		}
	}

	return scanner.Err()
}
