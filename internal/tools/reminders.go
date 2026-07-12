package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/storage"
)

// RemindersTool manages scheduled reminders.
type RemindersTool struct {
	db *storage.DB
}

// NewRemindersTool creates a new reminders tool.
func NewRemindersTool(db *storage.DB) *RemindersTool {
	return &RemindersTool{db: db}
}

// DB returns the underlying storage.DB (used in tests).
func (t *RemindersTool) DB() *storage.DB { return t.db }

type remindersArgs struct {
	Action string `json:"action"` // create, list, delete, done
	ID     int64  `json:"id"`
	Text   string `json:"text"`
	When   string `json:"when"`   // natural language or ISO datetime
	Repeat string `json:"repeat"` // "daily", "weekly", "hourly", "Nm", "Nh", "Nd"
	ChatID int64  `json:"chat_id"`
}

func (t *RemindersTool) Name() string { return "reminders" }

func (t *RemindersTool) Description() string {
	return "Manage scheduled reminders. Actions:\n" +
		"  create — schedule a new reminder (params: text, when, repeat?).\n" +
		"    when: ISO datetime (e.g. '2026-04-03T10:00'), or relative Russian/English:\n" +
		"      'через 2 часа', 'через 30 минут', 'через 3 дня',\n" +
		"      'завтра в 10:00', 'послезавтра в 15:30', 'в 09:00', 'сегодня в 20:00',\n" +
		"      'in 2 hours', 'tomorrow at 10:00'.\n" +
		"    repeat (optional): 'daily', 'weekly', 'hourly', 'Nm' (N minutes), 'Nh' (N hours), 'Nd' (N days).\n" +
		"  list   — list all pending reminders.\n" +
		"  delete — cancel a reminder by id.\n" +
		"  done   — mark a reminder as completed by id."
}

func (t *RemindersTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "list", "delete", "done"},
				"description": "Action to perform",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Reminder text (for create)",
			},
			"when": map[string]any{
				"type":        "string",
				"description": "When to fire the reminder: ISO datetime or natural language (for create)",
			},
			"repeat": map[string]any{
				"type":        "string",
				"description": "Repeat interval: daily, weekly, hourly, Nm, Nh, Nd (optional, for create)",
			},
			"id": map[string]any{
				"type":        "integer",
				"description": "Reminder ID (for delete, done)",
			},
		},
		"required": []string{"action"},
	}
}

func (t *RemindersTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a remindersArgs
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
		return t.executeCreate(ctx, a)
	case "list":
		return t.executeList(ctx, a)
	case "delete":
		return t.executeDelete(ctx, a)
	case "done":
		return t.executeDone(ctx, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (t *RemindersTool) executeCreate(ctx context.Context, a remindersArgs) (string, error) {
	if a.Text == "" {
		return "", fmt.Errorf("text is required for create")
	}
	if a.When == "" {
		return "", fmt.Errorf("when is required for create")
	}

	now := time.Now()
	triggerAt, err := ParseReminderTime(a.When, now)
	if err != nil {
		return "", fmt.Errorf("invalid 'when': %w", err)
	}

	repeatInterval, err := ParseRepeatInterval(a.Repeat)
	if err != nil {
		return "", fmt.Errorf("invalid 'repeat': %w", err)
	}

	id, err := t.db.CreateReminder(ctx, a.ChatID, a.Text, triggerAt, repeatInterval)
	if err != nil {
		return "", fmt.Errorf("create reminder: %w", err)
	}

	msg := fmt.Sprintf("✅ Напоминание создано (id: %d)\n📅 Когда: %s\n📝 Текст: %s",
		id,
		triggerAt.In(now.Location()).Format("02 Jan 2006 в 15:04"),
		a.Text,
	)
	if repeatInterval > 0 {
		msg += fmt.Sprintf("\n🔁 Повтор: каждые %s", formatInterval(repeatInterval))
	}
	return msg, nil
}

func (t *RemindersTool) executeList(ctx context.Context, a remindersArgs) (string, error) {
	reminders, err := t.db.ListReminders(ctx, a.ChatID)
	if err != nil {
		return "", fmt.Errorf("list reminders: %w", err)
	}
	if len(reminders) == 0 {
		return "Активных напоминаний нет.", nil
	}

	now := time.Now()
	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 Активные напоминания (%d):\n", len(reminders))
	for _, r := range reminders {
		diff := time.Until(r.TriggerAt)
		when := r.TriggerAt.In(now.Location()).Format("02 Jan 2006 в 15:04")
		diffStr := ""
		if diff > 0 {
			diffStr = " (через " + formatDuration(diff) + ")"
		} else {
			diffStr = " (просрочено)"
		}
		fmt.Fprintf(&sb, "\n[%d] %s\n    🕐 %s%s", r.ID, r.Text, when, diffStr)
		if r.RepeatInterval > 0 {
			fmt.Fprintf(&sb, "\n    🔁 Повтор: каждые %s", formatInterval(r.RepeatInterval))
		}
	}
	return sb.String(), nil
}

func (t *RemindersTool) executeDelete(ctx context.Context, a remindersArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for delete")
	}
	if err := t.db.CancelReminder(ctx, a.ID, a.ChatID); err != nil {
		return "", err
	}
	return fmt.Sprintf("🗑 Напоминание %d отменено.", a.ID), nil
}

func (t *RemindersTool) executeDone(ctx context.Context, a remindersArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for done")
	}
	if err := t.db.MarkReminderDone(ctx, a.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Напоминание %d отмечено как выполненное.", a.ID), nil
}

// ─── Time parser ─────────────────────────────────────────────────────────────

// isoFormats lists formats tried in order when parsing the `when` field.
var isoFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
}

