package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DayReview represents a daily self-review entry.
type DayReview struct {
	ID        int64
	ChatID    int64
	Date      string // "2026-04-18"
	Planned   string
	Done      string
	NotDone   string
	Reason    string
	Score     int // 1-10, 0 = not set
	Mood      string
	Note      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// WeekReview represents a weekly self-review entry.
type WeekReview struct {
	ID           int64
	ChatID       int64
	Week         string // ISO week "2026-W16"
	Wins         string
	Fails        string
	Pattern      string
	NextWeekPlan string
	Score        int // 1-10, 0 = not set
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Objective represents a goal with a time horizon.
type Objective struct {
	ID          int64
	ChatID      int64
	Title       string
	Description string
	Horizon     string // year / quarter / month
	Period      string // "2026-Q2", "2026", "2026-04"
	Status      string // active / paused / done / archived
	Progress    int    // 0-100
	KeyResults  []KeyResult
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// KeyResult is a measurable outcome attached to an Objective.
type KeyResult struct {
	ID          int64
	ObjectiveID int64
	Title       string
	Progress    int    // 0-100
	Deadline    string // "2026-06-30"
	Note        string
	UpdatedAt   time.Time
}

// DayReviewStats holds aggregated stats for recent day reviews.
type DayReviewStats struct {
	Streak   int     // consecutive days with a review ending today
	AvgScore float64 // average score over the period
	Count    int     // number of reviews in the period
}

// ── Day Reviews ──────────────────────────────────────────────────────────────

// SaveDayReview creates or updates the review for the given date (UPSERT).
func (s *DB) SaveDayReview(ctx context.Context, r DayReview) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO day_reviews
			(chat_id, date, planned, done, not_done, reason, score, mood, note, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id, date) DO UPDATE SET
			planned    = excluded.planned,
			done       = excluded.done,
			not_done   = excluded.not_done,
			reason     = excluded.reason,
			score      = excluded.score,
			mood       = excluded.mood,
			note       = excluded.note,
			updated_at = CURRENT_TIMESTAMP`,
		r.ChatID, r.Date, r.Planned, r.Done, r.NotDone,
		r.Reason, r.Score, r.Mood, r.Note,
	)
	return err
}

// GetDayReview returns the review for the given chat and date, or nil if absent.
func (s *DB) GetDayReview(ctx context.Context, chatID int64, date string) (*DayReview, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, chat_id, date, planned, done, not_done, reason, score, mood, note,
		       created_at, updated_at
		FROM day_reviews WHERE chat_id = ? AND date = ?`,
		chatID, date,
	)
	r, err := scanDayReview(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ListDayReviews returns the most recent `limit` reviews for a chat.
func (s *DB) ListDayReviews(ctx context.Context, chatID int64, limit int) ([]DayReview, error) {
	if limit <= 0 {
		limit = 7
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chat_id, date, planned, done, not_done, reason, score, mood, note,
		       created_at, updated_at
		FROM day_reviews WHERE chat_id = ?
		ORDER BY date DESC LIMIT ?`,
		chatID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	return scanDayReviews(rows)
}

// GetDayReviewStats returns streak and score statistics for the given chat.
// days controls the period for average score (default 30).
func (s *DB) GetDayReviewStats(ctx context.Context, chatID int64, days int) (DayReviewStats, error) {
	if days <= 0 {
		days = 30
	}

	// Streak: fetch last 90 review dates and count consecutive days from today.
	rows, err := s.db.QueryContext(ctx,
		"SELECT date FROM day_reviews WHERE chat_id = ? ORDER BY date DESC LIMIT 90",
		chatID,
	)
	if err != nil {
		return DayReviewStats{}, err
	}
	var dates []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			rows.Close() //nolint:errcheck
			return DayReviewStats{}, err
		}
		dates = append(dates, d)
	}
	rows.Close() //nolint:errcheck

	streak := calcStreak(dates)

	// Average score over the last `days` days.
	since := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	var avg sql.NullFloat64
	var cnt int
	err = s.db.QueryRowContext(ctx,
		"SELECT AVG(NULLIF(score,0)), COUNT(*) FROM day_reviews WHERE chat_id = ? AND date >= ?",
		chatID, since,
	).Scan(&avg, &cnt)
	if err != nil {
		return DayReviewStats{}, err
	}

	return DayReviewStats{
		Streak:   streak,
		AvgScore: avg.Float64,
		Count:    cnt,
	}, nil
}

// calcStreak counts consecutive days with a review ending today (or yesterday).
func calcStreak(datesDesc []string) int {
	if len(datesDesc) == 0 {
		return 0
	}
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	// Streak must start from today or yesterday.
	if datesDesc[0] != today && datesDesc[0] != yesterday {
		return 0
	}

	streak := 1
	for i := 1; i < len(datesDesc); i++ {
		prev, _ := time.Parse("2006-01-02", datesDesc[i-1])
		curr, _ := time.Parse("2006-01-02", datesDesc[i])
		if prev.Sub(curr) == 24*time.Hour {
			streak++
		} else {
			break
		}
	}
	return streak
}

func scanDayReview(row *sql.Row) (*DayReview, error) {
	var r DayReview
	err := row.Scan(
		&r.ID, &r.ChatID, &r.Date, &r.Planned, &r.Done, &r.NotDone,
		&r.Reason, &r.Score, &r.Mood, &r.Note, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func scanDayReviews(rows *sql.Rows) ([]DayReview, error) {
	var result []DayReview
	for rows.Next() {
		var r DayReview
		if err := rows.Scan(
			&r.ID, &r.ChatID, &r.Date, &r.Planned, &r.Done, &r.NotDone,
			&r.Reason, &r.Score, &r.Mood, &r.Note, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// ── Week Reviews ─────────────────────────────────────────────────────────────

// SaveWeekReview creates or updates the review for the given ISO week (UPSERT).
func (s *DB) SaveWeekReview(ctx context.Context, r WeekReview) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO week_reviews
			(chat_id, week, wins, fails, pattern, next_week_plan, score, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(chat_id, week) DO UPDATE SET
			wins           = excluded.wins,
			fails          = excluded.fails,
			pattern        = excluded.pattern,
			next_week_plan = excluded.next_week_plan,
			score          = excluded.score,
			updated_at     = CURRENT_TIMESTAMP`,
		r.ChatID, r.Week, r.Wins, r.Fails, r.Pattern, r.NextWeekPlan, r.Score,
	)
	return err
}

// GetWeekReview returns the review for the given chat and ISO week, or nil.
func (s *DB) GetWeekReview(ctx context.Context, chatID int64, week string) (*WeekReview, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, chat_id, week, wins, fails, pattern, next_week_plan, score,
		       created_at, updated_at
		FROM week_reviews WHERE chat_id = ? AND week = ?`,
		chatID, week,
	)
	var r WeekReview
	err := row.Scan(
		&r.ID, &r.ChatID, &r.Week, &r.Wins, &r.Fails, &r.Pattern,
		&r.NextWeekPlan, &r.Score, &r.CreatedAt, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// ── Objectives ───────────────────────────────────────────────────────────────

// CreateObjective inserts a new objective and returns its ID.
func (s *DB) CreateObjective(ctx context.Context, o Objective) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO objectives (chat_id, title, description, horizon, period, status, progress)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		o.ChatID, o.Title, o.Description, o.Horizon, o.Period,
		orDefault(o.Status, "active"), o.Progress,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListObjectives returns objectives for a chat filtered by status ("" = all active).
func (s *DB) ListObjectives(ctx context.Context, chatID int64, status string) ([]Objective, error) {
	var rows *sql.Rows
	var err error
	switch status {
	case "": // default: active only
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, chat_id, title, description, horizon, period, status, progress,
			       created_at, updated_at
			FROM objectives WHERE chat_id = ? AND status = 'active'
			ORDER BY created_at DESC`,
			chatID,
		)
	case "all":
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, chat_id, title, description, horizon, period, status, progress,
			       created_at, updated_at
			FROM objectives WHERE chat_id = ?
			ORDER BY status, created_at DESC`,
			chatID,
		)
	default:
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, chat_id, title, description, horizon, period, status, progress,
			       created_at, updated_at
			FROM objectives WHERE chat_id = ? AND status = ?
			ORDER BY created_at DESC`,
			chatID, status,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var result []Objective
	for rows.Next() {
		var o Objective
		if err := rows.Scan(
			&o.ID, &o.ChatID, &o.Title, &o.Description, &o.Horizon, &o.Period,
			&o.Status, &o.Progress, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load key results for each objective.
	for i := range result {
		krs, err := s.listKeyResults(ctx, result[i].ID)
		if err != nil {
			return nil, err
		}
		result[i].KeyResults = krs
	}
	return result, nil
}

// GetObjective returns a single objective with its key results, or nil.
func (s *DB) GetObjective(ctx context.Context, chatID, id int64) (*Objective, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, chat_id, title, description, horizon, period, status, progress,
		       created_at, updated_at
		FROM objectives WHERE id = ? AND chat_id = ?`,
		id, chatID,
	)
	var o Objective
	err := row.Scan(
		&o.ID, &o.ChatID, &o.Title, &o.Description, &o.Horizon, &o.Period,
		&o.Status, &o.Progress, &o.CreatedAt, &o.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.KeyResults, err = s.listKeyResults(ctx, o.ID)
	return &o, err
}

// UpdateObjective updates mutable fields of an objective.
func (s *DB) UpdateObjective(ctx context.Context, chatID, id int64, title, description, status string, progress int) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE objectives
		SET title = ?, description = ?, status = ?, progress = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND chat_id = ?`,
		title, description, status, progress, id, chatID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("objective %d not found", id)
	}
	return nil
}

// DeleteObjective deletes an objective and its key results (CASCADE).
func (s *DB) DeleteObjective(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM objectives WHERE id = ? AND chat_id = ?", id, chatID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("objective %d not found", id)
	}
	return nil
}

// ── Key Results ──────────────────────────────────────────────────────────────

// AddKeyResult adds a key result to an objective.
func (s *DB) AddKeyResult(ctx context.Context, kr KeyResult) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO key_results (objective_id, title, progress, deadline, note)
		VALUES (?, ?, ?, ?, ?)`,
		kr.ObjectiveID, kr.Title, kr.Progress, kr.Deadline, kr.Note,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetKeyResult returns a key result by ID, verifying it belongs to chatID via objectives join.
// Returns nil if not found or not owned by chatID.
func (s *DB) GetKeyResult(ctx context.Context, chatID, id int64) (*KeyResult, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT kr.id, kr.objective_id, kr.title, kr.progress, kr.deadline, kr.note, kr.updated_at
		FROM key_results kr
		JOIN objectives o ON kr.objective_id = o.id
		WHERE kr.id = ? AND o.chat_id = ?`,
		id, chatID,
	)
	var kr KeyResult
	err := row.Scan(&kr.ID, &kr.ObjectiveID, &kr.Title, &kr.Progress, &kr.Deadline, &kr.Note, &kr.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &kr, nil
}

// UpdateKeyResult updates progress and note of a key result, verifying chat ownership.
func (s *DB) UpdateKeyResult(ctx context.Context, chatID, id int64, progress int, note string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE key_results
		SET progress = ?, note = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND objective_id IN (SELECT id FROM objectives WHERE chat_id = ?)`,
		progress, note, id, chatID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("key result %d not found", id)
	}
	return nil
}

// DeleteKeyResult removes a key result, verifying chat ownership.
func (s *DB) DeleteKeyResult(ctx context.Context, chatID, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM key_results
		WHERE id = ? AND objective_id IN (SELECT id FROM objectives WHERE chat_id = ?)`,
		id, chatID,
	)
	return err
}

