package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// DisciplineTool manages daily and weekly self-reviews.
// Covers D-1 (CRUD), D-2 (day review), D-3 (week review), D-4 (history/patterns), D-8 (dashboard).
type DisciplineTool struct {
	db *storage.DB
}

// NewDisciplineTool creates a new discipline tool.
func NewDisciplineTool(db *storage.DB) *DisciplineTool {
	return &DisciplineTool{db: db}
}

type disciplineArgs struct {
	Action string `json:"action"`
	// Day review fields
	Date    string `json:"date"`     // "2026-04-18", default: today
	Planned string `json:"planned"`  // что планировал
	Done    string `json:"done"`     // что сделал
	NotDone string `json:"not_done"` // что не сделал
	Reason  string `json:"reason"`   // почему не сделал
	Score   int    `json:"score"`    // 1-10
	Mood    string `json:"mood"`     // хорошее / нейтральное / плохое
	Note    string `json:"note"`     // свободная заметка
	// Week review fields
	Week         string `json:"week"`           // "2026-W16", default: current
	Wins         string `json:"wins"`           // что получилось
	Fails        string `json:"fails"`          // что не получилось
	Pattern      string `json:"pattern"`        // инсайт недели
	NextWeekPlan string `json:"next_week_plan"` // план на следующую неделю
	// List/stats params
	Limit  int   `json:"limit"` // for day_list (default 7)
	Days   int   `json:"days"`  // for day_stats period (default 30)
	ChatID int64 `json:"chat_id"`
}

func (t *DisciplineTool) Name() string { return "discipline" }

func (t *DisciplineTool) Description() string {
	return "Manage daily and weekly self-reviews for discipline tracking.\n" +
		"Actions:\n" +
		"  day_save   — save/update today's (or given date) review: planned, done, not_done, reason, score (1-10), mood, note.\n" +
		"  day_get    — get review for a date (default: today). Returns full review.\n" +
		"  day_list   — list recent reviews (limit, default 7).\n" +
		"  day_stats  — streak of consecutive review days, avg score, review count over last N days.\n" +
		"  week_save  — save/update weekly review: wins, fails, pattern, next_week_plan, score (1-10).\n" +
		"  week_get   — get weekly review (week like '2026-W16', default: current week).\n" +
		"  dashboard  — combined overview: day streak, avg score (7d), current week review status."
}

