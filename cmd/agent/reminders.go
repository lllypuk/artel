package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/lllypuk/artel/internal/storage"
	"github.com/lllypuk/artel/internal/tools"
)

func runReminderScheduler(ctx context.Context, db *storage.DB, notifier tools.Notifier, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("reminder scheduler stopped")
			return
		case <-ticker.C:
			processDueReminders(ctx, db, notifier, logger)
		}
	}
}

func processDueReminders(ctx context.Context, db *storage.DB, notifier tools.Notifier, logger *slog.Logger) {
	now := time.Now()
	reminders, err := db.GetDueReminders(ctx, now)
	if err != nil {
		logger.Error("get due reminders", "error", err)
		return
	}
	for _, r := range reminders {
		if notifier != nil {
			notifier(r.ChatID, fmt.Sprintf("⏰ Напоминание: %s", r.Text))
		}
		if r.RepeatInterval > 0 {
			nextAt := r.TriggerAt.Add(time.Duration(r.RepeatInterval) * time.Second)
			for !nextAt.After(now) {
				nextAt = nextAt.Add(time.Duration(r.RepeatInterval) * time.Second)
			}
			if err := db.RescheduleReminder(ctx, r.ID, nextAt); err != nil {
				logger.Error("reschedule reminder", "id", r.ID, "error", err)
			}
			logger.Info("reminder rescheduled", "id", r.ID, "next_at", nextAt)
		} else {
			if err := db.MarkReminderDone(ctx, r.ID); err != nil {
				logger.Error("mark reminder done", "id", r.ID, "error", err)
			}
			logger.Info("reminder fired", "id", r.ID, "chat_id", r.ChatID)
		}
	}
}
