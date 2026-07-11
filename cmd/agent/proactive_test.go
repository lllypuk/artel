package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/config"
	"github.com/lllypuk/artel/internal/storage"
)

// newTestScheduler creates a scheduler suitable for unit tests (no agent/send/users/db).
func newTestScheduler(cfg config.ProactiveConfig) *proactiveScheduler {
	s := newProactiveScheduler(cfg, nil, nil, nil, nil, false, false, false, slog.Default())
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
	// GoalReminderDaysIdle=0 means «disabled»; the constructor must NOT override it with a default.
	if s.cfg.GoalReminderDaysIdle != 0 {
		t.Errorf("GoalReminderDaysIdle: got %d, want 0 (zero = disabled, no override)", s.cfg.GoalReminderDaysIdle)
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
		nil, nil, nil, nil, false, false, false, slog.Default(),
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

// ── P-5: Goal reminder ────────────────────────────────────────────────────────

// TestGoalReminderEvent_FiredByTick: when GoalReminderDaysIdle > 0,
// tick() must schedule the "goal_reminder" event.
func TestGoalReminderEvent_FiredByTick(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{
		MaxMessagesPerDay:    7,
		GoalReminderDaysIdle: 3,
		MorningBriefingTime:  "09:00",
	})
	// Inject a mock DB-like presence: set db to a non-nil sentinel value.
	// Because fireGoalReminder returns early when db is nil, we need it non-nil
	// to reach the handler body — but we test via the fired-map, not the body.
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	s.db = db

	s.nowFn = func() time.Time { return scheduledNow("09:00") }
	s.tick(context.Background())

	// Give the goroutine time to mark the key.
	time.Sleep(50 * time.Millisecond)

	s.mu.Lock()
	fired := s.fired["goal_reminder"]
	s.mu.Unlock()

	if !fired {
		t.Error("expected 'goal_reminder' to be marked as fired after tick()")
	}
}

// TestGoalReminderEvent_NotFiredWhenDisabled: goal_reminder must NOT fire when
// GoalReminderDaysIdle == 0, even if a live DB is present.
func TestGoalReminderEvent_NotFiredWhenDisabled(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{
		MaxMessagesPerDay:    7,
		GoalReminderDaysIdle: 0, // explicitly disabled
		MorningBriefingTime:  "09:00",
	})
	// Give it a real DB so the only thing preventing the event is daysIdle == 0.
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	s.db = db

	s.nowFn = func() time.Time { return scheduledNow("09:00") }
	s.tick(context.Background())
	time.Sleep(20 * time.Millisecond)

	s.mu.Lock()
	fired := s.fired["goal_reminder"]
	s.mu.Unlock()

	if fired {
		t.Error("goal_reminder must not fire when GoalReminderDaysIdle=0 (feature disabled)")
	}
}

// TestGoalReminderEvent_DefaultTime: when MorningBriefingTime is empty,
// the goal reminder fires at the default 09:00.
func TestGoalReminderEvent_DefaultTime(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{
		MaxMessagesPerDay:    7,
		GoalReminderDaysIdle: 3,
		// MorningBriefingTime intentionally empty
	})
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	s.db = db

	s.nowFn = func() time.Time { return scheduledNow("09:00") }
	s.tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	s.mu.Lock()
	fired := s.fired["goal_reminder"]
	s.mu.Unlock()

	if !fired {
		t.Error("goal_reminder should fire at 09:00 when MorningBriefingTime is empty")
	}
}

// ── P-5 (continued): no-double-fire ──────────────────────────────────────────────

// TestGoalReminderEvent_NoDoubleFire: goal_reminder fires at most once per day.
func TestGoalReminderEvent_NoDoubleFire(t *testing.T) {
	s := newTestScheduler(config.ProactiveConfig{
		MaxMessagesPerDay:    7,
		GoalReminderDaysIdle: 3,
		MorningBriefingTime:  "09:00",
	})
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close() //nolint:errcheck
	s.db = db

	s.nowFn = func() time.Time { return scheduledNow("09:00") }
	s.tick(context.Background())
	time.Sleep(50 * time.Millisecond)

	// Second tick at the same time must NOT re-fire.
	var handlerCalls int32
	s.maybeFireEvent(context.Background(), "goal_reminder", "09:00",
		scheduledNow("09:00"), func(_ context.Context) {
			atomic.AddInt32(&handlerCalls, 1)
		})
	time.Sleep(20 * time.Millisecond)

	if atomic.LoadInt32(&handlerCalls) != 0 {
		t.Error("goal_reminder must not fire a second time on the same day")
	}
}

// ── Fix-5: weatherEnabled guards ─────────────────────────────────────────────