func (s *DB) listKeyResults(ctx context.Context, objectiveID int64) ([]KeyResult, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, objective_id, title, progress, deadline, note, updated_at
		FROM key_results WHERE objective_id = ? ORDER BY id`,
		objectiveID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var result []KeyResult
	for rows.Next() {
		var kr KeyResult
		if err := rows.Scan(
			&kr.ID, &kr.ObjectiveID, &kr.Title, &kr.Progress,
			&kr.Deadline, &kr.Note, &kr.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, kr)
	}
	return result, rows.Err()
}

// GetStaleObjectives returns active objectives for a chat that have not been
// updated since updatedBefore. Results are ordered oldest-first.
func (s *DB) GetStaleObjectives(ctx context.Context, chatID int64, updatedBefore time.Time) ([]Objective, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, chat_id, title, description, horizon, period, status, progress,
		       created_at, updated_at
		FROM objectives
		WHERE chat_id = ? AND status = 'active'
		  AND updated_at < ?
		ORDER BY updated_at ASC`,
		chatID, updatedBefore.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var result []Objective
	for rows.Next() {
		var o Objective
		if err := rows.Scan(
			&o.ID, &o.ChatID, &o.Title, &o.Description, &o.Horizon, &o.Period,
			&o.Status, &o.Progress, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
