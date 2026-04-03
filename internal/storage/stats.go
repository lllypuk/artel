package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"
)

// UsageStats represents token usage statistics for one day.
type UsageStats struct {
	Date             string
	PromptTokens     int
	CompletionTokens int
	RequestsCount    int
}

// DBStats holds general database statistics.
type DBStats struct {
	NotesCount         int
	ConversationsCount int
	MessagesCount      int
	RemindersCount     int // pending reminders only
	FeedsCount         int
	DBSizeBytes        int64
}

// RecordUsage increments token usage counters for today.
// It is called once per LLM API round-trip.
func (s *DB) RecordUsage(ctx context.Context, promptTokens, completionTokens int) error {
	today := time.Now().Format("2006-01-02")
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_stats (date, prompt_tokens, completion_tokens, requests_count)
		VALUES (?, ?, ?, 1)
		ON CONFLICT(date) DO UPDATE SET
			prompt_tokens     = prompt_tokens     + excluded.prompt_tokens,
			completion_tokens = completion_tokens + excluded.completion_tokens,
			requests_count    = requests_count    + 1
	`, today, promptTokens, completionTokens)
	return err
}

// GetUsageToday returns accumulated token usage for the current calendar day.
// Returns an empty (zero-value) struct when no data exists for today.
func (s *DB) GetUsageToday(ctx context.Context) (*UsageStats, error) {
	today := time.Now().Format("2006-01-02")
	stats := &UsageStats{Date: today}
	err := s.db.QueryRowContext(ctx, `
		SELECT prompt_tokens, completion_tokens, requests_count
		FROM usage_stats WHERE date = ?
	`, today).Scan(&stats.PromptTokens, &stats.CompletionTokens, &stats.RequestsCount)
	if err == sql.ErrNoRows {
		return stats, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get usage today: %w", err)
	}
	return stats, nil
}

// GetUsageLast7Days returns daily usage for the last 7 days (newest first).
func (s *DB) GetUsageLast7Days(ctx context.Context) ([]UsageStats, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT date, prompt_tokens, completion_tokens, requests_count
		FROM usage_stats
		WHERE date >= date('now', '-6 days')
		ORDER BY date DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("get usage last 7 days: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var stats []UsageStats
	for rows.Next() {
		var st UsageStats
		if err := rows.Scan(&st.Date, &st.PromptTokens, &st.CompletionTokens, &st.RequestsCount); err != nil {
			return nil, err
		}
		stats = append(stats, st)
	}
	return stats, rows.Err()
}

// GetDBStats returns row counts and the database file size.
// dbPath must be the filesystem path passed to Open().
func (s *DB) GetDBStats(ctx context.Context, dbPath string) (*DBStats, error) {
	stats := &DBStats{}

	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes").Scan(&stats.NotesCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversations").Scan(&stats.ConversationsCount)
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages").Scan(&stats.MessagesCount)
	_ = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM reminders WHERE status = 'pending'").Scan(&stats.RemindersCount)
	_ = s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM news_feeds WHERE enabled = 1").Scan(&stats.FeedsCount)

	if fi, err := os.Stat(dbPath); err == nil {
		stats.DBSizeBytes = fi.Size()
	}

	return stats, nil
}
