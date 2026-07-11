package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/storage"
)

// GoalsTool manages objectives and key results (OKR) plus long/medium-term goals.
// Covers D-5 (OKR), D-6 (decomposition via LLM), D-7 (long/medium-term goals).
type GoalsTool struct {
	db *storage.DB
}

// NewGoalsTool creates a new goals tool.
func NewGoalsTool(db *storage.DB) *GoalsTool {
	return &GoalsTool{db: db}
}

type goalsArgs struct {
	Action      string `json:"action"`
	ID          int64  `json:"id"`          // objective id
	KRID        int64  `json:"kr_id"`       // key result id
	Title       string `json:"title"`       // objective title
	Description string `json:"description"` // objective description
	Horizon     string `json:"horizon"`     // year / quarter / month
	Period      string `json:"period"`      // "2026-Q2", "2026", "2026-04"
	Status      string `json:"status"`      // active/paused/done/archived
	Progress    *int   `json:"progress"`    // pointer: nil = not set, 0 = explicitly zero
	KRTitle     string `json:"kr_title"`    // key result title
	KRDeadline  string `json:"kr_deadline"` // "2026-06-30"
	KRNote      string `json:"kr_note"`     // key result note
	Filter      string `json:"filter"`      // for list: "all", "active"(default), "done", ...
	ChatID      int64  `json:"chat_id"`
}

func (t *GoalsTool) Name() string { return "goals" }

func (t *GoalsTool) Description() string {
	return "Manage goals and OKR (Objectives and Key Results).\n" +
		"Actions:\n" +
		"  create      — create a new objective. Params: title, description?, horizon (year/quarter/month), period (e.g. '2026-Q2').\n" +
		"  list        — list objectives. filter: 'active'(default), 'done', 'archived', 'paused', 'all'.\n" +
		"  get         — get objective details with key results by id.\n" +
		"  update      — update objective: title, description, status, progress by id.\n" +
		"  delete      — delete objective by id.\n" +
		"  set_progress — set progress % (0-100) on an objective by id.\n" +
		"  kr_add      — add a key result to an objective: id, kr_title, kr_deadline?, kr_note?.\n" +
		"  kr_update   — update key result progress and/or note: kr_id, progress, kr_note?.\n" +
		"  kr_delete   — delete a key result by kr_id.\n" +
		"  dashboard   — OKR dashboard: active objectives with avg progress, top goals.\n" +
		"Tip: use 'goals' + 'reminders' together to decompose objectives into weekly tasks."
}

func (t *GoalsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"create", "list", "get", "update", "delete", "set_progress",
					"kr_add", "kr_update", "kr_delete", "dashboard",
				},
				"description": "Action to perform",
			},
			"id": map[string]any{
				"type":        "integer",
				"description": "Objective ID",
			},
			"kr_id": map[string]any{
				"type":        "integer",
				"description": "Key Result ID",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Objective title",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Objective description",
			},
			"horizon": map[string]any{
				"type":        "string",
				"enum":        []string{"year", "quarter", "month"},
				"description": "Time horizon: year / quarter / month",
			},
			"period": map[string]any{
				"type":        "string",
				"description": "Period label: '2026-Q2' for quarter, '2026' for year, '2026-04' for month",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"active", "paused", "done", "archived"},
				"description": "Objective status",
			},
			"progress": map[string]any{
				"type":        "integer",
				"description": "Progress 0-100; omit to keep current value (for set_progress, update, kr_update)",
			},
			"kr_title": map[string]any{
				"type":        "string",
				"description": "Key Result title (for kr_add)",
			},
			"kr_deadline": map[string]any{
				"type":        "string",
				"description": "Key Result deadline YYYY-MM-DD (for kr_add)",
			},
			"kr_note": map[string]any{
				"type":        "string",
				"description": "Key Result note (for kr_add, kr_update)",
			},
			"filter": map[string]any{
				"type":        "string",
				"description": "Filter for list: active(default)/done/archived/paused/all",
			},
		},
		"required": []string{"action"},
	}
}