// relativeRu matches "через N единиц" (Russian).
var relativeRu = regexp.MustCompile(`^через\s+(\d+)\s+(\S+)`)

// relativeEn matches "in N units" (English).
var relativeEn = regexp.MustCompile(`^in\s+(\d+)\s+(\S+)`)

// timeOnlyRe matches "в HH:MM" or bare "HH:MM".
var timeOnlyRe = regexp.MustCompile(`^(?:в\s+)?(\d{1,2}):(\d{2})\s*$`)

// ParseReminderTime parses a human-readable or ISO time string into time.Time.
// now is used as the reference for relative and "today" computations.
func ParseReminderTime(when string, now time.Time) (time.Time, error) {
	loc := now.Location()
	s := strings.TrimSpace(when)

	// 1. Try ISO formats first.
	for _, f := range isoFormats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t, nil
		}
	}

	lower := strings.ToLower(s)

	// 2. Russian relative: "через N <unit>".
	if m := relativeRu.FindStringSubmatch(lower); m != nil {
		n, _ := strconv.Atoi(m[1])
		return now.Add(unitToDuration(m[2], n)), nil
	}

	// 3. English relative: "in N <unit>".
	if m := relativeEn.FindStringSubmatch(lower); m != nil {
		n, _ := strconv.Atoi(m[1])
		return now.Add(unitToDuration(m[2], n)), nil
	}

	// 4. Day prefix + optional "в HH:MM".
	dayOffset, remainder := parseDayPrefix(lower, now)
	if dayOffset >= 0 {
		base := truncateToDay(now).AddDate(0, 0, dayOffset)
		if t, ok := parseTimeComponent(remainder, loc, base); ok {
			return t, nil
		}
		// No time component — keep the current wall-clock time on that day.
		return base.Add(time.Duration(now.Hour())*time.Hour + time.Duration(now.Minute())*time.Minute), nil
	}

	// 5. Bare "в HH:MM" or "HH:MM".
	if m := timeOnlyRe.FindStringSubmatch(lower); m != nil {
		h, _ := strconv.Atoi(m[1])
		minutes, _ := strconv.Atoi(m[2])
		t := truncateToDay(now).Add(time.Duration(h)*time.Hour + time.Duration(minutes)*time.Minute)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}

	return time.Time{}, fmt.Errorf("не удалось распознать время %q — используйте формат ISO (2026-04-03T10:00) или 'через 2 часа', 'завтра в 10:00'", when)
}

