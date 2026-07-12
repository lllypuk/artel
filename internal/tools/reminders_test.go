package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/storage"
	"github.com/lllypuk/artel/internal/tools"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func openRemindersDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func remExec(t *testing.T, rt *tools.RemindersTool, chatID int64, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, chatID)
	return rt.Execute(ctx, raw)
}

// ─── ParseReminderTime ────────────────────────────────────────────────────────

func TestParseReminderTime_ISO(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"2026-04-03T10:00", time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)},
		{"2026-04-03 10:00", time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)},
		{"2026-04-03T10:00:00", time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		got, err := tools.ParseReminderTime(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseReminderTime_RelativeRu(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"через 2 часа", now.Add(2 * time.Hour)},
		{"через 30 минут", now.Add(30 * time.Minute)},
		{"через 3 дня", now.Add(3 * 24 * time.Hour)},
		{"через 1 неделю", now.Add(7 * 24 * time.Hour)},
	}
	for _, c := range cases {
		got, err := tools.ParseReminderTime(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseReminderTime_RelativeEn(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"in 2 hours", now.Add(2 * time.Hour)},
		{"in 45 minutes", now.Add(45 * time.Minute)},
	}
	for _, c := range cases {
		got, err := tools.ParseReminderTime(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseReminderTime_AbsoluteRu(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, loc)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"завтра в 10:00", time.Date(2026, 4, 3, 10, 0, 0, 0, loc)},
		{"послезавтра в 15:30", time.Date(2026, 4, 4, 15, 30, 0, 0, loc)},
		{"сегодня в 14:00", time.Date(2026, 4, 2, 14, 0, 0, 0, loc)},
		{"tomorrow at 09:00", time.Date(2026, 4, 3, 9, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		got, err := tools.ParseReminderTime(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseReminderTime_TimeOnly_Future(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	// 15:00 today is in the future → same day
	got, err := tools.ParseReminderTime("в 15:00", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 2, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseReminderTime_TimeOnly_Past(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	// 09:00 today is in the past → advance to tomorrow
	got, err := tools.ParseReminderTime("в 09:00", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseReminderTime_Invalid(t *testing.T) {
	now := time.Now()
	_, err := tools.ParseReminderTime("banana", now)
	if err == nil {
		t.Error("expected error for invalid input")
	}
}

// ─── ParseRepeatInterval ──────────────────────────────────────────────────────

func TestParseRepeatInterval(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"daily", 86400},
		{"weekly", 604800},
		{"hourly", 3600},
		{"30m", 1800},
		{"2h", 7200},
		{"3d", 259200},
	}
	for _, c := range cases {
		got, err := tools.ParseRepeatInterval(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseRepeatInterval_Invalid(t *testing.T) {
	_, err := tools.ParseRepeatInterval("forever")
	if err == nil {
		t.Error("expected error for invalid repeat interval")
	}
}

// ─── RemindersTool actions ────────────────────────────────────────────────────

func TestRemindersTool_Name(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	if rt.Name() != "reminders" {
		t.Errorf("expected 'reminders', got %q", rt.Name())
	}
}

func TestRemindersTool_CreateAndList(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	const chatID int64 = 100

	out, err := remExec(t, rt, chatID, map[string]any{
		"action": "create",
		"text":   "тест напоминание",
		"when":   time.Now().Add(time.Hour).Format("2006-01-02T15:04:05"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "Напоминание создано") {
		t.Errorf("unexpected create output: %q", out)
	}

	list, err := remExec(t, rt, chatID, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(list, "тест напоминание") {
		t.Errorf("reminder not in list: %q", list)
	}
}

func TestRemindersTool_CreateWithRepeat(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	const chatID int64 = 101

	out, err := remExec(t, rt, chatID, map[string]any{
		"action": "create",
		"text":   "ежедневная задача",
		"when":   time.Now().Add(time.Hour).Format("2006-01-02T15:04:05"),
		"repeat": "daily",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "Повтор") {
		t.Errorf("expected repeat info in output: %q", out)
	}
}

func TestRemindersTool_Delete(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	const chatID int64 = 102

	_, err := remExec(t, rt, chatID, map[string]any{
		"action": "create",
		"text":   "удалить меня",
		"when":   time.Now().Add(time.Hour).Format("2006-01-02T15:04:05"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get id from list
	list, _ := remExec(t, rt, chatID, map[string]any{"action": "list"})
	if !strings.Contains(list, "удалить меня") {
		t.Fatalf("reminder not found after create")
	}

	// Extract id from DB directly
	reminders, err := rt.DB().ListReminders(context.Background(), chatID)
	if err != nil || len(reminders) == 0 {
		t.Fatalf("list from DB: %v, count: %d", err, len(reminders))
	}
	id := reminders[0].ID

	out, err := remExec(t, rt, chatID, map[string]any{"action": "delete", "id": id})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out, "отменено") {
		t.Errorf("unexpected delete output: %q", out)
	}

	list2, _ := remExec(t, rt, chatID, map[string]any{"action": "list"})
	if strings.Contains(list2, "удалить меня") {
		t.Errorf("reminder still in list after delete")
	}
}

func TestRemindersTool_Done(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	const chatID int64 = 103

	_, _ = remExec(t, rt, chatID, map[string]any{
		"action": "create",
		"text":   "завершить",
		"when":   time.Now().Add(time.Hour).Format("2006-01-02T15:04:05"),
	})

	reminders, _ := rt.DB().ListReminders(context.Background(), chatID)
	if len(reminders) == 0 {
		t.Fatal("no reminders after create")
	}
	id := reminders[0].ID

	out, err := remExec(t, rt, chatID, map[string]any{"action": "done", "id": id})
	if err != nil {
		t.Fatalf("done: %v", err)
	}
	if !strings.Contains(out, "выполненное") {
		t.Errorf("unexpected done output: %q", out)
	}
}

func TestRemindersTool_ListEmpty(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	out, err := remExec(t, rt, 999, map[string]any{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "нет") {
		t.Errorf("expected empty message, got: %q", out)
	}
}

func TestRemindersTool_CreateMissingFields(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	_, err := remExec(t, rt, 1, map[string]any{"action": "create"})
	if err == nil {
		t.Error("expected error for missing text")
	}

	_, err = remExec(t, rt, 1, map[string]any{"action": "create", "text": "foo"})
	if err == nil {
		t.Error("expected error for missing when")
	}
}

func TestRemindersTool_UnknownAction(t *testing.T) {
	rt := tools.NewRemindersTool(openRemindersDB(t))
	_, err := remExec(t, rt, 1, map[string]any{"action": "fly"})
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

// ─── Storage: GetDueReminders ─────────────────────────────────────────────────

func TestGetDueReminders(t *testing.T) {
	db := openRemindersDB(t)
	ctx := context.Background()

	now := time.Now()
	past := now.Add(-5 * time.Minute)
	future := now.Add(5 * time.Minute)

	_, err := db.CreateReminder(ctx, 1, "past", past, 0)
	if err != nil {
		t.Fatalf("create past: %v", err)
	}
	_, err = db.CreateReminder(ctx, 1, "future", future, 0)
	if err != nil {
		t.Fatalf("create future: %v", err)
	}

	due, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due reminder, got %d", len(due))
	}
	if due[0].Text != "past" {
		t.Errorf("expected 'past', got %q", due[0].Text)
	}
}

func TestRescheduleReminder(t *testing.T) {
	db := openRemindersDB(t)
	ctx := context.Background()

	id, err := db.CreateReminder(ctx, 1, "recurring", time.Now().Add(-time.Minute), 3600)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	nextAt := time.Now().Add(time.Hour)
	if err := db.RescheduleReminder(ctx, id, nextAt); err != nil {
		t.Fatalf("reschedule: %v", err)
	}

	reminders, err := db.ListReminders(ctx, 1)
	if err != nil || len(reminders) == 0 {
		t.Fatalf("list: %v, count: %d", err, len(reminders))
	}
	if reminders[0].Status != "pending" {
		t.Errorf("expected pending, got %q", reminders[0].Status)
	}
	// trigger_at should be in the future now
	if !reminders[0].TriggerAt.After(time.Now()) {
		t.Errorf("rescheduled trigger_at is not in the future: %v", reminders[0].TriggerAt)
	}
}
