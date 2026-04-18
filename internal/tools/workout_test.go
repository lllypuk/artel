package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func openWorkoutDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

func workoutExec(t *testing.T, wt *tools.WorkoutTool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return wt.Execute(context.Background(), raw)
}

const wChatID = int64(42000)

// ── metadata ──────────────────────────────────────────────────────────────────

func TestWorkoutName(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	if wt.Name() != "workout" {
		t.Errorf("expected 'workout', got %q", wt.Name())
	}
}

func TestWorkoutDescription(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	if wt.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestWorkoutParameters(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	params := wt.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map")
	}
	for _, key := range []string{"action", "type", "date", "sets", "exercise"} {
		if _, ok := props[key]; !ok {
			t.Errorf("missing parameter %q", key)
		}
	}
}

// ── H-1: log ─────────────────────────────────────────────────────────────────

func TestWorkoutLog_Basic(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":       "log",
		"type":         "run",
		"date":         "2026-04-19",
		"duration_min": 30,
		"chat_id":      wChatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "записана") {
		t.Errorf("expected 'записана' in result, got %q", result)
	}
}

func TestWorkoutLog_WithSets(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":       "log",
		"type":         "strength",
		"date":         "2026-04-19",
		"duration_min": 60,
		"chat_id":      wChatID,
		"sets": []map[string]any{
			{"exercise": "bench press", "sets": 4, "reps": 8, "weight_kg": 80.0},
			{"exercise": "squat", "sets": 3, "reps": 10, "weight_kg": 100.0},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "bench press") {
		t.Errorf("expected exercise 'bench press' in result, got %q", result)
	}
	if !strings.Contains(result, "squat") {
		t.Errorf("expected exercise 'squat' in result, got %q", result)
	}
}

func TestWorkoutLog_DefaultDate(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "yoga",
		"chat_id": wChatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "записана") {
		t.Errorf("expected 'записана' in result, got %q", result)
	}
}

func TestWorkoutLog_MissingType(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error when type is missing")
	}
}

func TestWorkoutLog_InvalidDate(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "run",
		"date":    "not-a-date",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
}

func TestWorkoutLog_WithGoalID(t *testing.T) {
	// Create the objective and the workout tool using the same DB.
	db := openWorkoutDB(t)
	wt := tools.NewWorkoutTool(db)
	ctx := context.Background()
	goalID, err := db.CreateObjective(ctx, storage.Objective{
		ChatID: wChatID, Title: "Run 10km", Horizon: "quarter", Status: "active",
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}

	result, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "run",
		"chat_id": wChatID,
		"goal_id": goalID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, fmt.Sprintf("#%d", goalID)) {
		t.Errorf("expected goal_id in result, got %q", result)
	}
}

func TestWorkoutLog_InvalidGoalID(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "run",
		"chat_id": wChatID,
		"goal_id": int64(9999), // non-existent goal
	})
	if err == nil {
		t.Fatal("expected error for non-existent goal_id")
	}
}

// ── H-2: list ────────────────────────────────────────────────────────────────

func TestWorkoutList_Empty(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "list",
		"chat_id": wChatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найдено") {
		t.Errorf("expected 'не найдено' for empty list, got %q", result)
	}
}

func TestWorkoutList_Multiple(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	for i, tp := range []string{"run", "strength", "yoga"} {
		_, err := workoutExec(t, wt, map[string]any{
			"action":  "log",
			"type":    tp,
			"date":    fmt.Sprintf("2026-04-%02d", 10+i),
			"chat_id": wChatID + 1,
		})
		if err != nil {
			t.Fatalf("log %s: %v", tp, err)
		}
	}
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "list",
		"chat_id": wChatID + 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tp := range []string{"run", "strength", "yoga"} {
		if !strings.Contains(result, tp) {
			t.Errorf("expected %q in list result, got %q", tp, result)
		}
	}
}

func TestWorkoutList_FilterByType(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 2
	for _, tp := range []string{"run", "strength", "run"} {
		_, _ = workoutExec(t, wt, map[string]any{
			"action":  "log",
			"type":    tp,
			"chat_id": chatID,
		})
	}
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "list",
		"type":    "run",
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "strength") {
		t.Errorf("expected no 'strength' workouts in filtered list, got %q", result)
	}
}

