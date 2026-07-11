package news_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/news"
)

// ── constructor ───────────────────────────────────────────────────────────────

func TestNewHTTPFetcher_NotNil(t *testing.T) {
	f := news.NewHTTPFetcher(5 * time.Second)
	if f == nil {
		t.Fatal("expected non-nil HTTPFetcher")
	}
}

func TestHTTPFetcher_UserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	}))
	defer srv.Close()

	f := news.NewHTTPFetcher(5 * time.Second)
	_, _ = f.Fetch(context.Background(), srv.URL, "")
	if !strings.Contains(gotUA, "agent-shatrov") {
		t.Errorf("expected 'agent-shatrov' in User-Agent, got %q", gotUA)
	}
}

func TestHTTPFetcher_AcceptHeader(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := news.NewHTTPFetcher(5 * time.Second)
	_, _ = f.Fetch(context.Background(), srv.URL, "")
	if gotAccept == "" {
		t.Error("expected non-empty Accept header")
	}
}

// ── successful fetch ──────────────────────────────────────────────────────────

func TestHTTPFetcher_Fetch_Success(t *testing.T) {
	const body = `<?xml version="1.0"?><rss version="2.0"/>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := news.NewHTTPFetcher(5 * time.Second)
	data, err := f.Fetch(context.Background(), srv.URL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != body {
		t.Errorf("expected body %q, got %q", body, string(data))
	}
}

// ── error cases ───────────────────────────────────────────────────────────────

func TestHTTPFetcher_Fetch_Non200Status(t *testing.T) {
	for _, code := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			f := news.NewHTTPFetcher(5 * time.Second)
			_, err := f.Fetch(context.Background(), srv.URL, "")
			if err == nil {
				t.Fatalf("expected error for HTTP %d", code)
			}
		})
	}
}

func TestHTTPFetcher_Fetch_StatusInError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	f := news.NewHTTPFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), srv.URL, "")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected status code in error, got: %v", err)
	}
}

func TestHTTPFetcher_Fetch_ConnectionRefused(t *testing.T) {
	f := news.NewHTTPFetcher(1 * time.Second)
	// Port 19999 should not be listening in CI.
	_, err := f.Fetch(context.Background(), "http://127.0.0.1:19999/feed", "")
	if err == nil {
		t.Fatal("expected error for refused connection")
	}
}

func TestHTTPFetcher_Fetch_InvalidProxyURL(t *testing.T) {
	f := news.NewHTTPFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), "http://example.com", "://invalid-proxy")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL")
	}
}

func TestHTTPFetcher_Fetch_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	f := news.NewHTTPFetcher(5 * time.Second)
	_, err := f.Fetch(ctx, srv.URL, "")
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// ── client reuse ──────────────────────────────────────────────────────────────

func TestHTTPFetcher_Fetch_ClientReuse(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	f := news.NewHTTPFetcher(5 * time.Second)
	for i := range 3 {
		_, err := f.Fetch(context.Background(), srv.URL, "")
		if err != nil {
			t.Fatalf("fetch %d failed: %v", i, err)
		}
	}
	if requestCount != 3 {
		t.Errorf("expected 3 requests, got %d", requestCount)
	}
}