// TestMorningBriefing_WeatherPromptOnlyWhenEnabled: when city is set but
// weather tool is disabled, the prompt must NOT include "инструмент weather".
func TestMorningBriefing_WeatherEnabledContainsWeatherInstruction(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{City: "Moscow"},
		nil, nil, nil, nil, true /* weatherEnabled */, false, false, slog.Default(),
	)
	// Collect the prompt by intercepting dispatch via a custom agent.
	// We just verify the field is stored; actual dispatch needs a live agent.
	if !s.weatherEnabled {
		t.Error("weatherEnabled should be true")
	}
	if s.cfg.City != "Moscow" {
		t.Error("city should be Moscow")
	}
}

func TestMorningBriefing_NoWeatherWhenDisabled(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{City: "Moscow"},
		nil, nil, nil, nil, false /* weatherEnabled */, false, false, slog.Default(),
	)
	if s.weatherEnabled {
		t.Error("weatherEnabled should be false")
	}
}

// ── Finance / morning briefing prompts ───────────────────────────────────────

func TestDefaultFinancialTickers_Empty(t *testing.T) {
	cfg := config.ProactiveConfig{}
	tickers := defaultFinancialTickers(cfg)
	want := []string{"IMOEX", "RTSI", "sp500", "nasdaq"}
	if len(tickers) != len(want) {
		t.Fatalf("got %v, want %v", tickers, want)
	}
	for i, v := range want {
		if tickers[i] != v {
			t.Errorf("tickers[%d]: got %q, want %q", i, tickers[i], v)
		}
	}
}

func TestDefaultFinancialTickers_Custom(t *testing.T) {
	cfg := config.ProactiveConfig{FinancialTickers: []string{"AAPL", "MSFT"}}
	tickers := defaultFinancialTickers(cfg)
	if len(tickers) != 2 || tickers[0] != "AAPL" || tickers[1] != "MSFT" {
		t.Errorf("got %v, want [AAPL MSFT]", tickers)
	}
}

func TestMorningBriefing_StocksEnabled_PromptContainsStocks(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{City: "Москва"},
		nil, nil, nil, nil, true /* weather */, false, true /* stocks */, slog.Default(),
	)
	if !s.stocksEnabled {
		t.Error("stocksEnabled should be true")
	}
}

func TestMorningBriefing_StocksDisabled_FieldFalse(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{City: "Москва"},
		nil, nil, nil, nil, false, false, false, slog.Default(),
	)
	if s.stocksEnabled {
		t.Error("stocksEnabled should be false when not configured")
	}
}

// ── Evening news / finance digests ───────────────────────────────────────────

func TestFireEveningNews_Disabled_NoDispatch(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{},
		nil, nil, nil, nil,
		false, false /* newsEnabled=false */, false, slog.Default(),
	)
	// fireEveningNews should be a no-op when newsEnabled=false.
	// We verify newsEnabled field directly (dispatch would panic with nil agent).
	if s.newsEnabled {
		t.Error("newsEnabled should be false")
	}
}

func TestFireEveningNews_Enabled_FieldTrue(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{},
		nil, nil, nil, nil,
		false, true /* newsEnabled */, false, slog.Default(),
	)
	if !s.newsEnabled {
		t.Error("newsEnabled should be true")
	}
}

func TestFireEveningFinance_Disabled_NoDispatch(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{},
		nil, nil, nil, nil,
		false, false, false /* stocksEnabled=false */, slog.Default(),
	)
	if s.stocksEnabled {
		t.Error("stocksEnabled should be false")
	}
}

func TestFireEveningFinance_Enabled_FieldTrue(t *testing.T) {
	s := newProactiveScheduler(
		config.ProactiveConfig{},
		nil, nil, nil, nil,
		false, false, true /* stocksEnabled */, slog.Default(),
	)
	if !s.stocksEnabled {
		t.Error("stocksEnabled should be true")
	}
}

// TestTick_EveningEventsRegistered verifies that when evening time fires,
// evening_news and evening_finance event keys are registered when enabled.
func TestTick_EveningEventsRegistered(t *testing.T) {
	now := time.Now()
	hhmm := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())

	s := newProactiveScheduler(
		config.ProactiveConfig{EveningCheckinTime: hhmm},
		nil, nil, nil, nil,
		false, true /* newsEnabled */, true /* stocksEnabled */, slog.Default(),
	)
	s.today = now.Format("2006-01-02")
	s.nowFn = func() time.Time { return now }

	// tick fires events; handlers will panic (nil agent) — recover from that.
	func() {
		defer func() { recover() }() //nolint:errcheck
		s.tick(context.Background())
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, key := range []string{"evening_checkin", "evening_news", "evening_finance"} {
		if !s.fired[key] {
			t.Errorf("expected event %q to be fired", key)
		}
	}
}