func TestWorkoutList_Limit(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 3
	for i := range 5 {
		_, _ = workoutExec(t, wt, map[string]any{
			"action":  "log",
			"type":    "run",
			"date":    fmt.Sprintf("2026-04-%02d", i+1),
			"chat_id": chatID,
		})
	}
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "list",
		"limit":   2,
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "(2)") {
		t.Errorf("expected 2 workouts with limit, got %q", result)
	}
}

// ── get ───────────────────────────────────────────────────────────────────────

func TestWorkoutGet(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 10
	result, _ := workoutExec(t, wt, map[string]any{
		"action":       "log",
		"type":         "strength",
		"duration_min": 45,
		"note":         "great session",
		"chat_id":      chatID,
		"sets": []map[string]any{
			{"exercise": "deadlift", "sets": 3, "reps": 5, "weight_kg": 120.0},
		},
	})
	var id int64
	if _, err := fmt.Sscanf(result, "✅ Тренировка записана (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id from %q: %v", result, err)
	}

	out, err := workoutExec(t, wt, map[string]any{
		"action":  "get",
		"id":      id,
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "deadlift") {
		t.Errorf("expected 'deadlift' in get result, got %q", out)
	}
	if !strings.Contains(out, "great session") {
		t.Errorf("expected note in get result, got %q", out)
	}
}

func TestWorkoutGet_NotFound(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	out, err := workoutExec(t, wt, map[string]any{
		"action":  "get",
		"id":      9999,
		"chat_id": wChatID + 11,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "не найдена") {
		t.Errorf("expected 'не найдена' for missing workout, got %q", out)
	}
}

func TestWorkoutGet_MissingID(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "get",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error when id missing")
	}
}

// ── delete ────────────────────────────────────────────────────────────────────

func TestWorkoutDelete(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 20
	result, _ := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "run",
		"chat_id": chatID,
	})
	var id int64
	if _, err := fmt.Sscanf(result, "✅ Тренировка записана (id: %d)", &id); err != nil {
		t.Fatalf("could not parse id from %q: %v", result, err)
	}

	_, err := workoutExec(t, wt, map[string]any{
		"action":  "delete",
		"id":      id,
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be gone.
	out, _ := workoutExec(t, wt, map[string]any{
		"action":  "list",
		"chat_id": chatID,
	})
	if !strings.Contains(out, "не найдено") {
		t.Errorf("expected empty list after delete, got %q", out)
	}
}

func TestWorkoutDelete_MissingID(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "delete",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error when id missing")
	}
}

// ── H-2: stats ───────────────────────────────────────────────────────────────

func TestWorkoutStats_Basic(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 30
	for _, tc := range []struct {
		tp  string
		dur int
		day string
	}{
		{"run", 30, "2026-04-01"},
		{"strength", 60, "2026-04-05"},
		{"yoga", 45, "2026-04-10"},
	} {
		_, _ = workoutExec(t, wt, map[string]any{
			"action":       "log",
			"type":         tc.tp,
			"date":         tc.day,
			"duration_min": tc.dur,
			"chat_id":      chatID,
		})
	}

	result, err := workoutExec(t, wt, map[string]any{
		"action":  "stats",
		"from":    "2026-04-01",
		"to":      "2026-04-30",
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "3") {
		t.Errorf("expected count 3 in stats, got %q", result)
	}
	if !strings.Contains(result, "135") {
		t.Errorf("expected total 135 min in stats, got %q", result)
	}
}

func TestWorkoutStats_Empty(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "stats",
		"from":    "2020-01-01",
		"to":      "2020-01-31",
		"chat_id": wChatID + 31,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найдено") {
		t.Errorf("expected 'не найдено' for empty stats, got %q", result)
	}
}

// ── H-3: templates ───────────────────────────────────────────────────────────

func TestWorkoutTemplate_SaveAndList(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 40
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "Push Day",
		"chat_id": chatID,
		"exercises": []map[string]any{
			{"name": "bench press", "sets": 4, "reps": 8, "weight_kg": 80.0},
			{"name": "overhead press", "sets": 3, "reps": 10},
		},
	})
	if err != nil {
		t.Fatalf("template_save error: %v", err)
	}

	list, err := workoutExec(t, wt, map[string]any{
		"action":  "template_list",
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("template_list error: %v", err)
	}
	if !strings.Contains(list, "Push Day") {
		t.Errorf("expected 'Push Day' in template list, got %q", list)
	}
	if !strings.Contains(list, "bench press") {
		t.Errorf("expected 'bench press' in template list, got %q", list)
	}
}