func (t *GoalsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a goalsArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.ChatID == 0 {
		if chatID, ok := ctx.Value(ChatIDKey).(int64); ok {
			a.ChatID = chatID
		}
	}

	switch a.Action {
	case "create":
		return t.create(ctx, a)
	case "list":
		return t.list(ctx, a)
	case "get":
		return t.get(ctx, a)
	case "update":
		return t.update(ctx, a)
	case "delete":
		return t.deleteObj(ctx, a)
	case "set_progress":
		return t.setProgress(ctx, a)
	case "kr_add":
		return t.krAdd(ctx, a)
	case "kr_update":
		return t.krUpdate(ctx, a)
	case "kr_delete":
		return t.krDelete(ctx, a)
	case "dashboard":
		return t.dashboard(ctx, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (t *GoalsTool) create(ctx context.Context, a goalsArgs) (string, error) {
	if a.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	horizon := a.Horizon
	if horizon == "" {
		horizon = "quarter"
	}
	period := a.Period
	if period == "" {
		period = currentPeriod(horizon)
	}
	obj := storage.Objective{
		ChatID:      a.ChatID,
		Title:       a.Title,
		Description: a.Description,
		Horizon:     horizon,
		Period:      period,
		Status:      "active",
		Progress:    0,
	}
	id, err := t.db.CreateObjective(ctx, obj)
	if err != nil {
		return "", fmt.Errorf("create objective: %w", err)
	}
	return fmt.Sprintf("✅ Цель создана (id=%d): %s [%s %s]", id, a.Title, horizon, period), nil
}

func (t *GoalsTool) list(ctx context.Context, a goalsArgs) (string, error) {
	filter := a.Filter
	if filter == "" {
		filter = "active"
	}
	objs, err := t.db.ListObjectives(ctx, a.ChatID, filter)
	if err != nil {
		return "", err
	}
	if len(objs) == 0 {
		return fmt.Sprintf("Нет целей со статусом «%s».", filter), nil
	}
	var sb strings.Builder
	statusLabel := map[string]string{
		"active": "Активные цели", "done": "Выполненные цели",
		"paused": "На паузе", "archived": "Архив", "all": "Все цели",
	}
	label := statusLabel[filter]
	if label == "" {
		label = "Цели"
	}
	fmt.Fprintf(&sb, "🎯 **%s** (%d):\n\n", label, len(objs))
	for _, o := range objs {
		progressBar := progressEmoji(o.Progress)
		fmt.Fprintf(&sb, "**[%d] %s**\n", o.ID, o.Title)
		fmt.Fprintf(&sb, "    %s %d%% | %s | %s\n", progressBar, o.Progress, o.Horizon, o.Period)
		if len(o.KeyResults) > 0 {
			for _, kr := range o.KeyResults {
				fmt.Fprintf(&sb, "    • KR[%d] %s — %d%%\n", kr.ID, kr.Title, kr.Progress)
			}
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

func (t *GoalsTool) get(ctx context.Context, a goalsArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	o, err := t.db.GetObjective(ctx, a.ChatID, a.ID)
	if err != nil {
		return "", err
	}
	if o == nil {
		return fmt.Sprintf("Цель %d не найдена.", a.ID), nil
	}
	return formatObjective(o), nil
}

func (t *GoalsTool) update(ctx context.Context, a goalsArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	o, err := t.db.GetObjective(ctx, a.ChatID, a.ID)
	if err != nil {
		return "", err
	}
	if o == nil {
		return fmt.Sprintf("Цель %d не найдена.", a.ID), nil
	}
	// Apply partial updates.
	title := a.Title
	if title == "" {
		title = o.Title
	}
	desc := a.Description
	if desc == "" {
		desc = o.Description
	}
	status := a.Status
	if status == "" {
		status = o.Status
	}
	progress := o.Progress
	if a.Progress != nil {
		progress = *a.Progress
	}
	if err := t.db.UpdateObjective(ctx, a.ChatID, a.ID, title, desc, status, progress); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Цель %d обновлена.", a.ID), nil
}

func (t *GoalsTool) deleteObj(ctx context.Context, a goalsArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	if err := t.db.DeleteObjective(ctx, a.ChatID, a.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("🗑 Цель %d удалена.", a.ID), nil
}

func (t *GoalsTool) setProgress(ctx context.Context, a goalsArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required")
	}
	if a.Progress == nil {
		return "", fmt.Errorf("progress is required")
	}
	o, err := t.db.GetObjective(ctx, a.ChatID, a.ID)
	if err != nil {
		return "", err
	}
	if o == nil {
		return fmt.Sprintf("Цель %d не найдена.", a.ID), nil
	}
	if err := t.db.UpdateObjective(ctx, a.ChatID, a.ID, o.Title, o.Description, o.Status, *a.Progress); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Прогресс цели %d установлен: %d%%", a.ID, *a.Progress), nil
}

func (t *GoalsTool) krAdd(ctx context.Context, a goalsArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id (objective) is required")
	}
	if a.KRTitle == "" {
		return "", fmt.Errorf("kr_title is required")
	}
	// Validate that the objective belongs to this chat.
	obj, err := t.db.GetObjective(ctx, a.ChatID, a.ID)
	if err != nil {
		return "", err
	}
	if obj == nil {
		return fmt.Sprintf("Цель %d не найдена.", a.ID), nil
	}
	kr := storage.KeyResult{
		ObjectiveID: a.ID,
		Title:       a.KRTitle,
		Deadline:    a.KRDeadline,
		Note:        a.KRNote,
	}
	id, err := t.db.AddKeyResult(ctx, kr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Key Result добавлен (id=%d): %s", id, a.KRTitle), nil
}

func (t *GoalsTool) krUpdate(ctx context.Context, a goalsArgs) (string, error) {
	if a.KRID == 0 {
		return "", fmt.Errorf("kr_id is required")
	}
	// Fetch current values to enable partial update and verify chat ownership.
	current, err := t.db.GetKeyResult(ctx, a.ChatID, a.KRID)
	if err != nil {
		return "", err
	}
	if current == nil {
		return fmt.Sprintf("Key Result %d не найден.", a.KRID), nil
	}
	// Merge: use new value when explicitly provided, else keep current.
	progress := current.Progress
	if a.Progress != nil {
		progress = *a.Progress
	}
	note := current.Note
	if a.KRNote != "" {
		note = a.KRNote
	}
	if err := t.db.UpdateKeyResult(ctx, a.ChatID, a.KRID, progress, note); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Key Result %d обновлён: %d%%", a.KRID, progress), nil
}

func (t *GoalsTool) krDelete(ctx context.Context, a goalsArgs) (string, error) {
	if a.KRID == 0 {
		return "", fmt.Errorf("kr_id is required")
	}
	if err := t.db.DeleteKeyResult(ctx, a.ChatID, a.KRID); err != nil {
		return "", err
	}
	return fmt.Sprintf("🗑 Key Result %d удалён.", a.KRID), nil
}

func (t *GoalsTool) dashboard(ctx context.Context, a goalsArgs) (string, error) {
	objs, err := t.db.ListObjectives(ctx, a.ChatID, "active")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("🎯 **OKR Дашборд**\n\n")
	if len(objs) == 0 {
		sb.WriteString("Нет активных целей. Создай первую цель: goals create.\n")
		return sb.String(), nil
	}

	// Aggregate progress.
	var totalProgress int
	for _, o := range objs {
		totalProgress += o.Progress
	}
	avgProgress := totalProgress / len(objs)
	fmt.Fprintf(&sb, "📊 Активных целей: %d | Средний прогресс: %d%%\n\n", len(objs), avgProgress)

	// Top 5 active objectives.
	limit := min(len(objs), 5)
	sb.WriteString("**Топ целей:**\n")
	for _, o := range objs[:limit] {
		bar := progressEmoji(o.Progress)
		fmt.Fprintf(&sb, "%s **%s** — %d%% [%s %s]", bar, o.Title, o.Progress, o.Horizon, o.Period)
		// H-5: show linked workout count if any.
		if wCount, err := t.db.CountWorkoutsForGoal(ctx, a.ChatID, o.ID); err == nil && wCount > 0 {
			fmt.Fprintf(&sb, " 🏋️%d", wCount)
		}
		sb.WriteString("\n")
		for _, kr := range o.KeyResults {
			fmt.Fprintf(&sb, "   • %s — %d%%\n", kr.Title, kr.Progress)
		}
	}
	return sb.String(), nil
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func formatObjective(o *storage.Objective) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "🎯 **[%d] %s**\n\n", o.ID, o.Title)
	fmt.Fprintf(&sb, "📅 Горизонт: %s | Период: %s\n", o.Horizon, o.Period)
	fmt.Fprintf(&sb, "📊 Статус: %s | Прогресс: %s %d%%\n", o.Status, progressEmoji(o.Progress), o.Progress)
	if o.Description != "" {
		fmt.Fprintf(&sb, "\n📝 %s\n", o.Description)
	}
	if len(o.KeyResults) > 0 {
		sb.WriteString("\n**Key Results:**\n")
		for _, kr := range o.KeyResults {
			fmt.Fprintf(&sb, "  [%d] %s — %d%%", kr.ID, kr.Title, kr.Progress)
			if kr.Deadline != "" {
				fmt.Fprintf(&sb, " (до %s)", kr.Deadline)
			}
			if kr.Note != "" {
				fmt.Fprintf(&sb, "\n       💬 %s", kr.Note)
			}
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "\nСоздана: %s", o.CreatedAt.Format("2006-01-02"))
	return sb.String()
}

func progressEmoji(p int) string {
	switch {
	case p >= 80:
		return "🟢"
	case p >= 50:
		return "🟡"
	case p >= 20:
		return "🟠"
	default:
		return "🔴"
	}
}

// currentPeriod returns a sensible period label for the given horizon.
func currentPeriod(horizon string) string {
	now := time.Now()
	switch horizon {
	case "year":
		return fmt.Sprintf("%d", now.Year())
	case "month":
		return now.Format("2006-01")
	default: // quarter
		q := (int(now.Month())-1)/3 + 1
		return fmt.Sprintf("%d-Q%d", now.Year(), q)
	}
}
