package news_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/news"
)

// ── NewsError.Error() ─────────────────────────────────────────────────────────

func TestNewsError_Error_WithoutInner(t *testing.T) {
	e := &news.NewsError{Code: "TEST_CODE", Message: "something failed"}
	got := e.Error()
	if got != "[TEST_CODE] something failed" {
		t.Errorf("unexpected error string: %q", got)
	}
}

func TestNewsError_Error_WithInner(t *testing.T) {
	inner := errors.New("root cause")
	e := &news.NewsError{Code: "TEST_CODE", Message: "something failed", Err: inner}
	got := e.Error()
	if !strings.Contains(got, "TEST_CODE") {
		t.Errorf("expected code in error: %q", got)
	}
	if !strings.Contains(got, "something failed") {
		t.Errorf("expected message in error: %q", got)
	}
	if !strings.Contains(got, "root cause") {
		t.Errorf("expected inner error in string: %q", got)
	}
}

// ── NewsError.Unwrap() ────────────────────────────────────────────────────────

func TestNewsError_Unwrap_ReturnsInner(t *testing.T) {
	inner := errors.New("inner")
	e := &news.NewsError{Err: inner}
	if e.Unwrap() != inner {
		t.Error("Unwrap should return the inner error")
	}
}

func TestNewsError_Unwrap_ReturnsNilWhenNoInner(t *testing.T) {
	e := &news.NewsError{}
	if e.Unwrap() != nil {
		t.Error("Unwrap should return nil when there is no inner error")
	}
}

func TestNewsError_ErrorsIs_WrappedSentinel(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := &news.NewsError{Err: sentinel}
	if !errors.Is(e, sentinel) {
		t.Error("errors.Is should find wrapped sentinel via Unwrap")
	}
}

// ── constructors ──────────────────────────────────────────────────────────────

func TestNewFetchError(t *testing.T) {
	inner := errors.New("connection refused")
	e := news.NewFetchError(inner, "https://feed.example.com/rss")
	if e.Code != news.ErrCodeFetchFailed {
		t.Errorf("expected code %q, got %q", news.ErrCodeFetchFailed, e.Code)
	}
	if !strings.Contains(e.Error(), "https://feed.example.com/rss") {
		t.Errorf("expected URL in error message: %q", e.Error())
	}
	if e.Unwrap() != inner {
		t.Error("inner error not preserved")
	}
}

func TestNewParseError(t *testing.T) {
	inner := errors.New("unexpected EOF")
	e := news.NewParseError(inner, "rss")
	if e.Code != news.ErrCodeParseFailed {
		t.Errorf("expected code %q, got %q", news.ErrCodeParseFailed, e.Code)
	}
	if !strings.Contains(e.Error(), "rss") {
		t.Errorf("expected feed type in error message: %q", e.Error())
	}
	if e.Unwrap() != inner {
		t.Error("inner error not preserved")
	}
}

func TestNewFeedNotFoundError(t *testing.T) {
	e := news.NewFeedNotFoundError(42)
	if e.Code != news.ErrCodeFeedNotFound {
		t.Errorf("expected code %q, got %q", news.ErrCodeFeedNotFound, e.Code)
	}
	if !strings.Contains(e.Error(), "42") {
		t.Errorf("expected feed ID in error: %q", e.Error())
	}
	if e.Unwrap() != nil {
		t.Error("FeedNotFound should have no inner error")
	}
}

func TestNewFeedExistsError(t *testing.T) {
	e := news.NewFeedExistsError("https://example.com/feed")
	if e.Code != news.ErrCodeFeedExists {
		t.Errorf("expected code %q, got %q", news.ErrCodeFeedExists, e.Code)
	}
	if !strings.Contains(e.Error(), "https://example.com/feed") {
		t.Errorf("expected URL in error: %q", e.Error())
	}
}

func TestNewItemNotFoundError(t *testing.T) {
	e := news.NewItemNotFoundError(99)
	if e.Code != news.ErrCodeItemNotFound {
		t.Errorf("expected code %q, got %q", news.ErrCodeItemNotFound, e.Code)
	}
	if !strings.Contains(e.Error(), "99") {
		t.Errorf("expected item ID in error: %q", e.Error())
	}
}

func TestNewInvalidURLError(t *testing.T) {
	e := news.NewInvalidURLError("not-a-url")
	if e.Code != news.ErrCodeInvalidURL {
		t.Errorf("expected code %q, got %q", news.ErrCodeInvalidURL, e.Code)
	}
	if !strings.Contains(e.Error(), "not-a-url") {
		t.Errorf("expected URL in error: %q", e.Error())
	}
}

func TestNewInvalidFeedTypeError(t *testing.T) {
	e := news.NewInvalidFeedTypeError("xml2")
	if e.Code != news.ErrCodeInvalidFeedType {
		t.Errorf("expected code %q, got %q", news.ErrCodeInvalidFeedType, e.Code)
	}
	if !strings.Contains(e.Error(), "xml2") {
		t.Errorf("expected feed type in error: %q", e.Error())
	}
}

func TestNewSubscriptionNotFoundError(t *testing.T) {
	e := news.NewSubscriptionNotFoundError(111, 222)
	if e.Code != news.ErrCodeSubscriptionNotFound {
		t.Errorf("expected code %q, got %q", news.ErrCodeSubscriptionNotFound, e.Code)
	}
	got := e.Error()
	if !strings.Contains(got, "111") || !strings.Contains(got, "222") {
		t.Errorf("expected chatID and feedID in error: %q", got)
	}
}

// ── error code constants are distinct ─────────────────────────────────────────

func TestErrorCodes_AreDistinct(t *testing.T) {
	codes := []string{
		news.ErrCodeFetchFailed,
		news.ErrCodeParseFailed,
		news.ErrCodeFeedNotFound,
		news.ErrCodeFeedExists,
		news.ErrCodeItemNotFound,
		news.ErrCodeInvalidURL,
		news.ErrCodeInvalidFeedType,
		news.ErrCodeSubscriptionNotFound,
		news.ErrCodeDatabase,
	}
	seen := make(map[string]bool)
	for _, c := range codes {
		if seen[c] {
			t.Errorf("duplicate error code: %q", c)
		}
		seen[c] = true
		if c == "" {
			t.Error("empty error code")
		}
	}
}
