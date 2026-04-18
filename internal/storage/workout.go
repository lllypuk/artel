package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// Workout represents a single training session.
type Workout struct {
	ID          int64
	ChatID      int64
	Date        string // "YYYY-MM-DD"
	Type        string // run/strength/yoga/cycling/…
	DurationMin int
	Note        string
	GoalID      *int64 // optional FK → objectives(id)
	Sets        []WorkoutSet
	CreatedAt   time.Time
}

// WorkoutSet represents one exercise entry inside a workout.
type WorkoutSet struct {
	ID         int64
	WorkoutID  int64
	Exercise   string
	Sets       int
	Reps       int
	WeightKg   float64
	DistanceKm float64
	Note       string
}

// WorkoutTemplate is a saved programme (e.g. "Push Day").
type WorkoutTemplate struct {
	ID        int64
	ChatID    int64
	Name      string
	Exercises []TemplateExercise
}

// TemplateExercise is one exercise entry inside a template.
type TemplateExercise struct {
	Name       string  `json:"name"`
	Sets       int     `json:"sets,omitempty"`
	Reps       int     `json:"reps,omitempty"`
	WeightKg   float64 `json:"weight_kg,omitempty"`
	DistanceKm float64 `json:"distance_km,omitempty"`
	Note       string  `json:"note,omitempty"`
}

// PersonalRecord holds the best set for an exercise.
type PersonalRecord struct {
	Exercise  string
	WeightKg  float64
	Reps      int
	WorkoutID int64
	Date      string
}

// WorkoutStats holds aggregated training statistics.
type WorkoutStats struct {
	TotalCount    int
	TotalMinutes  int
	AvgMinutes    int
	TypeBreakdown map[string]int // type → count
}

// ── Workout CRUD ──────────────────────────────────────────────────────────────

