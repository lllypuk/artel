package llm

import (
	"context"
	"errors"
	"fmt"
)

// Provider is the interface for LLM API clients. Both Client (single provider)
// and MultiClient (multi-provider with fallback) implement this interface.
type Provider interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(StreamDelta)) error
}

// APIError represents an HTTP error response from an LLM API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error %d: %s", e.StatusCode, e.Body)
}

// IsRetriable returns true if the error is likely transient and can be retried.
// Retriable errors: 5xx server errors, 429 rate limits, and network errors.
// Non-retriable: 4xx client errors (except 429), marshal/decode errors.
func IsRetriable(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500 || apiErr.StatusCode == 429
	}

	// Context cancellation is not retriable.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Non-API errors (network, DNS, TLS, etc.) are generally retriable.
	return true
}
