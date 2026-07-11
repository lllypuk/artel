package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/storage"
)

// WorkoutTool provides training log, templates and personal-record tracking.
// Covers H-1 (log), H-2 (history/stats), H-3 (templates), H-4 (PRs), H-5 (goal link).
type WorkoutTool struct {
	db *storage.DB
}

// NewWorkoutTool creates a new workout tool.
func NewWorkoutTool(db *storage.DB) *WorkoutTool {
	return &WorkoutTool{db: db}
}

// ── args ─────────────────────────────────────────────────────────────────────

type workoutSetInput struct {
	Exercise   string  `json:"exercise"`
	Sets       int     `json:"sets"`
	Reps       int     `json:"reps"`
	WeightKg   float64 `json:"weight_kg"`
	DistanceKm float64 `json:"distance_km"`
	Note       string  `json:"note"`
}

type workoutArgs struct {
	Action      string            `json:"action"`
	ID          int64             `json:"id"`
	Date        string            `json:"date"`         // YYYY-MM-DD, default today
	Type        string            `json:"type"`         // run/strength/yoga/…
	DurationMin int               `json:"duration_min"` // minutes
	Note        string            `json:"note"`
	GoalID      *int64            `json:"goal_id"`  // optional FK → objectives(id)
	Sets        []workoutSetInput `json:"sets"`     // for log
	Exercise    string            `json:"exercise"` // for pr / pr_history
	From        string            `json:"from"`     // YYYY-MM-DD
	To          string            `json:"to"`       // YYYY-MM-DD
	Limit       int               `json:"limit"`    // default 10
	// template fields
	Name      string                     `json:"name"`
	Exercises []storage.TemplateExercise `json:"exercises"`
	ChatID    int64                      `json:"chat_id"`
}

// ── Tool interface ────────────────────────────────────────────────────────────

func (t *WorkoutTool) Name() string { return "workout" }

func (t *WorkoutTool) Description() string {
	return "Training log with history, templates and personal records.\n" +
		"Actions:\n" +
		"  log           — record a workout session. Params: type (required, e.g. strength/run/yoga), " +
		"date (YYYY-MM-DD, default today), duration_min, note, goal_id (FK to objectives), " +
		"sets (array of {exercise, sets, reps, weight_kg, distance_km, note}).\n" +
		"  list          — list recent workouts. Params: type?, from? (YYYY-MM-DD), to?, limit (default 10).\n" +
		"  get           — get full workout details by id.\n" +
		"  delete        — delete workout by id.\n" +
		"  stats         — training statistics (count, total/avg duration, type breakdown). Params: from?, to?.\n" +
		"  template_save — save a workout template. Params: name (required), " +
		"exercises (array of {name, sets, reps, weight_kg, distance_km, note}).\n" +
		"  template_list — list all saved templates.\n" +
		"  template_get  — get a template by name.\n" +
		"  template_delete — delete a template by id.\n" +
		"  pr            — show personal records (max weight per exercise). Params: exercise? (filter to one exercise).\n" +
		"  pr_history    — show weight progression for an exercise over time. Params: exercise (required)."
}

func (t *WorkoutTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"log", "list", "get", "delete", "stats",
					"template_save", "template_list", "template_get", "template_delete",
					"pr", "pr_history",
				},
				"description": "Action to perform",
			},
			"id": map[string]any{
				"type":        "integer",
				"description": "Workout or template ID (for get, delete, template_delete)",
			},
			"date": map[string]any{
				"type":        "string",
				"description": "Workout date YYYY-MM-DD (default: today)",
			},
			"type": map[string]any{
				"type":        "string",
				"description": "Workout type: strength, run, yoga, cycling, swimming, etc.",
			},
			"duration_min": map[string]any{
				"type":        "integer",
				"description": "Duration in minutes",
			},
			"note": map[string]any{
				"type":        "string",
				"description": "Free-text note",
			},
			"goal_id": map[string]any{
				"type":        "integer",
				"description": "Link to objective ID from goals tool (H-5)",
			},
			"sets": map[string]any{
				"type":        "array",
				"description": "List of exercises/sets for this workout",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"exercise":    map[string]any{"type": "string", "description": "Exercise name"},
						"sets":        map[string]any{"type": "integer", "description": "Number of sets"},
						"reps":        map[string]any{"type": "integer", "description": "Reps per set"},
						"weight_kg":   map[string]any{"type": "number", "description": "Weight in kg"},
						"distance_km": map[string]any{"type": "number", "description": "Distance in km (for cardio)"},
						"note":        map[string]any{"type": "string", "description": "Set note"},
					},
					"required": []string{"exercise"},
				},
			},
			"exercise": map[string]any{
				"type":        "string",
				"description": "Exercise name for pr / pr_history",
			},
			"from": map[string]any{
				"type":        "string",
				"description": "Start date YYYY-MM-DD for list/stats",
			},
			"to": map[string]any{
				"type":        "string",
				"description": "End date YYYY-MM-DD for list/stats",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Max results to return for list (default 10)",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Template name for template_save / template_get",
			},
			"exercises": map[string]any{
				"type":        "array",
				"description": "Exercise list for template_save",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"sets":        map[string]any{"type": "integer"},
						"reps":        map[string]any{"type": "integer"},
						"weight_kg":   map[string]any{"type": "number"},
						"distance_km": map[string]any{"type": "number"},
						"note":        map[string]any{"type": "string"},
					},
					"required": []string{"name"},
				},
			},
		},
		"required": []string{"action"},
	}
}

