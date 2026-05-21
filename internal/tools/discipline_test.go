package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func disciplineExec(t *testing.T, dt *tools.DisciplineTool, chatID int64, args map[string]any) (string, error) {
	t.Helper()
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, chatID)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return dt.Execute(ctx, raw)
}

func TestDisciplineToolName(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	if dt.Name() != "discipline" {
		t.Errorf("expected 'discipline', got %q", dt.Name())
	}
}

func TestDisciplineToolDescription(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	if dt.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestDisciplineToolParameters(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	params := dt.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be map[string]any")
	}
	for _, key := range []string{"action", "date", "planned", "done", "score", "week", "wins"} {
		if _, exists := props[key]; !exists {
			t.Errorf("expected property %q in parameters", key)
		}
	}
	req, ok := params["required"].([]string)
	if !ok || len(req) == 0 || req[0] != "action" {
		t.Errorf("expected 'action' to be required, got %v", params["required"])
	}
}

func TestDisciplineDaySave(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 100, map[string]any{
		"action":   "day_save",
		"date":     "2026-04-18",
		"planned":  "План",
		"done":     "Сделал",
		"not_done": "Не сделал",
		"reason":   "Причина",
		"score":    8,
		"mood":     "хорошее",
		"note":     "Заметка",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "сохранено") {
		t.Errorf("expected 'сохранено' in result, got %q", result)
	}

	// Verify persistence with day_get
	got, err := disciplineExec(t, dt, 100, map[string]any{
		"action": "day_get",
		"date":   "2026-04-18",
	})
	if err != nil {
		t.Fatalf("day_get after save: %v", err)
	}
	for _, want := range []string{"2026-04-18", "8/10", "хорошее", "План", "Сделал", "Не сделал", "Причина", "Заметка"} {
		if !strings.Contains(got, want) {
			t.Errorf("day_get: expected %q in result, got %q", want, got)
		}
	}
}

func TestDisciplineDayGet(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	date := "2026-04-18"
	_, err := disciplineExec(t, dt, 101, map[string]any{
		"action":   "day_save",
		"date":     date,
		"planned":  "Планировал важное",
		"done":     "Сделал задачу",
		"not_done": "Не сделал второе",
		"reason":   "Не хватило времени",
		"score":    7,
		"mood":     "нейтральное",
		"note":     "Надо начать раньше",
	})
	if err != nil {
		t.Fatalf("save day review: %v", err)
	}

	result, err := disciplineExec(t, dt, 101, map[string]any{
		"action": "day_get",
		"date":   date,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		date,
		"7/10",
		"нейтральное",
		"Планировал важное",
		"Сделал задачу",
		"Не сделал второе",
		"Не хватило времени",
		"Надо начать раньше",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got %q", want, result)
		}
	}
}

func TestDisciplineDayGet_NotFound(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 102, map[string]any{
		"action": "day_get",
		"date":   "2026-04-18",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найдено") {
		t.Errorf("expected 'не найдено' in result, got %q", result)
	}
}

func TestDisciplineDayList(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	for _, tc := range []struct {
		date string
		done string
	}{
		{date: "2026-04-18", done: "Сделал 1"},
		{date: "2026-04-19", done: "Сделал 2"},
	} {
		_, err := disciplineExec(t, dt, 103, map[string]any{
			"action": "day_save",
			"date":   tc.date,
			"done":   tc.done,
			"score":  8,
		})
		if err != nil {
			t.Fatalf("save day review %s: %v", tc.date, err)
		}
	}

	result, err := disciplineExec(t, dt, 103, map[string]any{
		"action": "day_list",
		"limit":  10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"2026-04-18", "2026-04-19"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got %q", want, result)
		}
	}
	// Verify both entries appear (no more, no fewer)
	count := strings.Count(result, "2026-04-1")
	if count != 2 {
		t.Errorf("expected 2 entries in day_list, got %d (result: %q)", count, result)
	}
}

func TestDisciplineDayList_Empty(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 104, map[string]any{
		"action": "day_list",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Нет ни одного") {
		t.Errorf("expected empty-list message, got %q", result)
	}
}

func TestDisciplineDayStats_Empty(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 105, map[string]any{
		"action": "day_stats",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Нет ревью") {
		t.Errorf("expected empty stats message, got %q", result)
	}
}

func TestDisciplineDayStats_WithData(t *testing.T) {
	now := time.Now()
	dt := tools.NewDisciplineTool(openTestDB(t))
	dates := []struct {
		date  string
		score int
	}{
		{date: now.Format("2006-01-02"), score: 8},
		{date: now.AddDate(0, 0, -1).Format("2006-01-02"), score: 7},
		{date: now.AddDate(0, 0, -2).Format("2006-01-02"), score: 6},
	}
	for _, tc := range dates {
		_, err := disciplineExec(t, dt, 106, map[string]any{
			"action": "day_save",
			"date":   tc.date,
			"score":  tc.score,
			"done":   "Сделано",
		})
		if err != nil {
			t.Fatalf("save day review %s: %v", tc.date, err)
		}
	}

	result, err := disciplineExec(t, dt, 106, map[string]any{
		"action": "day_stats",
		"days":   7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Стрик: 3", "7.0/10", "3 из 7"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got %q", want, result)
		}
	}
}

