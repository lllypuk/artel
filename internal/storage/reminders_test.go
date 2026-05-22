package storage_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

func fixedReminderTime() time.Time {
	return time.Date(2025, time.January, 2, 15, 4, 5, 0, time.UTC)
}

func createTestReminder(t *testing.T, db *storage.DB, ctx context.Context, chatID int64, text string, triggerAt time.Time, repeatInterval int64) int64 {
	t.Helper()

	id, err := db.CreateReminder(ctx, chatID, text, triggerAt, repeatInterval)
	if err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	return id
}

func TestCreateReminder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := db.CreateReminder(ctx, 100, "pay rent", fixedReminderTime(), 3600)
	if err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero reminder id")
	}
}

func TestListReminders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	base := fixedReminderTime()

	laterID := createTestReminder(t, db, ctx, 200, "later", base.Add(2*time.Hour), 0)
	earlierID := createTestReminder(t, db, ctx, 200, "earlier", base.Add(30*time.Minute), 0)
	doneID := createTestReminder(t, db, ctx, 200, "done", base.Add(time.Hour), 0)

	if err := db.MarkReminderDone(ctx, doneID); err != nil {
		t.Fatalf("mark reminder done: %v", err)
	}

	reminders, err := db.ListReminders(ctx, 200)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 2 {
		t.Fatalf("expected 2 pending reminders, got %d", len(reminders))
	}
	if reminders[0].ID != earlierID {
		t.Errorf("expected first reminder id=%d, got %d", earlierID, reminders[0].ID)
	}
	if reminders[1].ID != laterID {
		t.Errorf("expected second reminder id=%d, got %d", laterID, reminders[1].ID)
	}
	if reminders[0].Status != "pending" {
		t.Errorf("expected first reminder status pending, got %q", reminders[0].Status)
	}
	if reminders[1].Status != "pending" {
		t.Errorf("expected second reminder status pending, got %q", reminders[1].Status)
	}
}

func TestListReminders_EmptyForOtherChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	createTestReminder(t, db, ctx, 300, "chat-a reminder", fixedReminderTime(), 0)

	reminders, err := db.ListReminders(ctx, 301)
	if err != nil {
		t.Fatalf("list reminders for other chat: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 reminders for other chat, got %d", len(reminders))
	}
}

func TestGetDueReminders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	oldestID := createTestReminder(t, db, ctx, 400, "oldest", now.Add(-2*time.Hour), 0)
	futureID := createTestReminder(t, db, ctx, 401, "future", now.Add(2*time.Hour), 0)
	recentID := createTestReminder(t, db, ctx, 402, "recent", now.Add(-15*time.Minute), 0)

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 2 {
		t.Fatalf("expected 2 due reminders, got %d", len(reminders))
	}
	if reminders[0].ID != oldestID {
		t.Errorf("expected first due reminder id=%d, got %d", oldestID, reminders[0].ID)
	}
	if reminders[1].ID != recentID {
		t.Errorf("expected second due reminder id=%d, got %d", recentID, reminders[1].ID)
	}
	for _, reminder := range reminders {
		if reminder.ID == futureID {
			t.Errorf("future reminder id=%d should not be due", futureID)
		}
	}
}

func TestMarkReminderDone(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := createTestReminder(t, db, ctx, 500, "done me", fixedReminderTime(), 0)

	if err := db.MarkReminderDone(ctx, id); err != nil {
		t.Fatalf("mark reminder done: %v", err)
	}

	reminders, err := db.ListReminders(ctx, 500)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 pending reminders after mark done, got %d", len(reminders))
	}
}

func TestRescheduleReminder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()
	newTrigger := now.Add(2 * time.Hour)

	id := createTestReminder(t, db, ctx, 600, "reschedule me", now.Add(-time.Hour), 0)

	if err := db.RescheduleReminder(ctx, id, newTrigger); err != nil {
		t.Fatalf("reschedule reminder: %v", err)
	}

	dueNow, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders at current time: %v", err)
	}
	if len(dueNow) != 0 {
		t.Fatalf("expected 0 due reminders after reschedule, got %d", len(dueNow))
	}

	dueLater, err := db.GetDueReminders(ctx, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("get due reminders after new trigger: %v", err)
	}
	if len(dueLater) != 1 {
		t.Fatalf("expected 1 due reminder after new trigger, got %d", len(dueLater))
	}
	if dueLater[0].ID != id {
		t.Errorf("expected rescheduled reminder id=%d, got %d", id, dueLater[0].ID)
	}
	if !dueLater[0].TriggerAt.Equal(newTrigger) {
		t.Errorf("expected rescheduled trigger_at %v, got %v", newTrigger, dueLater[0].TriggerAt)
	}
}

func TestCancelReminder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := createTestReminder(t, db, ctx, 700, "cancel me", fixedReminderTime(), 0)

	if err := db.CancelReminder(ctx, id, 700); err != nil {
		t.Fatalf("cancel reminder: %v", err)
	}

	reminders, err := db.ListReminders(ctx, 700)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 pending reminders after cancel, got %d", len(reminders))
	}
}

func TestCancelReminder_WrongChat(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id := createTestReminder(t, db, ctx, 800, "wrong chat", fixedReminderTime(), 0)

	err := db.CancelReminder(ctx, id, 801)
	if err == nil {
		t.Fatal("expected cancel reminder error for wrong chat")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}

	reminders, err := db.ListReminders(ctx, 800)
	if err != nil {
		t.Fatalf("list reminders for original chat: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected reminder to remain for original chat, got %d reminders", len(reminders))
	}
}

func TestGetDueReminders_RespectsNow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	createTestReminder(t, db, ctx, 900, "future only", now.Add(10*time.Minute), 0)

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 due reminders before trigger time, got %d", len(reminders))
	}
}