func (t *DisciplineTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"day_save", "day_get", "day_list", "day_stats",
					"week_save", "week_get", "dashboard",
				},
				"description": "Action to perform",
			},
			"date": map[string]any{
				"type":        "string",
				"description": "Date in YYYY-MM-DD format (default: today)",
			},
			"planned": map[string]any{
				"type":        "string",
				"description": "What was planned for the day",
			},
			"done": map[string]any{
				"type":        "string",
				"description": "What was actually done",
			},
			"not_done": map[string]any{
				"type":        "string",
				"description": "What was not done",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Why something was not done",
			},
			"score": map[string]any{
				"type":        "integer",
				"description": "Day score 1-10",
			},
			"mood": map[string]any{
				"type":        "string",
				"description": "Mood: хорошее / нейтральное / плохое",
			},
			"note": map[string]any{
				"type":        "string",
				"description": "Free-form note",
			},
			"week": map[string]any{
				"type":        "string",
				"description": "ISO week like '2026-W16' (default: current week)",
			},
			"wins": map[string]any{
				"type":        "string",
				"description": "Weekly wins (week_save)",
			},
			"fails": map[string]any{
				"type":        "string",
				"description": "Weekly fails (week_save)",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Main insight or pattern of the week (week_save)",
			},
			"next_week_plan": map[string]any{
				"type":        "string",
				"description": "Plan for next week (week_save)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of recent reviews to return (day_list, default 7)",
			},
			"days": map[string]any{
				"type":        "integer",
				"description": "Period in days for statistics (day_stats, default 30)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *DisciplineTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a disciplineArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.ChatID == 0 {
		if chatID, ok := ctx.Value(ChatIDKey).(int64); ok {
			a.ChatID = chatID
		}
	}

	switch a.Action {
	case "day_save":
		return t.daySave(ctx, a)
	case "day_get":
		return t.dayGet(ctx, a)
	case "day_list":
		return t.dayList(ctx, a)
	case "day_stats":
		return t.dayStats(ctx, a)
	case "week_save":
		return t.weekSave(ctx, a)
	case "week_get":
		return t.weekGet(ctx, a)
	case "dashboard":
		return t.dashboard(ctx, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (t *DisciplineTool) daySave(ctx context.Context, a disciplineArgs) (string, error) {
	date := a.Date
	if date == "" {
		date = today()
	}
	r := storage.DayReview{
		ChatID:  a.ChatID,
		Date:    date,
		Planned: a.Planned,
		Done:    a.Done,
		NotDone: a.NotDone,
		Reason:  a.Reason,
		Score:   a.Score,
		Mood:    a.Mood,
		Note:    a.Note,
	}
	if err := t.db.SaveDayReview(ctx, r); err != nil {
		return "", fmt.Errorf("save day review: %w", err)
	}
	return fmt.Sprintf("✅ Ревью дня за %s сохранено.", date), nil
}

func (t *DisciplineTool) dayGet(ctx context.Context, a disciplineArgs) (string, error) {
	date := a.Date
	if date == "" {
		date = today()
	}
	r, err := t.db.GetDayReview(ctx, a.ChatID, date)
	if err != nil {
		return "", err
	}
	if r == nil {
		return fmt.Sprintf("Ревью за %s не найдено.", date), nil
	}
	return formatDayReview(r), nil
}

func (t *DisciplineTool) dayList(ctx context.Context, a disciplineArgs) (string, error) {
	limit := a.Limit
	if limit <= 0 {
		limit = 7
	}
	reviews, err := t.db.ListDayReviews(ctx, a.ChatID, limit)
	if err != nil {
		return "", err
	}
	if len(reviews) == 0 {
		return "Нет ни одного ревью дня.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 Последние %d ревью дня:\n\n", len(reviews))
	for _, r := range reviews {
		fmt.Fprintf(&sb, "**%s**", r.Date)
		if r.Score > 0 {
			fmt.Fprintf(&sb, " — %d/10", r.Score)
		}
		if r.Mood != "" {
			fmt.Fprintf(&sb, " (%s)", r.Mood)
		}
		sb.WriteString("\n")
		if r.Done != "" {
			fmt.Fprintf(&sb, "  ✅ %s\n", r.Done)
		}
		if r.NotDone != "" {
			fmt.Fprintf(&sb, "  ❌ %s\n", r.NotDone)
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func (t *DisciplineTool) dayStats(ctx context.Context, a disciplineArgs) (string, error) {
	days := a.Days
	if days <= 0 {
		days = 30
	}
	stats, err := t.db.GetDayReviewStats(ctx, a.ChatID, days)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "📊 Статистика ревью дня:\n\n")
	fmt.Fprintf(&sb, "🔥 Стрик: %d дней подряд\n", stats.Streak)
	if stats.Count > 0 {
		fmt.Fprintf(&sb, "⭐ Средняя оценка (за %d дней): %.1f/10\n", days, stats.AvgScore)
		fmt.Fprintf(&sb, "📝 Ревью за период: %d из %d дней\n", stats.Count, days)
	} else {
		fmt.Fprintf(&sb, "Нет ревью за последние %d дней.\n", days)
	}
	return sb.String(), nil
}

func (t *DisciplineTool) weekSave(ctx context.Context, a disciplineArgs) (string, error) {
	week := a.Week
	if week == "" {
		week = currentWeek()
	}
	r := storage.WeekReview{
		ChatID:       a.ChatID,
		Week:         week,
		Wins:         a.Wins,
		Fails:        a.Fails,
		Pattern:      a.Pattern,
		NextWeekPlan: a.NextWeekPlan,
		Score:        a.Score,
	}
	if err := t.db.SaveWeekReview(ctx, r); err != nil {
		return "", fmt.Errorf("save week review: %w", err)
	}
	return fmt.Sprintf("✅ Ревью недели %s сохранено.", week), nil
}

func (t *DisciplineTool) weekGet(ctx context.Context, a disciplineArgs) (string, error) {
	week := a.Week
	if week == "" {
		week = currentWeek()
	}
	r, err := t.db.GetWeekReview(ctx, a.ChatID, week)
	if err != nil {
		return "", err
	}
	if r == nil {
		return fmt.Sprintf("Ревью недели %s не найдено.", week), nil
	}
	return formatWeekReview(r), nil
}

func (t *DisciplineTool) dashboard(ctx context.Context, a disciplineArgs) (string, error) {
	stats, err := t.db.GetDayReviewStats(ctx, a.ChatID, 7)
	if err != nil {
		return "", err
	}
	week := currentWeek()
	wr, err := t.db.GetWeekReview(ctx, a.ChatID, week)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("📊 **Дашборд дисциплины**\n\n")
	fmt.Fprintf(&sb, "🔥 Стрик ревью: %d дней\n", stats.Streak)
	if stats.Count > 0 {
		fmt.Fprintf(&sb, "⭐ Средняя оценка (7 дней): %.1f/10\n", stats.AvgScore)
		fmt.Fprintf(&sb, "📝 Ревью за неделю: %d/7\n", stats.Count)
	} else {
		sb.WriteString("📝 За последние 7 дней ревью нет\n")
	}
	sb.WriteString("\n")
	if wr != nil {
		fmt.Fprintf(&sb, "📅 Ревью недели %s: ", week)
		if wr.Score > 0 {
			fmt.Fprintf(&sb, "%d/10", wr.Score)
		}
		sb.WriteString(" ✅ заполнено\n")
	} else {
		fmt.Fprintf(&sb, "📅 Ревью недели %s: ❌ не заполнено\n", week)
	}
	return sb.String(), nil
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func formatDayReview(r *storage.DayReview) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 **Ревью дня — %s**\n\n", r.Date)
	if r.Score > 0 {
		fmt.Fprintf(&sb, "⭐ Оценка: %d/10\n", r.Score)
	}
	if r.Mood != "" {
		fmt.Fprintf(&sb, "😊 Настроение: %s\n", r.Mood)
	}
	if r.Planned != "" {
		fmt.Fprintf(&sb, "\n📌 Планировал:\n%s\n", r.Planned)
	}
	if r.Done != "" {
		fmt.Fprintf(&sb, "\n✅ Сделал:\n%s\n", r.Done)
	}
	if r.NotDone != "" {
		fmt.Fprintf(&sb, "\n❌ Не сделал:\n%s\n", r.NotDone)
	}
	if r.Reason != "" {
		fmt.Fprintf(&sb, "\n💭 Причина:\n%s\n", r.Reason)
	}
	if r.Note != "" {
		fmt.Fprintf(&sb, "\n📝 Заметка:\n%s\n", r.Note)
	}
	return sb.String()
}

func formatWeekReview(r *storage.WeekReview) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 **Ревью недели — %s**\n\n", r.Week)
	if r.Score > 0 {
		fmt.Fprintf(&sb, "⭐ Оценка недели: %d/10\n\n", r.Score)
	}
	if r.Wins != "" {
		fmt.Fprintf(&sb, "🏆 Победы:\n%s\n\n", r.Wins)
	}
	if r.Fails != "" {
		fmt.Fprintf(&sb, "❌ Провалы:\n%s\n\n", r.Fails)
	}
	if r.Pattern != "" {
		fmt.Fprintf(&sb, "💡 Инсайт недели:\n%s\n\n", r.Pattern)
	}
	if r.NextWeekPlan != "" {
		fmt.Fprintf(&sb, "🎯 План на следующую неделю:\n%s\n", r.NextWeekPlan)
	}
	return sb.String()
}

// ── Time helpers ─────────────────────────────────────────────────────────────

func today() string {
	return time.Now().Format("2006-01-02")
}

func currentWeek() string {
	year, week := time.Now().ISOWeek()
	return fmt.Sprintf("%d-W%02d", year, week)
}