func TestDisciplineWeekSave(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 107, map[string]any{
		"action":         "week_save",
		"week":           "2026-W16",
		"wins":           "Победы недели",
		"fails":          "Провалы недели",
		"pattern":        "Главный инсайт",
		"next_week_plan": "План на следующую неделю",
		"score":          9,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "сохранено") {
		t.Errorf("expected 'сохранено' in result, got %q", result)
	}

	// Verify persistence with week_get
	got, err := disciplineExec(t, dt, 107, map[string]any{
		"action": "week_get",
		"week":   "2026-W16",
	})
	if err != nil {
		t.Fatalf("week_get after save: %v", err)
	}
	for _, want := range []string{"2026-W16", "9/10", "Победы недели", "Провалы недели", "Главный инсайт", "План на следующую неделю"} {
		if !strings.Contains(got, want) {
			t.Errorf("week_get: expected %q in result, got %q", want, got)
		}
	}
}

func TestDisciplineWeekGet(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	week := "2026-W16"
	_, err := disciplineExec(t, dt, 108, map[string]any{
		"action":         "week_save",
		"week":           week,
		"wins":           "Сильные стороны",
		"fails":          "Слабые места",
		"pattern":        "Регулярность работает",
		"next_week_plan": "Повторить систему",
		"score":          8,
	})
	if err != nil {
		t.Fatalf("save week review: %v", err)
	}

	result, err := disciplineExec(t, dt, 108, map[string]any{
		"action": "week_get",
		"week":   week,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		week,
		"8/10",
		"Сильные стороны",
		"Слабые места",
		"Регулярность работает",
		"Повторить систему",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got %q", want, result)
		}
	}
}

func TestDisciplineWeekGet_NotFound(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 109, map[string]any{
		"action": "week_get",
		"week":   "2026-W16",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найдено") {
		t.Errorf("expected 'не найдено' in result, got %q", result)
	}
}

func TestDisciplineDashboard(t *testing.T) {
	now := time.Now()
	dt := tools.NewDisciplineTool(openTestDB(t))
	_, err := disciplineExec(t, dt, 110, map[string]any{
		"action": "day_save",
		"date":   now.Format("2006-01-02"),
		"score":  8,
		"done":   "Закрыл день",
	})
	if err != nil {
		t.Fatalf("save day review: %v", err)
	}
	_, err = disciplineExec(t, dt, 110, map[string]any{
		"action":         "week_save",
		"wins":           "Победа",
		"fails":          "Провал",
		"pattern":        "Инсайт",
		"next_week_plan": "План",
		"score":          9,
	})
	if err != nil {
		t.Fatalf("save week review: %v", err)
	}

	result, err := disciplineExec(t, dt, 110, map[string]any{
		"action": "dashboard",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Дашборд дисциплины", "Стрик ревью", "Средняя оценка (7 дней): 8.0/10", "Ревью недели", "заполнено"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in result, got %q", want, result)
		}
	}
	// Verify streak = 1 (only today saved) and average correctly computed from saved data
	if !strings.Contains(result, "Стрик ревью: 1") {
		t.Errorf("expected 'Стрик ревью: 1' in dashboard result, got %q", result)
	}
}

func TestDisciplineUnknownAction(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	_, err := disciplineExec(t, dt, 111, map[string]any{
		"action": "fly",
	})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected unknown action error, got %v", err)
	}
}

func TestDisciplineChatIsolation(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	date := "2026-04-18"
	_, err := disciplineExec(t, dt, 100, map[string]any{
		"action": "day_save",
		"date":   date,
		"done":   "Только для чата 100",
		"score":  8,
	})
	if err != nil {
		t.Fatalf("save day review: %v", err)
	}

	result, err := disciplineExec(t, dt, 200, map[string]any{
		"action": "day_get",
		"date":   date,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найдено") {
		t.Errorf("expected chat isolation not-found message, got %q", result)
	}
}

func TestDisciplineDaySave_DefaultDate(t *testing.T) {
	now := time.Now()
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 120, map[string]any{
		"action": "day_save",
		"done":   "Сделано сегодня",
		"score":  5,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	today := now.Format("2006-01-02")
	if !strings.Contains(result, today) {
		t.Errorf("expected today's date %q in result, got %q", today, result)
	}
}

func TestDisciplineDayGet_DefaultDate(t *testing.T) {
	now := time.Now()
	today := now.Format("2006-01-02")
	dt := tools.NewDisciplineTool(openTestDB(t))
	_, err := disciplineExec(t, dt, 121, map[string]any{
		"action": "day_save",
		"date":   today,
		"done":   "Что-то",
		"score":  6,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	result, err := disciplineExec(t, dt, 121, map[string]any{
		"action": "day_get",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, today) {
		t.Errorf("expected today's date %q in result, got %q", today, result)
	}
}

func TestDisciplineWeekSave_DefaultWeek(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 122, map[string]any{
		"action": "week_save",
		"wins":   "Победа",
		"score":  7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "сохранено") {
		t.Errorf("expected 'сохранено' in result, got %q", result)
	}
}

func TestDisciplineWeekGet_DefaultWeek(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	_, err := disciplineExec(t, dt, 123, map[string]any{
		"action": "week_save",
		"wins":   "Текущая неделя",
		"score":  8,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	result, err := disciplineExec(t, dt, 123, map[string]any{
		"action": "week_get",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Текущая неделя") {
		t.Errorf("expected wins in result, got %q", result)
	}
}

func TestDisciplineDashboard_NoReviews(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	result, err := disciplineExec(t, dt, 124, map[string]any{
		"action": "dashboard",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Дашборд дисциплины", "За последние 7 дней ревью нет", "не заполнено"} {
		if !strings.Contains(result, want) {
			t.Errorf("expected %q in empty dashboard result, got %q", want, result)
		}
	}
}

func TestDisciplineExecute_InvalidJSON(t *testing.T) {
	dt := tools.NewDisciplineTool(openTestDB(t))
	ctx := context.WithValue(context.Background(), tools.ChatIDKey, int64(130))
	_, err := dt.Execute(ctx, json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
