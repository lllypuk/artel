package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/config"
)

// newTestScheduler creates a scheduler suitable for unit tests (no agent/send/users).
func newTestScheduler(cfg config.ProactiveConfig) *proactiveScheduler {
	s := newProactiveScheduler(cfg, nil, nil, nil, slog.Default())
	s.today = time.Now().Format("2006-01-02")
	return s
}

// scheduledNow returns a time.Time that is exactly at the given HH:MM today (local).
func scheduledNow(hhmm string) time.Time {
	now := time.Now()
	t, _ := parseScheduledTime(hhmm, now, time.Local)
	return t
}

// ── Defaults ─────────────────────────────────────────────────────────────────

func TestDefaults(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{})
	if s.cfg.MaxMessagesPerDay != 7 {
		t.Errorf("MaxMessagesPerDay: got %d, want 7", s.cfg.MaxMessagesPerDay)
	}
	if s.cfg.GoalReminderDaysIdle != 3 {
		t.Errorf("GoalReminderDaysIdle: got %d, want 3", s.cfg.GoalReminderDaysIdle)
	}
}

func TestDefaultsNotOverridden(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 5, GoalReminderDaysIdle: 7})
	if s.cfg.MaxMessagesPerDay != 5 {
		t.Errorf("MaxMessagesPerDay: got %d, want 5", s.cfg.MaxMessagesPerDay)
	}
	if s.cfg.GoalReminderDaysIdle != 7 {
		t.Errorf("GoalReminderDaysIdle: got %d, want 7", s.cfg.GoalReminderDaysIdle)
	}
}

// ── maybeFireEvent ────────────────────────────────────────────────────────────

// TestMaybeFireEvent_Fires: handler is called when now is exactly at scheduled time.
func TestMaybeFireEvent_Fires(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 7})

	now := scheduledNow("08:00")
	done := make(chan struct{})
	s.maybeFireEvent(context.Background(), "test", "08:00", now, func(_ context.Context) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler was not called within 1s")
	}
}

// TestMaybeFireEvent_WithinWindow: fires if now is 2 min after scheduled (within eventWindow).
func TestMaybeFireEvent_WithinWindow(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 7})

	base := scheduledNow("08:00")
	now := base.Add(2 * time.Minute) // within 3-min window

	done := make(chan struct{})
	s.maybeFireEvent(context.Background(), "test", "08:00", now, func(_ context.Context) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler should fire within window")
	}
}

// TestMaybeFireEvent_OutsideWindow: does NOT fire if now is 4 min after scheduled.
func TestMaybeFireEvent_OutsideWindow(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 7})

	base := scheduledNow("08:00")
	now := base.Add(4 * time.Minute) // outside 3-min window

	var called int32
	s.maybeFireEvent(context.Background(), "test", "08:00", now, func(_ context.Context) {
		atomic.StoreInt32(&called, 1)
	})

	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&called) != 0 {
		t.Fatal("handler should not fire outside window")
	}
}

// TestMaybeFireEvent_BeforeScheduled: does NOT fire if now is before scheduled time.
func TestMaybeFireEvent_BeforeScheduled(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 7})

	base := scheduledNow("08:00")
	now := base.Add(-1 * time.Minute) // before scheduled

	var called int32
	s.maybeFireEvent(context.Background(), "test", "08:00", now, func(_ context.Context) {
		atomic.StoreInt32(&called, 1)
	})

	time.Sleep(20 * time.Millisecond)
	if atomic.LoadInt32(&called) != 0 {
		t.Fatal("handler should not fire before scheduled time")
	}
}

// TestMaybeFireEvent_NoDoubleFire: same event fires only once per day.
func TestMaybeFireEvent_NoDoubleFire(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 7})
	now := scheduledNow("08:00")

	var count int32
	done := make(chan struct{})
	s.maybeFireEvent(context.Background(), "briefing", "08:00", now, func(_ context.Context) {
		atomic.AddInt32(&count, 1)
		close(done)
	})
	<-done

	// Second call must be suppressed by fired-map.
	s.maybeFireEvent(context.Background(), "briefing", "08:00", now, func(_ context.Context) {
		atomic.AddInt32(&count, 1)
	})
	time.Sleep(20 * time.Millisecond)

	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("event fired %d times, want exactly 1", got)
	}
}

// ── canSendToUser / per-user limit ────────────────────────────────────────────

func TestCanSendToUser_AllowsUpToLimit(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 2})

	if !s.canSendToUser(1) {
		t.Error("first send should be allowed")
	}
	if !s.canSendToUser(1) {
		t.Error("second send should be allowed")
	}
	if s.canSendToUser(1) {
		t.Error("third send should be blocked (limit=2)")
	}
}

func TestCanSendToUser_PerUserIsolation(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 1})

	if !s.canSendToUser(101) {
		t.Error("user 101: first send should be allowed")
	}
	if s.canSendToUser(101) {
		t.Error("user 101: second send should be blocked")
	}
	// Different user has independent counter.
	if !s.canSendToUser(202) {
		t.Error("user 202: should have independent limit")
	}
}

// ── Day reset ─────────────────────────────────────────────────────────────────

func TestDayReset(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{MaxMessagesPerDay: 1})

	// Use up the single daily slot for user 1.
	if !s.canSendToUser(1) {
		t.Fatal("first send should be allowed")
	}
	if s.canSendToUser(1) {
		t.Fatal("second send should be blocked")
	}

	// Simulate a new day by forcing today to an old value so tick() resets.
	s.mu.Lock()
	s.today = "2000-01-01"
	s.mu.Unlock()

	s.tick(context.Background())

	// After reset the slot should be available again.
	if !s.canSendToUser(1) {
		t.Error("after day reset, send should be allowed again")
	}
}

// ── Timezone and helper functions ─────────────────────────────────────────────

func TestInvalidTimezone(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{Timezone: "Invalid/Zone"},
		nil, nil, nil, slog.Default(),
	)
	if s.loc == nil {
		t.Fatal("loc must not be nil after invalid timezone")
	}
}

func TestParseScheduledTime_Valid(t *testing.T) {
	now := time.Date(2026, 4, 18, 10, 30, 0, 0, time.UTC)
	got, err := parseScheduledTime("08:00", now, time.UTC)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Hour() != 8 || got.Minute() != 0 {
		t.Errorf("got %v, want 08:00", got)
	}
}

func TestParseScheduledTime_Invalid(t *testing.T) {
	now := time.Now()
	cases := []string{"bad", "25:00", "08:60", ""}
	for _, c := range cases {
		_, err := parseScheduledTime(c, now, time.UTC)
		if err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestOrDisabled(t *testing.T) {
	if orDisabled("") != "disabled" {
		t.Error("empty string should return 'disabled'")
	}
	if orDisabled("08:00") != "08:00" {
		t.Error("non-empty string should be returned as-is")
	}
}
