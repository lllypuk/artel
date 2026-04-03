package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Reminder represents a scheduled reminder.
type Reminder struct {
	ID             int64
	ChatID         int64
	Text           string
	TriggerAt      time.Time
	RepeatInterval int64 // seconds; 0 = no repeat
	Status         string
	CreatedAt      time.Time
}

// CreateReminder inserts a new reminder and returns its id.
func (s *DB) CreateReminder(ctx context.Context, chatID int64, text string, triggerAt time.Time, repeatInterval int64) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO reminders (chat_id, text, trigger_at, repeat_interval) VALUES (?, ?, ?, ?)",
		chatID, text, triggerAt, repeatInterval,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListReminders returns all pending (and snoozed) reminders for a chat, ordered by trigger_at.
func (s *DB) ListReminders(ctx context.Context, chatID int64) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, text, trigger_at, repeat_interval, status, created_at
		 FROM reminders
		 WHERE chat_id = ? AND status = 'pending'
		 ORDER BY trigger_at ASC`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanReminders(rows)
}

// GetDueReminders returns pending reminders whose trigger_at <= now (across all chats).
func (s *DB) GetDueReminders(ctx context.Context, now time.Time) ([]Reminder, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, text, trigger_at, repeat_interval, status, created_at
		 FROM reminders
		 WHERE status = 'pending' AND trigger_at <= ?
		 ORDER BY trigger_at ASC`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanReminders(rows)
}

// MarkReminderDone marks a reminder as done.
func (s *DB) MarkReminderDone(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE reminders SET status = 'done' WHERE id = ?", id,
	)
	return err
}

// RescheduleReminder sets a new trigger_at for a recurring reminder (keeping status = 'pending').
func (s *DB) RescheduleReminder(ctx context.Context, id int64, nextAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE reminders SET trigger_at = ? WHERE id = ?",
		nextAt, id,
	)
	return err
}

// CancelReminder marks a reminder as cancelled (soft-delete).
func (s *DB) CancelReminder(ctx context.Context, id, chatID int64) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE reminders SET status = 'cancelled' WHERE id = ? AND chat_id = ?",
		id, chatID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("reminder %d not found or access denied", id)
	}
	return nil
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(
			&r.ID, &r.ChatID, &r.Text, &r.TriggerAt,
			&r.RepeatInterval, &r.Status, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