func TestGetDueReminders_IgnoresDone(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	id := createTestReminder(t, db, ctx, 1000, "already done", now.Add(-time.Hour), 0)
	if err := db.MarkReminderDone(ctx, id); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 due reminders (all done), got %d", len(reminders))
	}
}

func TestGetDueReminders_IgnoresCancelled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	id := createTestReminder(t, db, ctx, 1001, "cancelled", now.Add(-time.Hour), 0)
	if err := db.CancelReminder(ctx, id, 1001); err != nil {
		t.Fatalf("cancel reminder: %v", err)
	}

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 due reminders (cancelled), got %d", len(reminders))
	}
}

func TestGetDueReminders_ExactBoundary(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	// trigger_at == now must be included (trigger_at <= now).
	id := createTestReminder(t, db, ctx, 1002, "exact boundary", now, 0)

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected 1 due reminder at exact boundary, got %d", len(reminders))
	}
	if reminders[0].ID != id {
		t.Errorf("expected id=%d, got %d", id, reminders[0].ID)
	}
}

func TestListReminders_IgnoresCancelled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	pendingID := createTestReminder(t, db, ctx, 1003, "pending", now.Add(time.Hour), 0)
	cancelID := createTestReminder(t, db, ctx, 1003, "cancelled", now.Add(2*time.Hour), 0)
	if err := db.CancelReminder(ctx, cancelID, 1003); err != nil {
		t.Fatalf("cancel reminder: %v", err)
	}

	reminders, err := db.ListReminders(ctx, 1003)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected 1 pending reminder, got %d", len(reminders))
	}
	if reminders[0].ID != pendingID {
		t.Errorf("expected pending id=%d, got %d", pendingID, reminders[0].ID)
	}
}

func TestCreateReminder_FieldsStored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()
	const interval = int64(86400) // 1 day

	id := createTestReminder(t, db, ctx, 1004, "check fields", now, interval)

	reminders, err := db.ListReminders(ctx, 1004)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(reminders))
	}
	r := reminders[0]
	if r.ID != id {
		t.Errorf("id: want %d, got %d", id, r.ID)
	}
	if r.ChatID != 1004 {
		t.Errorf("chat_id: want 1004, got %d", r.ChatID)
	}
	if r.Text != "check fields" {
		t.Errorf("text: want %q, got %q", "check fields", r.Text)
	}
	if r.RepeatInterval != interval {
		t.Errorf("repeat_interval: want %d, got %d", interval, r.RepeatInterval)
	}
	if r.Status != "pending" {
		t.Errorf("status: want %q, got %q", "pending", r.Status)
	}
	if r.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
}

func TestRescheduleReminder_KeepsPending(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	id := createTestReminder(t, db, ctx, 1005, "recurring", now.Add(-time.Hour), 3600)
	if err := db.RescheduleReminder(ctx, id, now.Add(time.Hour)); err != nil {
		t.Fatalf("reschedule: %v", err)
	}

	// Must still appear in pending list.
	reminders, err := db.ListReminders(ctx, 1005)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected 1 pending reminder after reschedule, got %d", len(reminders))
	}
	if reminders[0].Status != "pending" {
		t.Errorf("expected status pending, got %q", reminders[0].Status)
	}
}

func TestCancelReminder_NotFoundID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	err := db.CancelReminder(ctx, 99999, 1006)
	if err == nil {
		t.Fatal("expected error when cancelling non-existent id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestGetDueReminders_OrderedByTriggerAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	id3 := createTestReminder(t, db, ctx, 1007, "third", now.Add(-10*time.Minute), 0)
	id1 := createTestReminder(t, db, ctx, 1007, "first", now.Add(-3*time.Hour), 0)
	id2 := createTestReminder(t, db, ctx, 1007, "second", now.Add(-90*time.Minute), 0)

	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		t.Fatalf("get due reminders: %v", err)
	}
	if len(reminders) != 3 {
		t.Fatalf("expected 3 due reminders, got %d", len(reminders))
	}
	wantOrder := []int64{id1, id2, id3}
	for i, want := range wantOrder {
		if reminders[i].ID != want {
			t.Errorf("position %d: want id=%d, got id=%d", i, want, reminders[i].ID)
		}
	}
}

func TestListReminders_OrderedByTriggerAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := fixedReminderTime()

	id2 := createTestReminder(t, db, ctx, 1008, "second", now.Add(2*time.Hour), 0)
	id3 := createTestReminder(t, db, ctx, 1008, "third", now.Add(5*time.Hour), 0)
	id1 := createTestReminder(t, db, ctx, 1008, "first", now.Add(30*time.Minute), 0)

	reminders, err := db.ListReminders(ctx, 1008)
	if err != nil {
		t.Fatalf("list reminders: %v", err)
	}
	if len(reminders) != 3 {
		t.Fatalf("expected 3 reminders, got %d", len(reminders))
	}
	wantOrder := []int64{id1, id2, id3}
	for i, want := range wantOrder {
		if reminders[i].ID != want {
			t.Errorf("position %d: want id=%d, got id=%d", i, want, reminders[i].ID)
		}
	}
}

func TestListReminders_EmptyDB(t *testing.T) {
	db := openTestDB(t)
	reminders, err := db.ListReminders(context.Background(), 9999)
	if err != nil {
		t.Fatalf("list reminders on empty db: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 reminders, got %d", len(reminders))
	}
}

func TestGetDueReminders_EmptyDB(t *testing.T) {
	db := openTestDB(t)
	reminders, err := db.GetDueReminders(context.Background(), fixedReminderTime())
	if err != nil {
		t.Fatalf("get due reminders on empty db: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("expected 0 due reminders, got %d", len(reminders))
	}
}
