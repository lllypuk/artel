package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/config"
	"github.com/lllypuk/artel/internal/storage"
	"github.com/lllypuk/artel/internal/tools"
)

func openMonitorDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ─── computeDownSince ─────────────────────────────────────────────────────────

func TestComputeDownSince_SiteUp(t *testing.T) {
	result := computeDownSince(true, time.Now(), nil)
	if result != nil {
		t.Errorf("expected nil for up site, got %v", result)
	}
}

func TestComputeDownSince_FirstDown(t *testing.T) {
	now := time.Now()
	result := computeDownSince(false, now, nil)
	if result == nil {
		t.Fatal("expected non-nil for first down")
	}
	if !result.Equal(now) {
		t.Errorf("want %v, got %v", now, result)
	}
}

func TestComputeDownSince_StaysDown(t *testing.T) {
	original := time.Now().Add(-5 * time.Minute)
	prev := &storage.MonitorState{IsUp: false, DownSinceAt: &original}
	result := computeDownSince(false, time.Now(), prev)
	if result == nil || !result.Equal(original) {
		t.Errorf("expected original time %v, got %v", original, result)
	}
}

func TestComputeDownSince_TransitionToDown(t *testing.T) {
	now := time.Now()
	prev := &storage.MonitorState{IsUp: true, DownSinceAt: nil}
	result := computeDownSince(false, now, prev)
	if result == nil || !result.Equal(now) {
		t.Errorf("expected now %v, got %v", now, result)
	}
}

// ─── buildAlertMessage ────────────────────────────────────────────────────────

func TestBuildAlertMessage_SiteDown(t *testing.T) {
	site := config.MonitorSite{URL: "https://example.com", Name: "Example"}
	result := storage.MonitorCheck{
		SiteURL:   site.URL,
		IsUp:      false,
		ErrorText: "connection refused",
	}
	msg := buildAlertMessage(site, result, nil)
	if !strings.Contains(msg, "🔴") {
		t.Errorf("expected 🔴: %q", msg)
	}
	if !strings.Contains(msg, "Example") {
		t.Errorf("expected site name: %q", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("expected error text: %q", msg)
	}
}

func TestBuildAlertMessage_SiteDownHTTP500(t *testing.T) {
	site := config.MonitorSite{URL: "https://example.com", Name: "Example"}
	result := storage.MonitorCheck{
		IsUp:       false,
		StatusCode: 503,
		ErrorText:  "HTTP 503",
	}
	msg := buildAlertMessage(site, result, nil)
	if !strings.Contains(msg, "503") {
		t.Errorf("expected 503: %q", msg)
	}
}

func TestBuildAlertMessage_SiteUp_NoDowntime(t *testing.T) {
	site := config.MonitorSite{URL: "https://example.com", Name: "Example"}
	result := storage.MonitorCheck{
		IsUp:       true,
		StatusCode: 200,
		ResponseMS: 42,
	}
	msg := buildAlertMessage(site, result, nil)
	if !strings.Contains(msg, "✅") {
		t.Errorf("expected ✅: %q", msg)
	}
	if !strings.Contains(msg, "200") {
		t.Errorf("expected HTTP 200: %q", msg)
	}
	if strings.Contains(msg, "Недоступен был") {
		t.Errorf("unexpected downtime message when prev==nil: %q", msg)
	}
}

func TestBuildAlertMessage_SiteUp_WithDowntime(t *testing.T) {
	site := config.MonitorSite{URL: "https://example.com", Name: "Example"}
	downSince := time.Now().Add(-30 * time.Minute)
	checkedAt := time.Now()
	result := storage.MonitorCheck{
		IsUp:       true,
		StatusCode: 200,
		CheckedAt:  checkedAt,
	}
	prev := &storage.MonitorState{IsUp: false, DownSinceAt: &downSince}
	msg := buildAlertMessage(site, result, prev)
	if !strings.Contains(msg, "Недоступен был") {
		t.Errorf("expected downtime duration: %q", msg)
	}
}

// ─── sendMonitorAlert ─────────────────────────────────────────────────────────

func TestSendMonitorAlert_AllUsers(t *testing.T) {
	var received []int64
	notifier := tools.Notifier(func(chatID int64, _ string) {
		received = append(received, chatID)
	})

	sendMonitorAlert(notifier, []int64{100, 200, 300}, "test alert")

	if len(received) != 3 {
		t.Fatalf("expected 3 alerts, got %d", len(received))
	}
	for i, want := range []int64{100, 200, 300} {
		if received[i] != want {
			t.Errorf("alert[%d]: got %d, want %d", i, received[i], want)
		}
	}
}

func TestSendMonitorAlert_NoUsers(t *testing.T) {
	var count int
	notifier := tools.Notifier(func(_ int64, _ string) { count++ })
	sendMonitorAlert(notifier, nil, "msg")
	if count != 0 {
		t.Errorf("expected 0 alerts for empty user list, got %d", count)
	}
}

// ─── checkOneSite: no alert on first run ─────────────────────────────────────

func TestCheckOneSite_NoAlertOnFirstRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := openMonitorDB(t)
	alertCount := 0
	notifier := tools.Notifier(func(_ int64, _ string) { alertCount++ })

	site := config.MonitorSite{URL: srv.URL, Name: "Test"}
	client := &http.Client{Timeout: 5 * time.Second}
	checkOneSite(context.Background(), site, db, notifier, []int64{1}, client, slog.Default())

	if alertCount != 0 {
		t.Errorf("expected 0 alerts on first run (no prev state), got %d", alertCount)
	}
}