func (t *WorkoutTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a workoutArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.ChatID == 0 {
		if chatID, ok := ctx.Value(ChatIDKey).(int64); ok {
			a.ChatID = chatID
		}
	}

	switch a.Action {
	case "log":
		return t.execLog(ctx, a)
	case "list":
		return t.execList(ctx, a)
	case "get":
		return t.execGet(ctx, a)
	case "delete":
		return t.execDelete(ctx, a)
	case "stats":
		return t.execStats(ctx, a)
	case "template_save":
		return t.execTemplateSave(ctx, a)
	case "template_list":
		return t.execTemplateList(ctx, a)
	case "template_get":
		return t.execTemplateGet(ctx, a)
	case "template_delete":
		return t.execTemplateDelete(ctx, a)
	case "pr":
		return t.execPR(ctx, a)
	case "pr_history":
		return t.execPRHistory(ctx, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

// ── H-1: log ─────────────────────────────────────────────────────────────────

func (t *WorkoutTool) execLog(ctx context.Context, a workoutArgs) (string, error) {
	if a.Type == "" {
		return "", fmt.Errorf("type is required (e.g. strength, run, yoga)")
	}
	date := a.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("invalid date %q: use YYYY-MM-DD format", date)
	}
	// Validate goal_id before persisting (H-5): must exist and belong to this chat.
	if a.GoalID != nil {
		if err := t.validateGoalID(ctx, a.ChatID, *a.GoalID); err != nil {
			return "", err
		}
	}

	id, err := t.db.CreateWorkout(ctx, a.ChatID, date, a.Type, a.DurationMin, a.Note, a.GoalID)
	if err != nil {
		return "", fmt.Errorf("create workout: %w", err)
	}
	if err := t.insertSets(ctx, id, a.Sets); err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "✅ Тренировка записана (id: %d)\n", id)
	fmt.Fprintf(&sb, "📅 %s | 🏋️ %s", date, a.Type)
	if a.DurationMin > 0 {
		fmt.Fprintf(&sb, " | ⏱ %d мин", a.DurationMin)
	}
	if a.GoalID != nil {
		fmt.Fprintf(&sb, " | 🎯 цель #%d", *a.GoalID)
	}
	writeLogSets(&sb, a.Sets)
	if a.Note != "" {
		fmt.Fprintf(&sb, "\n💬 %s", a.Note)
	}
	writeNewPRs(&sb, t.db, ctx, a.ChatID, id)
	return sb.String(), nil
}

// validateGoalID checks that a goal with the given ID exists and belongs to chatID.
func (t *WorkoutTool) validateGoalID(ctx context.Context, chatID, goalID int64) error {
	obj, err := t.db.GetObjective(ctx, chatID, goalID)
	if err != nil {
		return fmt.Errorf("validate goal_id: %w", err)
	}
	if obj == nil {
		return fmt.Errorf("цель #%d не найдена или не принадлежит вам", goalID)
	}
	return nil
}

// insertSets writes workout sets into the DB (skips entries without exercise name).
func (t *WorkoutTool) insertSets(ctx context.Context, workoutID int64, sets []workoutSetInput) error {
	for _, s := range sets {
		if s.Exercise == "" {
			continue
		}
		_, err := t.db.AddWorkoutSet(ctx, storage.WorkoutSet{
			WorkoutID:  workoutID,
			Exercise:   s.Exercise,
			Sets:       s.Sets,
			Reps:       s.Reps,
			WeightKg:   s.WeightKg,
			DistanceKm: s.DistanceKm,
			Note:       s.Note,
		})
		if err != nil {
			return fmt.Errorf("add set %q: %w", s.Exercise, err)
		}
	}
	return nil
}

// writeLogSets appends exercise summary lines to sb.
func writeLogSets(sb *strings.Builder, sets []workoutSetInput) {
	if len(sets) == 0 {
		return
	}
	sb.WriteString("\n\n**Упражнения:**\n")
	for _, s := range sets {
		if s.Exercise == "" {
			continue
		}
		fmt.Fprintf(sb, "• %s", s.Exercise)
		if s.Sets > 0 {
			fmt.Fprintf(sb, ": %d×%d", s.Sets, s.Reps)
		}
		if s.WeightKg > 0 {
			fmt.Fprintf(sb, " @ %.1f кг", s.WeightKg)
		}
		if s.DistanceKm > 0 {
			fmt.Fprintf(sb, " %.2f км", s.DistanceKm)
		}
		if s.Note != "" {
			fmt.Fprintf(sb, " (%s)", s.Note)
		}
		sb.WriteString("\n")
	}
}

// writeNewPRs appends personal-record announcements to sb (silently skips errors).
func writeNewPRs(sb *strings.Builder, db *storage.DB, ctx context.Context, chatID, workoutID int64) {
	prs, err := db.GetNewPRsFromWorkout(ctx, chatID, workoutID)
	if err != nil || len(prs) == 0 {
		return
	}
	sb.WriteString("\n\n🏆 **Новые рекорды:**\n")
	for _, pr := range prs {
		fmt.Fprintf(sb, "• %s — %.1f кг", pr.Exercise, pr.WeightKg)
		if pr.Reps > 0 {
			fmt.Fprintf(sb, " × %d повт", pr.Reps)
		}
		sb.WriteString("\n")
	}
}

// ── H-2: list ────────────────────────────────────────────────────────────────

func (t *WorkoutTool) execList(ctx context.Context, a workoutArgs) (string, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}
	workouts, err := t.db.ListWorkouts(ctx, a.ChatID, a.Type, a.From, a.To, limit)
	if err != nil {
		return "", fmt.Errorf("list workouts: %w", err)
	}
	if len(workouts) == 0 {
		return "Тренировок не найдено.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "🏋️ **Тренировки** (%d):\n\n", len(workouts))
	for _, w := range workouts {
		fmt.Fprintf(&sb, "**[%d] %s — %s**", w.ID, w.Date, w.Type)
		if w.DurationMin > 0 {
			fmt.Fprintf(&sb, " (%d мин)", w.DurationMin)
		}
		if w.GoalID != nil {
			fmt.Fprintf(&sb, " 🎯#%d", *w.GoalID)
		}
		sb.WriteString("\n")
		for _, s := range w.Sets {
			fmt.Fprintf(&sb, "  • %s", s.Exercise)
			if s.Sets > 0 {
				fmt.Fprintf(&sb, ": %d×%d", s.Sets, s.Reps)
			}
			if s.WeightKg > 0 {
				fmt.Fprintf(&sb, " @ %.1f кг", s.WeightKg)
			}
			if s.DistanceKm > 0 {
				fmt.Fprintf(&sb, " %.2f км", s.DistanceKm)
			}
			sb.WriteString("\n")
		}
		if w.Note != "" {
			fmt.Fprintf(&sb, "  💬 %s\n", w.Note)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ── get ───────────────────────────────────────────────────────────────────────

func (t *WorkoutTool) execGet(ctx context.Context, a workoutArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for get")
	}
	w, err := t.db.GetWorkout(ctx, a.ChatID, a.ID)
	if err != nil {
		return "", fmt.Errorf("get workout: %w", err)
	}
	if w == nil {
		return fmt.Sprintf("Тренировка %d не найдена.", a.ID), nil
	}
	return formatWorkout(w), nil
}

// ── delete ────────────────────────────────────────────────────────────────────

func (t *WorkoutTool) execDelete(ctx context.Context, a workoutArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for delete")
	}
	if err := t.db.DeleteWorkout(ctx, a.ChatID, a.ID); err != nil {
		return "", fmt.Errorf("delete workout: %w", err)
	}
	return fmt.Sprintf("🗑 Тренировка %d удалена.", a.ID), nil
}

// ── H-2: stats ───────────────────────────────────────────────────────────────

func (t *WorkoutTool) execStats(ctx context.Context, a workoutArgs) (string, error) {
	from, to := a.From, a.To
	// Default: current month if no range given.
	if from == "" && to == "" {
		now := time.Now()
		from = now.Format("2006-01") + "-01"
		to = now.Format("2006-01-02")
	}

	stats, err := t.db.GetWorkoutStats(ctx, a.ChatID, from, to)
	if err != nil {
		return "", fmt.Errorf("workout stats: %w", err)
	}

	if stats.TotalCount == 0 {
		return "Тренировок за указанный период не найдено.", nil
	}

	var sb strings.Builder
	sb.WriteString("📊 **Статистика тренировок**\n\n")
	if from != "" || to != "" {
		fmt.Fprintf(&sb, "📅 Период: %s — %s\n\n", from, to)
	}
	fmt.Fprintf(&sb, "🏋️ Всего тренировок: **%d**\n", stats.TotalCount)
	fmt.Fprintf(&sb, "⏱ Суммарно: **%d мин** (~%.1f ч)\n", stats.TotalMinutes, float64(stats.TotalMinutes)/60)
	fmt.Fprintf(&sb, "📈 Средняя длительность: **%d мин**\n", stats.AvgMinutes)
	if len(stats.TypeBreakdown) > 0 {
		sb.WriteString("\n**По типам:**\n")
		for tp, cnt := range stats.TypeBreakdown {
			fmt.Fprintf(&sb, "  • %s: %d\n", tp, cnt)
		}
	}
	return sb.String(), nil
}

// ── H-3: templates ───────────────────────────────────────────────────────────

func (t *WorkoutTool) execTemplateSave(ctx context.Context, a workoutArgs) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required for template_save")
	}
	if len(a.Exercises) == 0 {
		return "", fmt.Errorf("exercises are required for template_save")
	}
	id, err := t.db.CreateWorkoutTemplate(ctx, a.ChatID, a.Name, a.Exercises)
	if err != nil {
		return "", fmt.Errorf("save template: %w", err)
	}
	return fmt.Sprintf("✅ Шаблон «%s» сохранён (id: %d, упражнений: %d).", a.Name, id, len(a.Exercises)), nil
}

