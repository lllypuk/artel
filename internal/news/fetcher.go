package news

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"log/slog"
)

// Fetcher fetches feed data from URLs.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher fetches feeds via HTTP.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string
	timeout   time.Duration
	logger    *slog.Logger
}

// NewHTTPFetcher creates a new HTTPFetcher.
func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	return &HTTPFetcher{
		client: &http.Client{
			Timeout: timeout,
		},
		userAgent: "Mozilla/5.0 (compatible; agent-shatrov/1.0; +https://github.com/lllypuk/agent-shatrov)",
		timeout:   timeout,
		logger:    slog.Default(),
	}
}

// Fetch fetches feed data from the given URL.
func (f *HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			f.logger.Warn("close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	// Limit response size to prevent memory issues
	const maxResponseSize = 10 * 1024 * 1024 // 10MB
	limited := io.LimitReader(resp.Body, maxResponseSize)
	return io.ReadAll(limited)
}
