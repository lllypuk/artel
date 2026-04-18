package main

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// eventWindow is how long after a scheduled time the event may still fire.
// Allows the agent to catch up after a restart within this window.
const eventWindow = 3 * time.Minute

// proactiveScheduler sends scheduled messages to users without any user input.
// Each event fires at most once per day; per-user daily message limit is enforced.
type proactiveScheduler struct {
	cfg    config.ProactiveConfig
	ag     *agent.Agent
	send   tools.Notifier
	users  []int64
	loc    *time.Location
	logger *slog.Logger
	nowFn  func() time.Time // injectable for testing; default is time.Now

	mu        sync.Mutex
	today     string          // "2006-01-02"
	sentToday map[int64]int   // chatID → messages sent today (per-user limit)
	fired     map[string]bool // event key → fired today
}

// runProactiveScheduler starts the scheduler goroutine.
// Exits immediately if cfg.Enabled is false.
func runProactiveScheduler(
	ctx context.Context,
	cfg config.ProactiveConfig,
	ag *agent.Agent,
	send tools.Notifier,
	users []int64,
	logger *slog.Logger,
) {
	if !cfg.Enabled {
		logger.Info("proactive scheduler disabled")
		return
	}
	s := newProactiveScheduler(cfg, ag, send, users, logger)
	s.run(ctx)
}

func newProactiveScheduler(
	cfg config.ProactiveConfig,
	ag *agent.Agent,
	send tools.Notifier,
	users []int64,
	logger *slog.Logger,
) *proactiveScheduler {
	if cfg.MaxMessagesPerDay <= 0 {
		cfg.MaxMessagesPerDay = 7
	}
	if cfg.GoalReminderDaysIdle <= 0 {
		cfg.GoalReminderDaysIdle = 3
	}

	loc := time.Local
	if cfg.Timezone != "" {
		if l, err := time.LoadLocation(cfg.Timezone); err == nil {
			loc = l
		} else {
			logger.Warn("proactive: unknown timezone, using local", "timezone", cfg.Timezone)
		}
	}

	return &proactiveScheduler{
		cfg:       cfg,
		ag:        ag,
		send:      send,
		users:     users,
		loc:       loc,
		logger:    logger,
		sentToday: make(map[int64]int),
		fired:     make(map[string]bool),
	}
}

// now returns the current time in the scheduler's timezone.
func (s *proactiveScheduler) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn().In(s.loc)
	}
	return time.Now().In(s.loc)
}