func (t *WorkoutTool) execTemplateList(ctx context.Context, a workoutArgs) (string, error) {
	templates, err := t.db.ListWorkoutTemplates(ctx, a.ChatID)
	if err != nil {
		return "", fmt.Errorf("list templates: %w", err)
	}
	if len(templates) == 0 {
		return "Шаблонов тренировок нет. Создай: template_save.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 **Шаблоны тренировок** (%d):\n\n", len(templates))
	for _, tmpl := range templates {
		fmt.Fprintf(&sb, "**[%d] %s** (%d упр.):\n", tmpl.ID, tmpl.Name, len(tmpl.Exercises))
		for _, ex := range tmpl.Exercises {
			fmt.Fprintf(&sb, "  • %s", ex.Name)
			if ex.Sets > 0 {
				fmt.Fprintf(&sb, ": %d×%d", ex.Sets, ex.Reps)
			}
			if ex.WeightKg > 0 {
				fmt.Fprintf(&sb, " @ %.1f кг", ex.WeightKg)
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func (t *WorkoutTool) execTemplateGet(ctx context.Context, a workoutArgs) (string, error) {
	if a.Name == "" {
		return "", fmt.Errorf("name is required for template_get")
	}
	tmpl, err := t.db.GetWorkoutTemplate(ctx, a.ChatID, a.Name)
	if err != nil {
		return "", fmt.Errorf("get template: %w", err)
	}
	if tmpl == nil {
		return fmt.Sprintf("Шаблон «%s» не найден.", a.Name), nil
	}
	return formatTemplate(tmpl), nil
}

func (t *WorkoutTool) execTemplateDelete(ctx context.Context, a workoutArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for template_delete")
	}
	if err := t.db.DeleteWorkoutTemplate(ctx, a.ChatID, a.ID); err != nil {
		return "", fmt.Errorf("delete template: %w", err)
	}
	return fmt.Sprintf("🗑 Шаблон %d удалён.", a.ID), nil
}

// ── H-4: personal records ────────────────────────────────────────────────────

func (t *WorkoutTool) execPR(ctx context.Context, a workoutArgs) (string, error) {
	prs, err := t.db.GetPersonalRecords(ctx, a.ChatID, a.Exercise)
	if err != nil {
		return "", fmt.Errorf("get PRs: %w", err)
	}
	if len(prs) == 0 {
		if a.Exercise != "" {
			return fmt.Sprintf("Рекордов по упражнению «%s» пока нет.", a.Exercise), nil
		}
		return "Личных рекордов пока нет. Залогируй тренировку с весами.", nil
	}

	var sb strings.Builder
	if a.Exercise != "" {
		fmt.Fprintf(&sb, "🏆 **Рекорд: %s**\n\n", a.Exercise)
	} else {
		fmt.Fprintf(&sb, "🏆 **Личные рекорды** (%d упражнений):\n\n", len(prs))
	}
	for _, pr := range prs {
		fmt.Fprintf(&sb, "• **%s** — %.1f кг", pr.Exercise, pr.WeightKg)
		if pr.Reps > 0 {
			fmt.Fprintf(&sb, " × %d", pr.Reps)
		}
		fmt.Fprintf(&sb, " (%s, тр.#%d)\n", pr.Date, pr.WorkoutID)
	}
	return sb.String(), nil
}

func (t *WorkoutTool) execPRHistory(ctx context.Context, a workoutArgs) (string, error) {
	if a.Exercise == "" {
		return "", fmt.Errorf("exercise is required for pr_history")
	}
	history, err := t.db.GetExerciseHistory(ctx, a.ChatID, a.Exercise)
	if err != nil {
		return "", fmt.Errorf("get exercise history: %w", err)
	}
	if len(history) == 0 {
		return fmt.Sprintf("История по упражнению «%s» пуста.", a.Exercise), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "📈 **Прогресс: %s** (последние %d сессий):\n\n", a.Exercise, len(history))
	for _, h := range history {
		fmt.Fprintf(&sb, "• %s — %.1f кг", h.Date, h.WeightKg)
		if h.Reps > 0 {
			fmt.Fprintf(&sb, " × %d", h.Reps)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func formatWorkout(w *storage.Workout) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🏋️ **[%d] %s — %s**\n", w.ID, w.Date, w.Type)
	if w.DurationMin > 0 {
		fmt.Fprintf(&sb, "⏱ Длительность: %d мин\n", w.DurationMin)
	}
	if w.GoalID != nil {
		fmt.Fprintf(&sb, "🎯 Цель: #%d\n", *w.GoalID)
	}
	if w.Note != "" {
		fmt.Fprintf(&sb, "💬 %s\n", w.Note)
	}
	if len(w.Sets) > 0 {
		sb.WriteString("\n**Упражнения:**\n")
		for _, s := range w.Sets {
			fmt.Fprintf(&sb, "• %s", s.Exercise)
			if s.Sets > 0 {
				fmt.Fprintf(&sb, ": %d×%d", s.Sets, s.Reps)
			}
			if s.WeightKg > 0 {
				fmt.Fprintf(&sb, " @ %.1f кг", s.WeightKg)
			}
			if s.DistanceKm > 0 {
				fmt.Fprintf(&sb, " %.2f км", s.DistanceKm)
			}
			if s.Note != "" {
				fmt.Fprintf(&sb, " (%s)", s.Note)
			}
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "\nСоздана: %s", w.CreatedAt.Format("2006-01-02 15:04"))
	return sb.String()
}

func formatTemplate(tmpl *storage.WorkoutTemplate) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 **[%d] %s** (%d упражнений):\n\n", tmpl.ID, tmpl.Name, len(tmpl.Exercises))
	for _, ex := range tmpl.Exercises {
		fmt.Fprintf(&sb, "• **%s**", ex.Name)
		if ex.Sets > 0 {
			fmt.Fprintf(&sb, ": %d×%d", ex.Sets, ex.Reps)
		}
		if ex.WeightKg > 0 {
			fmt.Fprintf(&sb, " @ %.1f кг", ex.WeightKg)
		}
		if ex.DistanceKm > 0 {
			fmt.Fprintf(&sb, " %.2f км", ex.DistanceKm)
		}
		if ex.Note != "" {
			fmt.Fprintf(&sb, " — %s", ex.Note)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