// CreateWorkout inserts a new workout and returns its ID.
func (s *DB) CreateWorkout(ctx context.Context, chatID int64, date, wType string, durationMin int, note string, goalID *int64) (int64, error) {
	var res sql.Result
	var err error
	if goalID != nil {
		res, err = s.db.ExecContext(ctx,
			`INSERT INTO workouts (chat_id, date, type, duration_min, note, goal_id)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			chatID, date, wType, durationMin, note, *goalID,
		)
	} else {
		res, err = s.db.ExecContext(ctx,
			`INSERT INTO workouts (chat_id, date, type, duration_min, note)
			 VALUES (?, ?, ?, ?, ?)`,
			chatID, date, wType, durationMin, note,
		)
	}
	if err != nil {
		return 0, fmt.Errorf("create workout: %w", err)
	}
	return res.LastInsertId()
}

// AddWorkoutSet appends a set to an existing workout.
func (s *DB) AddWorkoutSet(ctx context.Context, ws WorkoutSet) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO workout_sets (workout_id, exercise, sets, reps, weight_kg, distance_km, note)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ws.WorkoutID, ws.Exercise, ws.Sets, ws.Reps, ws.WeightKg, ws.DistanceKm, ws.Note,
	)
	if err != nil {
		return 0, fmt.Errorf("add workout set: %w", err)
	}
	return res.LastInsertId()
}

// GetWorkout returns a workout with its sets, or nil if not found.
func (s *DB) GetWorkout(ctx context.Context, chatID, id int64) (*Workout, error) {
	var w Workout
	var goalID sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT id, chat_id, date, type, duration_min, note, goal_id, created_at
		 FROM workouts WHERE id = ? AND chat_id = ?`,
		id, chatID,
	).Scan(&w.ID, &w.ChatID, &w.Date, &w.Type, &w.DurationMin, &w.Note, &goalID, &w.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workout: %w", err)
	}
	if goalID.Valid {
		w.GoalID = &goalID.Int64
	}
	sets, err := s.getWorkoutSets(ctx, w.ID)
	if err != nil {
		return nil, err
	}
	w.Sets = sets
	return &w, nil
}

// getWorkoutSets loads all sets for a workout (ordered by insertion).
func (s *DB) getWorkoutSets(ctx context.Context, workoutID int64) ([]WorkoutSet, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workout_id, exercise, sets, reps, weight_kg, distance_km, note
		 FROM workout_sets WHERE workout_id = ? ORDER BY id`,
		workoutID,
	)
	if err != nil {
		return nil, fmt.Errorf("get workout sets: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []WorkoutSet
	for rows.Next() {
		var ws WorkoutSet
		if err := rows.Scan(&ws.ID, &ws.WorkoutID, &ws.Exercise, &ws.Sets, &ws.Reps,
			&ws.WeightKg, &ws.DistanceKm, &ws.Note); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

// ListWorkouts returns workouts for a chat with optional filters.
// wType, fromDate, toDate may be empty strings to skip filtering.
// limit ≤ 0 means no limit.
func (s *DB) ListWorkouts(ctx context.Context, chatID int64, wType, fromDate, toDate string, limit int) ([]Workout, error) {
	q := `SELECT id, chat_id, date, type, duration_min, note, goal_id, created_at
	      FROM workouts WHERE chat_id = ?`
	args := []any{chatID}

	if wType != "" {
		q += ` AND type = ?`
		args = append(args, wType)
	}
	if fromDate != "" {
		q += ` AND date >= ?`
		args = append(args, fromDate)
	}
	if toDate != "" {
		q += ` AND date <= ?`
		args = append(args, toDate)
	}
	q += ` ORDER BY date DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit) //nolint:gosec // limit is a trusted Go int, not user SQL input
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list workouts: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var workouts []Workout
	for rows.Next() {
		var w Workout
		var goalID sql.NullInt64
		if err := rows.Scan(&w.ID, &w.ChatID, &w.Date, &w.Type, &w.DurationMin, &w.Note, &goalID, &w.CreatedAt); err != nil {
			return nil, err
		}
		if goalID.Valid {
			w.GoalID = &goalID.Int64
		}
		workouts = append(workouts, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Load sets for each workout.
	for i := range workouts {
		sets, err := s.getWorkoutSets(ctx, workouts[i].ID)
		if err != nil {
			return nil, err
		}
		workouts[i].Sets = sets
	}
	return workouts, nil
}

// DeleteWorkout deletes a workout and cascades to its sets.
func (s *DB) DeleteWorkout(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM workouts WHERE id = ? AND chat_id = ?`, id, chatID)
	if err != nil {
		return fmt.Errorf("delete workout: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("workout %d not found", id)
	}
	return nil
}

// ── Statistics (H-2) ──────────────────────────────────────────────────────────

// GetWorkoutStats computes aggregated stats over a date range.
func (s *DB) GetWorkoutStats(ctx context.Context, chatID int64, fromDate, toDate string) (*WorkoutStats, error) {
	q := `SELECT type, duration_min FROM workouts WHERE chat_id = ?`
	args := []any{chatID}
	if fromDate != "" {
		q += ` AND date >= ?`
		args = append(args, fromDate)
	}
	if toDate != "" {
		q += ` AND date <= ?`
		args = append(args, toDate)
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("workout stats: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	stats := &WorkoutStats{TypeBreakdown: make(map[string]int)}
	for rows.Next() {
		var wType string
		var dur int
		if err := rows.Scan(&wType, &dur); err != nil {
			return nil, err
		}
		stats.TotalCount++
		stats.TotalMinutes += dur
		stats.TypeBreakdown[wType]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if stats.TotalCount > 0 {
		stats.AvgMinutes = stats.TotalMinutes / stats.TotalCount
	}
	return stats, nil
}

// ── Personal Records (H-4) ────────────────────────────────────────────────────

// GetPersonalRecords returns the max-weight set per exercise.
// When exercise is non-empty, it filters to that exercise only.
// In SQLite, non-aggregated columns (reps, workout_id, date) are taken from
// the row that contains MAX(weight_kg) — this is guaranteed SQLite behaviour.
func (s *DB) GetPersonalRecords(ctx context.Context, chatID int64, exercise string) ([]PersonalRecord, error) {
	q := `SELECT ws.exercise,
	             MAX(ws.weight_kg) AS max_weight,
	             ws.reps,
	             w.id            AS workout_id,
	             w.date
	      FROM workout_sets ws
	      JOIN workouts w ON w.id = ws.workout_id
	      WHERE w.chat_id = ? AND ws.weight_kg > 0`
	args := []any{chatID}

	if exercise != "" {
		q += ` AND LOWER(ws.exercise) = LOWER(?)`
		args = append(args, exercise)
	}
	q += ` GROUP BY ws.exercise ORDER BY max_weight DESC`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get PRs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var prs []PersonalRecord
	for rows.Next() {
		var pr PersonalRecord
		if err := rows.Scan(&pr.Exercise, &pr.WeightKg, &pr.Reps, &pr.WorkoutID, &pr.Date); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

// GetExerciseHistory returns the best weight per session for an exercise
// (most recent first, up to 20 sessions).
func (s *DB) GetExerciseHistory(ctx context.Context, chatID int64, exercise string) ([]PersonalRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ws.exercise,
		       MAX(ws.weight_kg) AS max_weight,
		       ws.reps,
		       w.id,
		       w.date
		FROM workout_sets ws
		JOIN workouts w ON w.id = ws.workout_id
		WHERE w.chat_id = ? AND LOWER(ws.exercise) = LOWER(?) AND ws.weight_kg > 0
		GROUP BY w.id, ws.exercise
		ORDER BY w.date DESC
		LIMIT 20`,
		chatID, exercise,
	)
	if err != nil {
		return nil, fmt.Errorf("get exercise history: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var history []PersonalRecord
	for rows.Next() {
		var pr PersonalRecord
		if err := rows.Scan(&pr.Exercise, &pr.WeightKg, &pr.Reps, &pr.WorkoutID, &pr.Date); err != nil {
			return nil, err
		}
		history = append(history, pr)
	}
	return history, rows.Err()
}

// GetNewPRsFromWorkout returns exercises from the given workout where the
// recorded weight is strictly greater than any previous best for that exercise
// (i.e. a genuine new personal record). The current workout is excluded from
// the historical max so that repeating the same weight is never announced as a PR.
func (s *DB) GetNewPRsFromWorkout(ctx context.Context, chatID, workoutID int64) ([]PersonalRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ws.exercise,
		       ws.weight_kg,
		       ws.reps,
		       ws.workout_id,
		       w.date
		FROM workout_sets ws
		JOIN workouts w ON w.id = ws.workout_id
		WHERE ws.workout_id = ?
		  AND ws.weight_kg > 0
		  AND ws.weight_kg > (
		      SELECT COALESCE(MAX(ws2.weight_kg), 0)
		      FROM workout_sets ws2
		      JOIN workouts w2 ON w2.id = ws2.workout_id
		      WHERE w2.chat_id = ?
		        AND LOWER(ws2.exercise) = LOWER(ws.exercise)
		        AND ws2.workout_id != ?
		  )
		GROUP BY ws.exercise`,
		workoutID, chatID, workoutID,
	)
	if err != nil {
		return nil, fmt.Errorf("get new PRs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var prs []PersonalRecord
	for rows.Next() {
		var pr PersonalRecord
		if err := rows.Scan(&pr.Exercise, &pr.WeightKg, &pr.Reps, &pr.WorkoutID, &pr.Date); err != nil {
			return nil, err
		}
		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

// ── Workout Templates (H-3) ───────────────────────────────────────────────────

// CreateWorkoutTemplate saves (or replaces) a template.
func (s *DB) CreateWorkoutTemplate(ctx context.Context, chatID int64, name string, exercises []TemplateExercise) (int64, error) {
	data, err := json.Marshal(exercises)
	if err != nil {
		return 0, fmt.Errorf("marshal exercises: %w", err)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO workout_templates (chat_id, name, exercises) VALUES (?, ?, ?)
		 ON CONFLICT(chat_id, name) DO UPDATE SET exercises = excluded.exercises`,
		chatID, name, string(data),
	)
	if err != nil {
		return 0, fmt.Errorf("create template: %w", err)
	}
	return res.LastInsertId()
}

// ListWorkoutTemplates returns all templates for a chat.
func (s *DB) ListWorkoutTemplates(ctx context.Context, chatID int64) ([]WorkoutTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, name, exercises FROM workout_templates WHERE chat_id = ? ORDER BY name`,
		chatID,
	)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var templates []WorkoutTemplate
	for rows.Next() {
		var t WorkoutTemplate
		var exercisesJSON string
		if err := rows.Scan(&t.ID, &t.ChatID, &t.Name, &exercisesJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(exercisesJSON), &t.Exercises); err != nil {
			return nil, fmt.Errorf("unmarshal exercises for template %q: %w", t.Name, err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

// GetWorkoutTemplate returns a template by name, or nil if not found.
func (s *DB) GetWorkoutTemplate(ctx context.Context, chatID int64, name string) (*WorkoutTemplate, error) {
	var t WorkoutTemplate
	var exercisesJSON string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, chat_id, name, exercises FROM workout_templates WHERE chat_id = ? AND name = ?`,
		chatID, name,
	).Scan(&t.ID, &t.ChatID, &t.Name, &exercisesJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}
	if err := json.Unmarshal([]byte(exercisesJSON), &t.Exercises); err != nil {
		return nil, fmt.Errorf("unmarshal exercises for template %q: %w", t.Name, err)
	}
	return &t, nil
}

// CountWorkoutsForGoal returns the number of workouts linked to a specific goal.
func (s *DB) CountWorkoutsForGoal(ctx context.Context, chatID, goalID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM workouts WHERE chat_id = ? AND goal_id = ?`,
		chatID, goalID,
	).Scan(&count)
	return count, err
}

// DeleteWorkoutTemplate deletes a template by id.
func (s *DB) DeleteWorkoutTemplate(ctx context.Context, chatID, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM workout_templates WHERE id = ? AND chat_id = ?`, id, chatID)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("template %d not found", id)
	}
	return nil
}