func (s *proactiveScheduler) run(ctx context.Context) {
	s.logger.Info("proactive scheduler started",
		"max_per_day", s.cfg.MaxMessagesPerDay,
		"morning", orDisabled(s.cfg.MorningBriefingTime),
		"evening", orDisabled(s.cfg.EveningCheckinTime),
		"weekly", orDisabled(s.cfg.WeeklyReportTime),
		"users", len(s.users),
	)

	// Check immediately on start to catch events within the window that fired
	// while the agent was down (e.g. restart at 08:01 catches the 08:00 briefing).
	s.tick(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("proactive scheduler stopped")
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *proactiveScheduler) tick(ctx context.Context) {
	now := s.now()
	todayStr := now.Format("2006-01-02")

	s.mu.Lock()
	if s.today != todayStr {
		s.today = todayStr
		s.sentToday = make(map[int64]int)
		s.fired = make(map[string]bool)
		s.logger.Debug("proactive: new day, counters reset", "date", todayStr)
	}
	s.mu.Unlock()

	// P-2: Morning briefing
	if s.cfg.MorningBriefingTime != "" {
		s.maybeFireEvent(ctx, "morning_briefing", s.cfg.MorningBriefingTime, now, s.fireMorningBriefing)
	}
	// P-3: Evening check-in
	if s.cfg.EveningCheckinTime != "" {
		s.maybeFireEvent(ctx, "evening_checkin", s.cfg.EveningCheckinTime, now, s.fireEveningCheckin)
	}
	// P-4: Weekly report — Sundays only
	if s.cfg.WeeklyReportTime != "" && now.Weekday() == time.Sunday {
		s.maybeFireEvent(ctx, "weekly_report", s.cfg.WeeklyReportTime, now, s.fireWeeklyReport)
	}
}

// maybeFireEvent fires the handler if now is within [scheduled, scheduled+eventWindow)
// and the event has not already fired today.
func (s *proactiveScheduler) maybeFireEvent(
	ctx context.Context,
	key, scheduled string,
	now time.Time,
	handler func(context.Context),
) {
	t, err := parseScheduledTime(scheduled, now, s.loc)
	if err != nil {
		s.logger.Warn("proactive: invalid schedule time", "key", key, "scheduled", scheduled, "error", err)
		return
	}

	elapsed := now.Sub(t)
	if elapsed < 0 || elapsed >= eventWindow {
		return
	}

	s.mu.Lock()
	if s.fired[key] {
		s.mu.Unlock()
		return
	}
	s.fired[key] = true
	s.mu.Unlock()

	s.logger.Info("proactive event fired", "event", key)
	go handler(ctx)
}

// canSendToUser checks and reserves a slot in the per-user daily limit.
func (s *proactiveScheduler) canSendToUser(chatID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sentToday[chatID] >= s.cfg.MaxMessagesPerDay {
		return false
	}
	s.sentToday[chatID]++
	return true
}

// --- P-2: Morning briefing ---

func (s *proactiveScheduler) fireMorningBriefing(ctx context.Context) {
	var prompt string
	if s.cfg.City != "" {
		prompt = fmt.Sprintf(
			"🔔 [АВТО: УТРЕННИЙ БРИФИНГ] Составь краткое утреннее сообщение: "+
				"1) Приветствие + день недели и дата "+
				"2) Прогноз погоды в %s на сегодня (используй инструмент weather) "+
				"3) Одна мотивирующая мысль на день. "+
				"Всего 5-7 строк.",
			s.cfg.City,
		)
	} else {
		prompt = "🔔 [АВТО: УТРЕННИЙ БРИФИНГ] Составь краткое утреннее сообщение: " +
			"1) Приветствие + день недели и дата " +
			"2) Одна мотивирующая мысль на день. " +
			"Всего 3-4 строки."
	}
	for _, chatID := range s.users {
		s.dispatch(ctx, chatID, prompt)
	}
}

// --- P-3: Evening check-in ---

func (s *proactiveScheduler) fireEveningCheckin(ctx context.Context) {
	prompt := "🔔 [АВТО: ВЕЧЕРНИЙ ЧЕК-ИН] Напиши короткое вечернее приветствие и предложи " +
		"сделать ревью дня. Задай один вопрос: как прошёл сегодняшний день? Максимум 3-4 строки."
	for _, chatID := range s.users {
		s.dispatch(ctx, chatID, prompt)
	}
}

// --- P-4: Weekly report ---

func (s *proactiveScheduler) fireWeeklyReport(ctx context.Context) {
	prompt := "🔔 [АВТО: ЕЖЕНЕДЕЛЬНЫЙ РЕВЬЮ] Напиши воскресное вечернее сообщение. " +
		"Поздравь с завершением недели и предложи сделать ревью: " +
		"что получилось, что нет, план на следующую неделю. Максимум 5 строк."
	for _, chatID := range s.users {
		s.dispatch(ctx, chatID, prompt)
	}
}

// dispatch sends a proactive prompt through the agent for a single user.
// The per-user daily limit is checked here, not in maybeFireEvent.
func (s *proactiveScheduler) dispatch(ctx context.Context, chatID int64, prompt string) {
	if !s.canSendToUser(chatID) {
		s.logger.Info("proactive: daily limit reached for user", "chat_id", chatID)
		return
	}

	s.logger.Debug("proactive dispatch", "chat_id", chatID, "prompt_len", len(prompt))
	resp, err := s.ag.HandleMessage(ctx, chatID, prompt)
	if err != nil {
		s.logger.Error("proactive dispatch failed", "chat_id", chatID, "error", err)
		return
	}
	if resp.Text != "" && s.send != nil {
		s.send(chatID, resp.Text)
	}
}

// parseScheduledTime parses "HH:MM" and returns today's time at that hour/minute in loc.
func parseScheduledTime(hhmm string, now time.Time, loc *time.Location) (time.Time, error) {
	parts := strings.SplitN(hhmm, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, fmt.Errorf("invalid time format %q: expected HH:MM", hhmm)
	}
	h, err := strconv.Atoi(parts[0])
	if err != nil || h < 0 || h > 23 {
		return time.Time{}, fmt.Errorf("invalid hour in %q", hhmm)
	}
	m, err := strconv.Atoi(parts[1])
	if err != nil || m < 0 || m > 59 {
		return time.Time{}, fmt.Errorf("invalid minute in %q", hhmm)
	}
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, loc), nil
}

func orDisabled(s string) string {
	if s == "" {
		return "disabled"
	}
	return s
}