// ─── checkOneSite: alert on state change ─────────────────────────────────────

func TestCheckOneSite_AlertOnTransitionToDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	db := openMonitorDB(t)
	ctx := context.Background()

	// Seed state: site was up
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{
		SiteURL:       srv.URL,
		IsUp:          true,
		LastCheckedAt: time.Now().UTC(),
	})

	var messages []string
	notifier := tools.Notifier(func(_ int64, msg string) { messages = append(messages, msg) })

	client := &http.Client{Timeout: 5 * time.Second}
	checkOneSite(ctx, config.MonitorSite{URL: srv.URL, Name: "Test"}, db, notifier, []int64{1}, client, slog.Default())

	if len(messages) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(messages))
	}
	if !strings.Contains(messages[0], "🔴") {
		t.Errorf("expected 🔴 in alert: %q", messages[0])
	}
}

func TestCheckOneSite_AlertOnRecovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := openMonitorDB(t)
	ctx := context.Background()

	downSince := time.Now().Add(-10 * time.Minute)
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{
		SiteURL:       srv.URL,
		IsUp:          false,
		LastCheckedAt: downSince,
		DownSinceAt:   &downSince,
	})

	var messages []string
	notifier := tools.Notifier(func(_ int64, msg string) { messages = append(messages, msg) })

	client := &http.Client{Timeout: 5 * time.Second}
	checkOneSite(ctx, config.MonitorSite{URL: srv.URL, Name: "Test"}, db, notifier, []int64{1}, client, slog.Default())

	if len(messages) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(messages))
	}
	if !strings.Contains(messages[0], "✅") {
		t.Errorf("expected ✅ in alert: %q", messages[0])
	}
	if !strings.Contains(messages[0], "Недоступен был") {
		t.Errorf("expected downtime in alert: %q", messages[0])
	}
}

func TestCheckOneSite_NoAlertWhenStateUnchanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := openMonitorDB(t)
	ctx := context.Background()

	// Seed: site already up
	_ = db.UpsertMonitorState(ctx, storage.MonitorState{
		SiteURL:       srv.URL,
		IsUp:          true,
		LastCheckedAt: time.Now().UTC(),
	})

	alertCount := 0
	notifier := tools.Notifier(func(_ int64, _ string) { alertCount++ })

	client := &http.Client{Timeout: 5 * time.Second}
	checkOneSite(ctx, config.MonitorSite{URL: srv.URL}, db, notifier, []int64{1}, client, slog.Default())

	if alertCount != 0 {
		t.Errorf("expected no alert when state unchanged, got %d", alertCount)
	}
}

// ─── pingMonitorSite ──────────────────────────────────────────────────────────

func TestPingMonitorSite_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	site := config.MonitorSite{URL: srv.URL, Name: "Test"}
	result := pingMonitorSite(context.Background(), &http.Client{Timeout: 5 * time.Second}, site)

	if !result.IsUp {
		t.Errorf("expected IsUp=true for HTTP 200")
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode: got %d, want 200", result.StatusCode)
	}
	if result.ResponseMS < 0 {
		t.Errorf("ResponseMS should be >= 0, got %d", result.ResponseMS)
	}
}

func TestPingMonitorSite_Down500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	site := config.MonitorSite{URL: srv.URL, Name: "Test"}
	result := pingMonitorSite(context.Background(), &http.Client{Timeout: 5 * time.Second}, site)

	if result.IsUp {
		t.Error("expected IsUp=false for HTTP 500")
	}
	if result.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode: got %d, want 500", result.StatusCode)
	}
}

func TestPingMonitorSite_InvalidURL(t *testing.T) {
	site := config.MonitorSite{URL: "://bad-url", Name: "Bad"}
	result := pingMonitorSite(context.Background(), &http.Client{Timeout: time.Second}, site)

	if result.IsUp {
		t.Error("expected IsUp=false for invalid URL")
	}
	if result.ErrorText == "" {
		t.Error("expected ErrorText for invalid URL")
	}
}
