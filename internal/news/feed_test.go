package news_test

import (
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/news"
)

// ── FetchIntervalSeconds ──────────────────────────────────────────────────────

func TestFeed_FetchIntervalSeconds(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     int64
	}{
		{"one hour", time.Hour, 3600},
		{"30 minutes", 30 * time.Minute, 1800},
		{"zero", 0, 0},
		{"one second", time.Second, 1},
		{"90 seconds", 90 * time.Second, 90},
		{"24 hours", 24 * time.Hour, 86400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &news.Feed{FetchInterval: tt.interval}
			if got := f.FetchIntervalSeconds(); got != tt.want {
				t.Errorf("FetchIntervalSeconds() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ── SetFetchIntervalFromSeconds ───────────────────────────────────────────────

func TestFeed_SetFetchIntervalFromSeconds(t *testing.T) {
	tests := []struct {
		name    string
		seconds int64
		want    time.Duration
	}{
		{"one hour", 3600, time.Hour},
		{"30 minutes", 1800, 30 * time.Minute},
		{"zero", 0, 0},
		{"one second", 1, time.Second},
		{"one day", 86400, 24 * time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &news.Feed{}
			f.SetFetchIntervalFromSeconds(tt.seconds)
			if f.FetchInterval != tt.want {
				t.Errorf("FetchInterval = %v, want %v", f.FetchInterval, tt.want)
			}
		})
	}
}

// ── round-trip ────────────────────────────────────────────────────────────────

func TestFeed_FetchInterval_RoundTrip(t *testing.T) {
	durations := []time.Duration{
		time.Hour,
		30 * time.Minute,
		24 * time.Hour,
		7 * 24 * time.Hour,
		5*time.Minute + 30*time.Second,
	}
	for _, d := range durations {
		f := &news.Feed{FetchInterval: d}
		secs := f.FetchIntervalSeconds()
		f2 := &news.Feed{}
		f2.SetFetchIntervalFromSeconds(secs)
		if f2.FetchInterval != d {
			t.Errorf("round-trip failed for %v: got %v", d, f2.FetchInterval)
		}
	}
}

// ── FeedType constants ────────────────────────────────────────────────────────

func TestFeedType_Constants(t *testing.T) {
	if news.FeedTypeRSS == "" {
		t.Error("FeedTypeRSS must be non-empty")
	}
	if news.FeedTypeAtom == "" {
		t.Error("FeedTypeAtom must be non-empty")
	}
	if news.FeedTypeJSON == "" {
		t.Error("FeedTypeJSON must be non-empty")
	}
	if news.FeedTypeRSS == news.FeedTypeAtom || news.FeedTypeAtom == news.FeedTypeJSON || news.FeedTypeRSS == news.FeedTypeJSON {
		t.Error("FeedType constants must be distinct")
	}
}