func TestWorkoutTemplate_Get(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 41
	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "Legs",
		"chat_id": chatID,
		"exercises": []map[string]any{
			{"name": "squat", "sets": 5, "reps": 5, "weight_kg": 100.0},
		},
	})

	result, err := workoutExec(t, wt, map[string]any{
		"action":  "template_get",
		"name":    "Legs",
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("template_get error: %v", err)
	}
	if !strings.Contains(result, "squat") {
		t.Errorf("expected 'squat' in template get, got %q", result)
	}
}

func TestWorkoutTemplate_GetNotFound(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "template_get",
		"name":    "NonExistent",
		"chat_id": wChatID + 42,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "не найден") {
		t.Errorf("expected 'не найден' for missing template, got %q", result)
	}
}

func TestWorkoutTemplate_Delete(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 43
	result, _ := workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "To Delete",
		"chat_id": chatID,
		"exercises": []map[string]any{
			{"name": "pullup", "sets": 3, "reps": 10},
		},
	})
	var id int64
	if _, err := fmt.Sscanf(result, "✅ Шаблон «To Delete» сохранён (id: %d,", &id); err != nil {
		t.Fatalf("could not parse template id from %q: %v", result, err)
	}

	_, err := workoutExec(t, wt, map[string]any{
		"action":  "template_delete",
		"id":      id,
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("template_delete error: %v", err)
	}

	list, _ := workoutExec(t, wt, map[string]any{
		"action":  "template_list",
		"chat_id": chatID,
	})
	if strings.Contains(list, "To Delete") {
		t.Errorf("expected template to be deleted, but still found in list: %q", list)
	}
}

func TestWorkoutTemplate_Empty(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "template_list",
		"chat_id": wChatID + 44,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Шаблонов") {
		t.Errorf("expected 'Шаблонов' for empty list, got %q", result)
	}
}

func TestWorkoutTemplate_SaveMissingName(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"chat_id": wChatID,
		"exercises": []map[string]any{
			{"name": "pullup"},
		},
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestWorkoutTemplate_SaveMissingExercises(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "Empty",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error for missing exercises")
	}
}

func TestWorkoutTemplate_SaveUpsert(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 45
	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "Cardio",
		"chat_id": chatID,
		"exercises": []map[string]any{
			{"name": "treadmill", "distance_km": 5.0},
		},
	})
	// Overwrite with new exercises.
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "template_save",
		"name":    "Cardio",
		"chat_id": chatID,
		"exercises": []map[string]any{
			{"name": "bike", "distance_km": 10.0},
		},
	})
	if err != nil {
		t.Fatalf("upsert error: %v", err)
	}
	result, _ := workoutExec(t, wt, map[string]any{
		"action":  "template_get",
		"name":    "Cardio",
		"chat_id": chatID,
	})
	if !strings.Contains(result, "bike") {
		t.Errorf("expected updated template to contain 'bike', got %q", result)
	}
	if strings.Contains(result, "treadmill") {
		t.Errorf("expected old 'treadmill' to be replaced, got %q", result)
	}
}

// ── H-4: personal records ────────────────────────────────────────────────────

func TestWorkoutPR_Basic(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 50

	// First workout: bench press 80 kg.
	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "bench press", "sets": 4, "reps": 8, "weight_kg": 80.0},
		},
	})

	result, err := workoutExec(t, wt, map[string]any{
		"action":  "pr",
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("pr error: %v", err)
	}
	if !strings.Contains(result, "bench press") {
		t.Errorf("expected 'bench press' in PR result, got %q", result)
	}
	if !strings.Contains(result, "80") {
		t.Errorf("expected '80' kg in PR result, got %q", result)
	}
}

func TestWorkoutPR_AutoDetectsNewRecord(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 51

	// First workout: bench press 80 kg.
	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "bench press", "sets": 4, "reps": 8, "weight_kg": 80.0},
		},
	})

	// Second workout: new PR 85 kg — must be announced.
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "bench press", "sets": 3, "reps": 6, "weight_kg": 85.0},
		},
	})
	if err != nil {
		t.Fatalf("log error: %v", err)
	}
	if !strings.Contains(result, "рекорд") || !strings.Contains(strings.ToLower(result), "bench") {
		t.Errorf("expected new PR announcement in log result, got %q", result)
	}
}

