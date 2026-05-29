package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Embedder converts text to embedding vectors.
type Embedder interface {
	// Embed returns a vector representation of the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// OllamaEmbedder uses the Ollama API to generate embeddings.
type OllamaEmbedder struct {
	baseURL string
	model   string
	client  *http.Client
}

// OllamaEmbedderConfig holds configuration for OllamaEmbedder.
type OllamaEmbedderConfig struct {
	BaseURL string
	Model   string
	Timeout time.Duration
}

// NewOllamaEmbedder creates a new Ollama-based embedder.
func NewOllamaEmbedder(cfg OllamaEmbedderConfig) *OllamaEmbedder {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OllamaEmbedder{
		baseURL: cfg.BaseURL,
		model:   cfg.Model,
		client:  &http.Client{Timeout: timeout},
	}
}

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float64 `json:"embedding"`
}

// Embed generates an embedding vector for the given text via Ollama API.
func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	reqBody := ollamaEmbedRequest{
		Model:  o.model,
		Prompt: text,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		o.baseURL+"/api/embeddings", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(body))
	}

	var embResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Convert []float64 → []float32.
	vec := make([]float32, len(embResp.Embedding))
	for i, v := range embResp.Embedding {
		vec[i] = float32(v)
	}
	return vec, nil
}