// parseDayPrefix extracts the day offset and the remainder of the string.
// Returns -1 for dayOffset if no day keyword found.
func parseDayPrefix(lower string, _ time.Time) (dayOffset int, remainder string) {
	prefixes := []struct {
		keyword string
		offset  int
	}{
		{"послезавтра", 2},
		{"day after tomorrow", 2},
		{"завтра", 1},
		{"tomorrow", 1},
		{"сегодня", 0},
		{"today", 0},
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p.keyword) {
			return p.offset, strings.TrimSpace(lower[len(p.keyword):])
		}
	}
	return -1, lower
}

// parseTimeComponent tries to find "в HH:MM" or "at HH:MM" in s and
// returns the resulting time on the given base day.
func parseTimeComponent(s string, loc *time.Location, base time.Time) (time.Time, bool) {
	reAt := regexp.MustCompile(`(?:в|at)\s+(\d{1,2}):(\d{2})`)
	if m := reAt.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		minutes, _ := strconv.Atoi(m[2])
		return time.Date(base.Year(), base.Month(), base.Day(), h, minutes, 0, 0, loc), true
	}
	// bare HH:MM
	reNum := regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	if m := reNum.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		h, _ := strconv.Atoi(m[1])
		minutes, _ := strconv.Atoi(m[2])
		return time.Date(base.Year(), base.Month(), base.Day(), h, minutes, 0, 0, loc), true
	}
	return time.Time{}, false
}

func truncateToDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// unitToDuration maps a unit word to a duration multiplied by n.
func unitToDuration(unit string, n int) time.Duration {
	d := time.Duration(n)
	switch {
	case strings.HasPrefix(unit, "секунд"), strings.HasPrefix(unit, "second"):
		return d * time.Second
	case strings.HasPrefix(unit, "минут"), strings.HasPrefix(unit, "minute"):
		return d * time.Minute
	case strings.HasPrefix(unit, "час"), strings.HasPrefix(unit, "hour"):
		return d * time.Hour
	case strings.HasPrefix(unit, "день"), strings.HasPrefix(unit, "дня"), strings.HasPrefix(unit, "дней"),
		strings.HasPrefix(unit, "day"):
		return d * 24 * time.Hour
	case strings.HasPrefix(unit, "недел"), strings.HasPrefix(unit, "week"):
		return d * 7 * 24 * time.Hour
	default:
		return d * time.Hour // fallback
	}
}

// ParseRepeatInterval converts a repeat string to seconds (0 = no repeat).
func ParseRepeatInterval(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	switch s {
	case "hourly", "каждый час":
		return 3600, nil
	case "daily", "каждый день", "ежедневно":
		return 86400, nil
	case "weekly", "каждую неделю", "еженедельно":
		return 604800, nil
	}
	// Patterns: Nm, Nh, Nd
	re := regexp.MustCompile(`^(\d+)([mhdмчд])$`)
	if m := re.FindStringSubmatch(s); m != nil {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		switch m[2] {
		case "m", "м":
			return n * 60, nil
		case "h", "ч":
			return n * 3600, nil
		case "d", "д":
			return n * 86400, nil
		}
	}
	return 0, fmt.Errorf("неизвестный формат повтора %q (допустимо: daily, weekly, hourly, Nm, Nh, Nd)", s)
}

// ─── Formatting helpers ───────────────────────────────────────────────────────

func formatInterval(seconds int64) string {
	switch {
	case seconds%86400 == 0:
		d := seconds / 86400
		if d == 1 {
			return "день"
		}
		return fmt.Sprintf("%d дней", d)
	case seconds%3600 == 0:
		h := seconds / 3600
		if h == 1 {
			return "час"
		}
		return fmt.Sprintf("%d часов", h)
	case seconds%60 == 0:
		m := seconds / 60
		if m == 1 {
			return "минуту"
		}
		return fmt.Sprintf("%d минут", m)
	default:
		return fmt.Sprintf("%d секунд", seconds)
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d д", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d ч", hours))
	}
	if mins > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%d мин", mins))
	}
	return strings.Join(parts, " ")
}
