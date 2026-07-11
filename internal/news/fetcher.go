package news

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"log/slog"
)

// Fetcher fetches feed data from URLs.
type Fetcher interface {
	// Fetch downloads rawURL using proxyURL (empty = no proxy).
	Fetch(ctx context.Context, rawURL, proxyURL string) ([]byte, error)
}

// HTTPFetcher fetches feeds via HTTP.
// It keeps a pool of http.Client instances keyed by proxyURL so that
// connections are reused across requests to the same proxy.
type HTTPFetcher struct {
	timeout   time.Duration
	userAgent string
	logger    *slog.Logger

	mu      sync.Mutex
	clients map[string]*http.Client // key: proxyURL (empty = direct)
}

// NewHTTPFetcher creates a new HTTPFetcher.
func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
	return &HTTPFetcher{
		timeout:   timeout,
		userAgent: "Mozilla/5.0 (compatible; agent-shatrov/1.0; +https://github.com/lllypuk/artel)",
		logger:    slog.Default(),
		clients:   make(map[string]*http.Client),
	}
}

// clientFor returns (or lazily creates) an http.Client for the given proxyURL.
func (f *HTTPFetcher) clientFor(proxyURL string) (*http.Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if c, ok := f.clients[proxyURL]; ok {
		return c, nil
	}

	transport := &http.Transport{}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy url %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(parsed)
	}

	c := &http.Client{
		Timeout:   f.timeout,
		Transport: transport,
	}
	f.clients[proxyURL] = c
	return c, nil
}

// Fetch fetches feed data from rawURL, optionally routing through proxyURL.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL, proxyURL string) ([]byte, error) {
	client, err := f.clientFor(proxyURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json")

	resp, err := client.Do(req)
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