// TestWorkoutPR_NoFalsePositiveOnRepeat: repeating the same weight as a previous
// PR must NOT be announced as a new record.
func TestWorkoutPR_NoFalsePositiveOnRepeat(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 55

	// First workout — sets the PR at 100 kg.
	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "squat", "sets": 3, "reps": 5, "weight_kg": 100.0},
		},
	})

	// Second workout — same weight, must NOT announce a PR.
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "squat", "sets": 3, "reps": 5, "weight_kg": 100.0},
		},
	})
	if err != nil {
		t.Fatalf("log error: %v", err)
	}
	if strings.Contains(strings.ToLower(result), "рекорд") {
		t.Errorf("must not announce PR when weight equals previous best, got %q", result)
	}
}

func TestWorkoutPR_FilterByExercise(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 52

	_, _ = workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "strength",
		"chat_id": chatID,
		"sets": []map[string]any{
			{"exercise": "squat", "sets": 5, "reps": 5, "weight_kg": 120.0},
			{"exercise": "deadlift", "sets": 3, "reps": 3, "weight_kg": 150.0},
		},
	})

	result, err := workoutExec(t, wt, map[string]any{
		"action":   "pr",
		"exercise": "squat",
		"chat_id":  chatID,
	})
	if err != nil {
		t.Fatalf("pr error: %v", err)
	}
	if !strings.Contains(result, "squat") {
		t.Errorf("expected 'squat' in filtered PR, got %q", result)
	}
	if strings.Contains(result, "deadlift") {
		t.Errorf("expected no 'deadlift' in filtered PR, got %q", result)
	}
}

func TestWorkoutPR_Empty(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	result, err := workoutExec(t, wt, map[string]any{
		"action":  "pr",
		"chat_id": wChatID + 53,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "нет") {
		t.Errorf("expected 'нет' for empty PRs, got %q", result)
	}
}

func TestWorkoutPRHistory_Basic(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	chatID := wChatID + 54

	for i, kg := range []float64{70, 75, 80} {
		_, _ = workoutExec(t, wt, map[string]any{
			"action":  "log",
			"type":    "strength",
			"date":    fmt.Sprintf("2026-04-%02d", i+1),
			"chat_id": chatID,
			"sets": []map[string]any{
				{"exercise": "bench press", "sets": 4, "reps": 8, "weight_kg": kg},
			},
		})
	}

	result, err := workoutExec(t, wt, map[string]any{
		"action":   "pr_history",
		"exercise": "bench press",
		"chat_id":  chatID,
	})
	if err != nil {
		t.Fatalf("pr_history error: %v", err)
	}
	for _, kg := range []string{"70", "75", "80"} {
		if !strings.Contains(result, kg) {
			t.Errorf("expected weight %s kg in pr_history, got %q", kg, result)
		}
	}
}

func TestWorkoutPRHistory_MissingExercise(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "pr_history",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error when exercise missing")
	}
}

// ── unknown action ────────────────────────────────────────────────────────────

func TestWorkoutUnknownAction(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := workoutExec(t, wt, map[string]any{
		"action":  "fly",
		"chat_id": wChatID,
	})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestWorkoutInvalidJSON(t *testing.T) {
	wt := tools.NewWorkoutTool(openWorkoutDB(t))
	_, err := wt.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// ── H-5: goal link ────────────────────────────────────────────────────────────

func TestWorkoutGoalLink_StoredAndRetrieved(t *testing.T) {
	db := openWorkoutDB(t)
	wt := tools.NewWorkoutTool(db)
	chatID := wChatID + 60

	// Create a real objective so the goal_id validation passes.
	goalID, err := db.CreateObjective(context.Background(), storage.Objective{
		ChatID: chatID, Title: "Run 10km", Horizon: "quarter", Status: "active",
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}

	logResult, _ := workoutExec(t, wt, map[string]any{
		"action":  "log",
		"type":    "run",
		"chat_id": chatID,
		"goal_id": goalID,
	})
	var id int64
	if _, errS := fmt.Sscanf(logResult, "✅ Тренировка записана (id: %d)", &id); errS != nil {
		t.Fatalf("could not parse id from %q: %v", logResult, errS)
	}

	out, err := workoutExec(t, wt, map[string]any{
		"action":  "get",
		"id":      id,
		"chat_id": chatID,
	})
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if !strings.Contains(out, fmt.Sprintf("#%d", goalID)) {
		t.Errorf("expected goal in workout details, got %q", out)
	}
}
