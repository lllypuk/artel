package storage_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

func mustCreateObjective(t *testing.T, db *storage.DB, chatID int64, title string) int64 {
	t.Helper()

	id, err := db.CreateObjective(context.Background(), storage.Objective{
		ChatID:  chatID,
		Title:   title,
		Horizon: "quarter",
		Status:  "active",
	})
	if err != nil {
		t.Fatalf("create objective: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero objective id")
	}
	return id
}

func mustCreateWorkout(t *testing.T, db *storage.DB, chatID int64, date, wType string, durationMin int, note string, goalID *int64) int64 {
	t.Helper()

	id, err := db.CreateWorkout(context.Background(), chatID, date, wType, durationMin, note, goalID)
	if err != nil {
		t.Fatalf("create workout: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero workout id")
	}
	return id
}

func mustAddWorkoutSet(t *testing.T, db *storage.DB, set storage.WorkoutSet) int64 {
	t.Helper()

	id, err := db.AddWorkoutSet(context.Background(), set)
	if err != nil {
		t.Fatalf("add workout set: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero workout set id")
	}
	return id
}

func findPersonalRecord(prs []storage.PersonalRecord, exercise string) *storage.PersonalRecord {
	for i := range prs {
		if prs[i].Exercise == exercise {
			return &prs[i]
		}
	}
	return nil
}

func assertWorkoutCore(t *testing.T, workout *storage.Workout, wantID, wantChatID int64, wantDate, wantType string, wantDuration int, wantNote string, wantGoalID int64) {
	t.Helper()

	if workout == nil {
		t.Fatal("expected workout, got nil")
	}
	if workout.ID != wantID {
		t.Errorf("expected id %d, got %d", wantID, workout.ID)
	}
	if workout.ChatID != wantChatID {
		t.Errorf("expected chat id %d, got %d", wantChatID, workout.ChatID)
	}
	if workout.Date != wantDate {
		t.Errorf("expected date %s, got %q", wantDate, workout.Date)
	}
	if workout.Type != wantType {
		t.Errorf("expected type %s, got %q", wantType, workout.Type)
	}
	if workout.DurationMin != wantDuration {
		t.Errorf("expected duration %d, got %d", wantDuration, workout.DurationMin)
	}
	if workout.Note != wantNote {
		t.Errorf("expected note %q, got %q", wantNote, workout.Note)
	}
	if wantGoalID != 0 {
		if workout.GoalID == nil {
			t.Fatalf("expected goal id %d, got nil", wantGoalID)
		}
		if *workout.GoalID != wantGoalID {
			t.Fatalf("expected goal id %d, got %d", wantGoalID, *workout.GoalID)
		}
	} else if workout.GoalID != nil {
		t.Fatalf("expected nil goal id, got %d", *workout.GoalID)
	}
	if workout.CreatedAt.IsZero() {
		t.Error("expected created_at to be set")
	}
}

func assertWorkoutSet(t *testing.T, set storage.WorkoutSet, wantID, wantWorkoutID int64, wantExercise string, wantSets, wantReps int, wantWeight, wantDistance float64, wantNote string) {
	t.Helper()

	if set.ID != wantID {
		t.Errorf("expected set id %d, got %d", wantID, set.ID)
	}
	if set.WorkoutID != wantWorkoutID {
		t.Errorf("expected workout id %d, got %d", wantWorkoutID, set.WorkoutID)
	}
	if set.Exercise != wantExercise {
		t.Errorf("expected exercise %q, got %q", wantExercise, set.Exercise)
	}
	if set.Sets != wantSets {
		t.Errorf("expected sets %d, got %d", wantSets, set.Sets)
	}
	if set.Reps != wantReps {
		t.Errorf("expected reps %d, got %d", wantReps, set.Reps)
	}
	if set.WeightKg != wantWeight {
		t.Errorf("expected weight %v, got %v", wantWeight, set.WeightKg)
	}
	if set.DistanceKm != wantDistance {
		t.Errorf("expected distance %v, got %v", wantDistance, set.DistanceKm)
	}
	if set.Note != wantNote {
		t.Errorf("expected set note %q, got %q", wantNote, set.Note)
	}
}

func TestCreateWorkout(t *testing.T) {
	db := openTestDB(t)

	id, err := db.CreateWorkout(context.Background(), 1, "2026-01-10", "strength", 45, "Upper body", nil)
	if err != nil {
		t.Fatalf("CreateWorkout returned error: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero workout id")
	}
}

func TestCreateWorkout_WithGoalID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	goalID := mustCreateObjective(t, db, 1, "Bench goal")

	workoutID, err := db.CreateWorkout(ctx, 1, "2026-01-11", "strength", 50, "Goal session", &goalID)
	if err != nil {
		t.Fatalf("CreateWorkout returned error: %v", err)
	}
	if workoutID == 0 {
		t.Fatal("expected non-zero workout id")
	}

	workout, err := db.GetWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout == nil {
		t.Fatal("expected workout, got nil")
	}
	if workout.GoalID == nil {
		t.Fatal("expected goal id to be set")
	}
	if *workout.GoalID != goalID {
		t.Errorf("expected goal id %d, got %d", goalID, *workout.GoalID)
	}
}

func TestGetWorkout(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	goalID := mustCreateObjective(t, db, 10, "Run goal")
	workoutID := mustCreateWorkout(t, db, 10, "2026-02-10", "run", 35, "Morning run", &goalID)
	setID := mustAddWorkoutSet(t, db, storage.WorkoutSet{
		WorkoutID:  workoutID,
		Exercise:   "Intervals",
		Sets:       4,
		Reps:       800,
		WeightKg:   0,
		DistanceKm: 3.2,
		Note:       "Fast reps",
	})

	workout, err := db.GetWorkout(ctx, 10, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}

	assertWorkoutCore(t, workout, workoutID, 10, "2026-02-10", "run", 35, "Morning run", goalID)
	if len(workout.Sets) != 1 {
		t.Fatalf("expected 1 set, got %d", len(workout.Sets))
	}
	assertWorkoutSet(t, workout.Sets[0], setID, workoutID, "Intervals", 4, 800, 0, 3.2, "Fast reps")
}

func TestGetWorkout_NotFound(t *testing.T) {
	db := openTestDB(t)

	workout, err := db.GetWorkout(context.Background(), 1, 999)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout != nil {
		t.Errorf("expected nil workout, got %+v", workout)
	}
}

func TestGetWorkout_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	workoutID := mustCreateWorkout(t, db, 1, "2026-01-12", "strength", 40, "Chest day", nil)

	workout, err := db.GetWorkout(context.Background(), 2, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout != nil {
		t.Errorf("expected nil workout for another chat, got %+v", workout)
	}
}

func TestAddWorkoutSet(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workoutID := mustCreateWorkout(t, db, 1, "2026-01-13", "strength", 60, "Leg day", nil)

	setID, err := db.AddWorkoutSet(ctx, storage.WorkoutSet{
		WorkoutID:  workoutID,
		Exercise:   "Squat",
		Sets:       3,
		Reps:       5,
		WeightKg:   100,
		DistanceKm: 0,
		Note:       "Heavy",
	})
	if err != nil {
		t.Fatalf("AddWorkoutSet returned error: %v", err)
	}
	if setID == 0 {
		t.Fatal("expected non-zero workout set id")
	}

	workout, err := db.GetWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout == nil {
		t.Fatal("expected workout, got nil")
	}
	if len(workout.Sets) != 1 {
		t.Fatalf("expected 1 set, got %d", len(workout.Sets))
	}
	if workout.Sets[0].ID != setID {
		t.Errorf("expected set id %d, got %d", setID, workout.Sets[0].ID)
	}
	if workout.Sets[0].Exercise != "Squat" {
		t.Errorf("expected exercise %q, got %q", "Squat", workout.Sets[0].Exercise)
	}
}

func TestListWorkouts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	firstID := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 45, "Push", nil)
	_ = firstID
	mustCreateWorkout(t, db, 1, "2026-01-02", "run", 30, "Easy run", nil)
	thirdID := mustCreateWorkout(t, db, 1, "2026-01-03", "strength", 50, "Pull", nil)

	all, err := db.ListWorkouts(ctx, 1, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkouts returned error: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 workouts, got %d", len(all))
	}
	if all[0].ID != thirdID {
		t.Errorf("expected most recent workout id %d first, got %d", thirdID, all[0].ID)
	}

	strengthOnly, err := db.ListWorkouts(ctx, 1, "strength", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkouts with filter returned error: %v", err)
	}
	if len(strengthOnly) != 2 {
		t.Fatalf("expected 2 strength workouts, got %d", len(strengthOnly))
	}
	for i, workout := range strengthOnly {
		if workout.Type != "strength" {
			t.Errorf("workout[%d]: expected type strength, got %q", i, workout.Type)
		}
	}
}

func TestListWorkouts_DateFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "A", nil)
	midID := mustCreateWorkout(t, db, 1, "2026-01-10", "strength", 40, "B", nil)
	mustCreateWorkout(t, db, 1, "2026-01-20", "strength", 50, "C", nil)

	workouts, err := db.ListWorkouts(ctx, 1, "", "2026-01-05", "2026-01-15", 0)
	if err != nil {
		t.Fatalf("ListWorkouts returned error: %v", err)
	}
	if len(workouts) != 1 {
		t.Fatalf("expected 1 workout, got %d", len(workouts))
	}
	if workouts[0].ID != midID {
		t.Errorf("expected workout id %d, got %d", midID, workouts[0].ID)
	}
}

func TestListWorkouts_Limit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		date := "2026-01-0" + string(rune('0'+i))
		mustCreateWorkout(t, db, 1, date, "strength", 20+i, "session", nil)
	}

	workouts, err := db.ListWorkouts(ctx, 1, "", "", "", 2)
	if err != nil {
		t.Fatalf("ListWorkouts returned error: %v", err)
	}
	if len(workouts) != 2 {
		t.Fatalf("expected 2 workouts, got %d", len(workouts))
	}
	if workouts[0].Date != "2026-01-05" {
		t.Errorf("expected most recent date 2026-01-05, got %q", workouts[0].Date)
	}
	if workouts[1].Date != "2026-01-04" {
		t.Errorf("expected second most recent date 2026-01-04, got %q", workouts[1].Date)
	}
}

func TestDeleteWorkout(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workoutID := mustCreateWorkout(t, db, 1, "2026-01-14", "strength", 55, "Delete me", nil)
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: workoutID, Exercise: "Deadlift", Sets: 1, Reps: 5, WeightKg: 140})

	if err := db.DeleteWorkout(ctx, 1, workoutID); err != nil {
		t.Fatalf("DeleteWorkout returned error: %v", err)
	}

	workout, err := db.GetWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout != nil {
		t.Errorf("expected deleted workout to be nil, got %+v", workout)
	}
}

func TestDeleteWorkout_NotFound(t *testing.T) {
	db := openTestDB(t)

	if err := db.DeleteWorkout(context.Background(), 1, 999); err == nil {
		t.Fatal("expected error deleting missing workout")
	}
}

func TestGetWorkoutStats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "A", nil)
	mustCreateWorkout(t, db, 1, "2026-01-02", "run", 60, "B", nil)
	mustCreateWorkout(t, db, 2, "2026-01-03", "yoga", 90, "Other chat", nil)

	stats, err := db.GetWorkoutStats(ctx, 1, "", "")
	if err != nil {
		t.Fatalf("GetWorkoutStats returned error: %v", err)
	}
	if stats == nil {
		t.Fatal("expected stats, got nil")
	}
	if stats.TotalCount != 2 {
		t.Errorf("expected total count 2, got %d", stats.TotalCount)
	}
	if stats.TotalMinutes != 90 {
		t.Errorf("expected total minutes 90, got %d", stats.TotalMinutes)
	}
	if stats.AvgMinutes != 45 {
		t.Errorf("expected avg minutes 45, got %d", stats.AvgMinutes)
	}
	if stats.TypeBreakdown["strength"] != 1 {
		t.Errorf("expected strength breakdown 1, got %d", stats.TypeBreakdown["strength"])
	}
	if stats.TypeBreakdown["run"] != 1 {
		t.Errorf("expected run breakdown 1, got %d", stats.TypeBreakdown["run"])
	}
	if len(stats.TypeBreakdown) != 2 {
		t.Errorf("expected 2 type entries, got %d", len(stats.TypeBreakdown))
	}
}

func TestGetPersonalRecords(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w1 := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 45, "A", nil)
	w2 := mustCreateWorkout(t, db, 1, "2026-01-02", "strength", 50, "B", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w1, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 60})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Bench Press", Sets: 3, Reps: 3, WeightKg: 80})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Squat", Sets: 3, Reps: 5, WeightKg: 100})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Run", DistanceKm: 5})

	prs, err := db.GetPersonalRecords(ctx, 1, "")
	if err != nil {
		t.Fatalf("GetPersonalRecords returned error: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("expected 2 personal records, got %d", len(prs))
	}

	bench := findPersonalRecord(prs, "Bench Press")
	if bench == nil {
		t.Fatal("expected Bench Press personal record")
	}
	if bench.WeightKg != 80 {
		t.Errorf("expected Bench Press weight 80, got %v", bench.WeightKg)
	}
	if bench.Reps != 3 {
		t.Errorf("expected Bench Press reps 3, got %d", bench.Reps)
	}
	if bench.WorkoutID != w2 {
		t.Errorf("expected Bench Press workout id %d, got %d", w2, bench.WorkoutID)
	}
	if bench.Date != "2026-01-02" {
		t.Errorf("expected Bench Press date 2026-01-02, got %q", bench.Date)
	}

	squat := findPersonalRecord(prs, "Squat")
	if squat == nil {
		t.Fatal("expected Squat personal record")
	}
	if squat.WeightKg != 100 {
		t.Errorf("expected Squat weight 100, got %v", squat.WeightKg)
	}
}

func TestGetPersonalRecords_SpecificExercise(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w1 := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 45, "A", nil)
	w2 := mustCreateWorkout(t, db, 1, "2026-01-02", "strength", 50, "B", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w1, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 70})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Squat", Sets: 3, Reps: 5, WeightKg: 110})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Bench Press", Sets: 3, Reps: 2, WeightKg: 85})

	prs, err := db.GetPersonalRecords(ctx, 1, "bench press")
	if err != nil {
		t.Fatalf("GetPersonalRecords returned error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 personal record, got %d", len(prs))
	}
	if prs[0].Exercise != "Bench Press" {
		t.Errorf("expected exercise %q, got %q", "Bench Press", prs[0].Exercise)
	}
	if prs[0].WeightKg != 85 {
		t.Errorf("expected weight 85, got %v", prs[0].WeightKg)
	}
}

func TestGetExerciseHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w1 := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 45, "A", nil)
	w2 := mustCreateWorkout(t, db, 1, "2026-01-05", "strength", 50, "B", nil)
	w3 := mustCreateWorkout(t, db, 1, "2026-01-10", "strength", 55, "C", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w1, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 40})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 50})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Bench Press", Sets: 2, Reps: 3, WeightKg: 55})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w3, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 45})

	history, err := db.GetExerciseHistory(ctx, 1, "bench press")
	if err != nil {
		t.Fatalf("GetExerciseHistory returned error: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}
	if history[0].WorkoutID != w3 || history[0].Date != "2026-01-10" || history[0].WeightKg != 45 {
		t.Errorf("unexpected newest history entry: %+v", history[0])
	}
	if history[1].WorkoutID != w2 || history[1].Date != "2026-01-05" || history[1].WeightKg != 55 {
		t.Errorf("unexpected middle history entry: %+v", history[1])
	}
	if history[2].WorkoutID != w1 || history[2].Date != "2026-01-01" || history[2].WeightKg != 40 {
		t.Errorf("unexpected oldest history entry: %+v", history[2])
	}
}

func TestGetNewPRsFromWorkout(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	previous := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 40, "Previous", nil)
	current := mustCreateWorkout(t, db, 1, "2026-01-10", "strength", 50, "Current", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: previous, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 50})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: current, Exercise: "Bench Press", Sets: 3, Reps: 3, WeightKg: 60})

	prs, err := db.GetNewPRsFromWorkout(ctx, 1, current)
	if err != nil {
		t.Fatalf("GetNewPRsFromWorkout returned error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 new PR, got %d", len(prs))
	}
	if prs[0].Exercise != "Bench Press" {
		t.Errorf("expected exercise %q, got %q", "Bench Press", prs[0].Exercise)
	}
	if prs[0].WeightKg != 60 {
		t.Errorf("expected weight 60, got %v", prs[0].WeightKg)
	}
	if prs[0].WorkoutID != current {
		t.Errorf("expected workout id %d, got %d", current, prs[0].WorkoutID)
	}
}

func TestGetNewPRsFromWorkout_NoNewPR(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	previous := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 40, "Previous", nil)
	current := mustCreateWorkout(t, db, 1, "2026-01-10", "strength", 50, "Current", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: previous, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 50})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: current, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 50})

	prs, err := db.GetNewPRsFromWorkout(ctx, 1, current)
	if err != nil {
		t.Fatalf("GetNewPRsFromWorkout returned error: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("expected no new PRs, got %+v", prs)
	}
}

func TestWorkoutTemplate_CRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	exercises := []storage.TemplateExercise{
		{Name: "Bench Press", Sets: 3, Reps: 5, WeightKg: 80, Note: "Top set"},
		{Name: "Row", Sets: 3, Reps: 8, WeightKg: 60},
	}

	templateID, err := db.CreateWorkoutTemplate(ctx, 1, "Push Day", exercises)
	if err != nil {
		t.Fatalf("CreateWorkoutTemplate returned error: %v", err)
	}
	if templateID == 0 {
		t.Fatal("expected non-zero template id")
	}

	templates, err := db.ListWorkoutTemplates(ctx, 1)
	if err != nil {
		t.Fatalf("ListWorkoutTemplates returned error: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	if templates[0].ID != templateID {
		t.Errorf("expected template id %d, got %d", templateID, templates[0].ID)
	}
	if templates[0].Name != "Push Day" {
		t.Errorf("expected template name %q, got %q", "Push Day", templates[0].Name)
	}
	if !reflect.DeepEqual(templates[0].Exercises, exercises) {
		t.Errorf("expected exercises %+v, got %+v", exercises, templates[0].Exercises)
	}

	template, err := db.GetWorkoutTemplate(ctx, 1, "Push Day")
	if err != nil {
		t.Fatalf("GetWorkoutTemplate returned error: %v", err)
	}
	if template == nil {
		t.Fatal("expected template, got nil")
	}
	if template.ID != templateID {
		t.Errorf("expected template id %d, got %d", templateID, template.ID)
	}
	if !reflect.DeepEqual(template.Exercises, exercises) {
		t.Errorf("expected exercises %+v, got %+v", exercises, template.Exercises)
	}

	if err := db.DeleteWorkoutTemplate(ctx, 1, templateID); err != nil {
		t.Fatalf("DeleteWorkoutTemplate returned error: %v", err)
	}

	deleted, err := db.GetWorkoutTemplate(ctx, 1, "Push Day")
	if err != nil {
		t.Fatalf("GetWorkoutTemplate after delete returned error: %v", err)
	}
	if deleted != nil {
		t.Errorf("expected deleted template to be nil, got %+v", deleted)
	}
}

func TestWorkoutTemplate_Upsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	firstExercises := []storage.TemplateExercise{{Name: "Bench Press", Sets: 3, Reps: 5, WeightKg: 70}}
	updatedExercises := []storage.TemplateExercise{{Name: "Bench Press", Sets: 5, Reps: 5, WeightKg: 75}, {Name: "Incline Press", Sets: 3, Reps: 8, WeightKg: 50}}

	_, err := db.CreateWorkoutTemplate(ctx, 1, "Push Day", firstExercises)
	if err != nil {
		t.Fatalf("first CreateWorkoutTemplate returned error: %v", err)
	}
	_, err = db.CreateWorkoutTemplate(ctx, 1, "Push Day", updatedExercises)
	if err != nil {
		t.Fatalf("second CreateWorkoutTemplate returned error: %v", err)
	}

	templates, err := db.ListWorkoutTemplates(ctx, 1)
	if err != nil {
		t.Fatalf("ListWorkoutTemplates returned error: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template after upsert, got %d", len(templates))
	}

	template, err := db.GetWorkoutTemplate(ctx, 1, "Push Day")
	if err != nil {
		t.Fatalf("GetWorkoutTemplate returned error: %v", err)
	}
	if template == nil {
		t.Fatal("expected template, got nil")
	}
	if !reflect.DeepEqual(template.Exercises, updatedExercises) {
		t.Errorf("expected updated exercises %+v, got %+v", updatedExercises, template.Exercises)
	}
}

func TestCountWorkoutsForGoal(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	goalID := mustCreateObjective(t, db, 1, "Run goal")

	mustCreateWorkout(t, db, 1, "2026-01-01", "run", 30, "A", &goalID)
	mustCreateWorkout(t, db, 1, "2026-01-02", "run", 35, "B", &goalID)
	mustCreateWorkout(t, db, 1, "2026-01-03", "run", 40, "C", nil)
	mustCreateWorkout(t, db, 2, "2026-01-04", "run", 45, "Other chat", &goalID)

	count, err := db.CountWorkoutsForGoal(ctx, 1, goalID)
	if err != nil {
		t.Fatalf("CountWorkoutsForGoal returned error: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestCountWorkoutsForGoal_Zero(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	goalID := mustCreateObjective(t, db, 1, "Empty goal")

	// No workouts linked to this goal.
	count, err := db.CountWorkoutsForGoal(ctx, 1, goalID)
	if err != nil {
		t.Fatalf("CountWorkoutsForGoal returned error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count 0, got %d", count)
	}
}

func TestGetWorkoutStats_DateFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "Jan", nil)
	mustCreateWorkout(t, db, 1, "2026-02-15", "run", 45, "Feb", nil)
	mustCreateWorkout(t, db, 1, "2026-03-20", "yoga", 60, "Mar", nil)

	// Filter with fromDate only — exercises the `if fromDate != ""` branch.
	statsFrom, err := db.GetWorkoutStats(ctx, 1, "2026-02-01", "")
	if err != nil {
		t.Fatalf("GetWorkoutStats (fromDate) returned error: %v", err)
	}
	if statsFrom.TotalCount != 2 {
		t.Errorf("expected 2 workouts from Feb, got %d", statsFrom.TotalCount)
	}

	// Filter with toDate only — exercises the `if toDate != ""` branch.
	statsTo, err := db.GetWorkoutStats(ctx, 1, "", "2026-02-28")
	if err != nil {
		t.Fatalf("GetWorkoutStats (toDate) returned error: %v", err)
	}
	if statsTo.TotalCount != 2 {
		t.Errorf("expected 2 workouts to Feb, got %d", statsTo.TotalCount)
	}

	// Filter with both fromDate and toDate.
	statsBoth, err := db.GetWorkoutStats(ctx, 1, "2026-02-01", "2026-02-28")
	if err != nil {
		t.Fatalf("GetWorkoutStats (both) returned error: %v", err)
	}
	if statsBoth.TotalCount != 1 {
		t.Errorf("expected 1 workout in Feb range, got %d", statsBoth.TotalCount)
	}
	if statsBoth.TotalMinutes != 45 {
		t.Errorf("expected 45 minutes, got %d", statsBoth.TotalMinutes)
	}
	if statsBoth.AvgMinutes != 45 {
		t.Errorf("expected avg 45, got %d", statsBoth.AvgMinutes)
	}
	if statsBoth.TypeBreakdown["run"] != 1 {
		t.Errorf("expected run breakdown 1, got %d", statsBoth.TypeBreakdown["run"])
	}
}

func TestGetWorkoutStats_Empty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// No workouts for this chat — TotalCount=0 so AvgMinutes stays 0.
	stats, err := db.GetWorkoutStats(ctx, 99, "", "")
	if err != nil {
		t.Fatalf("GetWorkoutStats returned error: %v", err)
	}
	if stats == nil {
		t.Fatal("expected stats, got nil")
	}
	if stats.TotalCount != 0 {
		t.Errorf("expected total count 0, got %d", stats.TotalCount)
	}
	if stats.TotalMinutes != 0 {
		t.Errorf("expected total minutes 0, got %d", stats.TotalMinutes)
	}
	if stats.AvgMinutes != 0 {
		t.Errorf("expected avg minutes 0, got %d", stats.AvgMinutes)
	}
}

func TestListWorkouts_Empty(t *testing.T) {
	db := openTestDB(t)

	workouts, err := db.ListWorkouts(context.Background(), 99, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkouts returned error: %v", err)
	}
	if len(workouts) != 0 {
		t.Errorf("expected 0 workouts, got %d", len(workouts))
	}
}

func TestListWorkouts_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "Chat 1", nil)
	mustCreateWorkout(t, db, 2, "2026-01-01", "run", 40, "Chat 2", nil)

	workouts1, err := db.ListWorkouts(ctx, 1, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListWorkouts chat 1 returned error: %v", err)
	}
	if len(workouts1) != 1 {
		t.Errorf("expected 1 workout for chat 1, got %d", len(workouts1))
	}
	if workouts1[0].ChatID != 1 {
		t.Errorf("expected chat_id 1, got %d", workouts1[0].ChatID)
	}
}

func TestGetPersonalRecords_Empty(t *testing.T) {
	db := openTestDB(t)

	prs, err := db.GetPersonalRecords(context.Background(), 99, "")
	if err != nil {
		t.Fatalf("GetPersonalRecords returned error: %v", err)
	}
	if len(prs) != 0 {
		t.Errorf("expected 0 PRs, got %d", len(prs))
	}
}

func TestGetPersonalRecords_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w1 := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "Chat 1", nil)
	w2 := mustCreateWorkout(t, db, 2, "2026-01-01", "strength", 30, "Chat 2", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w1, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 80})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 120})

	// Chat 1 should only see its own PR (80 kg), not chat 2's (120 kg).
	prs, err := db.GetPersonalRecords(ctx, 1, "")
	if err != nil {
		t.Fatalf("GetPersonalRecords returned error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR for chat 1, got %d", len(prs))
	}
	if prs[0].WeightKg != 80 {
		t.Errorf("expected weight 80, got %v", prs[0].WeightKg)
	}
}

func TestGetExerciseHistory_Empty(t *testing.T) {
	db := openTestDB(t)

	history, err := db.GetExerciseHistory(context.Background(), 99, "Bench Press")
	if err != nil {
		t.Fatalf("GetExerciseHistory returned error: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected 0 history entries, got %d", len(history))
	}
}

func TestGetExerciseHistory_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	w1 := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "Chat 1", nil)
	w2 := mustCreateWorkout(t, db, 2, "2026-01-01", "strength", 30, "Chat 2", nil)

	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w1, Exercise: "Squat", Sets: 3, Reps: 5, WeightKg: 100})
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: w2, Exercise: "Squat", Sets: 3, Reps: 5, WeightKg: 150})

	history, err := db.GetExerciseHistory(ctx, 1, "squat")
	if err != nil {
		t.Fatalf("GetExerciseHistory returned error: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry for chat 1, got %d", len(history))
	}
	if history[0].WeightKg != 100 {
		t.Errorf("expected weight 100, got %v", history[0].WeightKg)
	}
}

func TestGetNewPRsFromWorkout_FirstEver(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// First time ever doing the exercise — no prior history → must be reported as a PR.
	workoutID := mustCreateWorkout(t, db, 1, "2026-01-01", "strength", 30, "First", nil)
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: workoutID, Exercise: "Deadlift", Sets: 1, Reps: 5, WeightKg: 100})

	prs, err := db.GetNewPRsFromWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetNewPRsFromWorkout returned error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 new PR for first-ever lift, got %d", len(prs))
	}
	if prs[0].Exercise != "Deadlift" {
		t.Errorf("expected exercise %q, got %q", "Deadlift", prs[0].Exercise)
	}
	if prs[0].WeightKg != 100 {
		t.Errorf("expected weight 100, got %v", prs[0].WeightKg)
	}
}

func TestGetNewPRsFromWorkout_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// Chat 2 has a previous heavy lift that should NOT count as prior history for chat 1.
	prevChat2 := mustCreateWorkout(t, db, 2, "2026-01-01", "strength", 30, "Chat 2 prev", nil)
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: prevChat2, Exercise: "OHP", Sets: 3, Reps: 5, WeightKg: 150})

	// Chat 1's first lift: should still be reported as a PR (no prior in chat 1).
	currentChat1 := mustCreateWorkout(t, db, 1, "2026-01-10", "strength", 40, "Chat 1 new", nil)
	mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: currentChat1, Exercise: "OHP", Sets: 3, Reps: 5, WeightKg: 80})

	prs, err := db.GetNewPRsFromWorkout(ctx, 1, currentChat1)
	if err != nil {
		t.Fatalf("GetNewPRsFromWorkout returned error: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 new PR (isolated to chat 1), got %d", len(prs))
	}
	if prs[0].WeightKg != 80 {
		t.Errorf("expected weight 80, got %v", prs[0].WeightKg)
	}
}

func TestListWorkoutTemplates_Empty(t *testing.T) {
	db := openTestDB(t)

	templates, err := db.ListWorkoutTemplates(context.Background(), 99)
	if err != nil {
		t.Fatalf("ListWorkoutTemplates returned error: %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("expected 0 templates, got %d", len(templates))
	}
}

func TestListWorkoutTemplates_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	exercises := []storage.TemplateExercise{{Name: "Bench Press", Sets: 3, Reps: 5, WeightKg: 60}}

	_, err := db.CreateWorkoutTemplate(ctx, 1, "Push Day", exercises)
	if err != nil {
		t.Fatalf("CreateWorkoutTemplate returned error: %v", err)
	}

	templates, err := db.ListWorkoutTemplates(ctx, 2)
	if err != nil {
		t.Fatalf("ListWorkoutTemplates for other chat returned error: %v", err)
	}
	if len(templates) != 0 {
		t.Errorf("expected 0 templates for other chat, got %d", len(templates))
	}
}

func TestGetWorkoutTemplate_NotFound(t *testing.T) {
	db := openTestDB(t)

	tmpl, err := db.GetWorkoutTemplate(context.Background(), 1, "NonExistent")
	if err != nil {
		t.Fatalf("GetWorkoutTemplate returned error: %v", err)
	}
	if tmpl != nil {
		t.Errorf("expected nil for missing template, got %+v", tmpl)
	}
}

func TestDeleteWorkoutTemplate_NotFound(t *testing.T) {
	db := openTestDB(t)

	err := db.DeleteWorkoutTemplate(context.Background(), 1, 99999)
	if err == nil {
		t.Fatal("expected error deleting non-existent template")
	}
}

func TestDeleteWorkoutTemplate_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	exercises := []storage.TemplateExercise{{Name: "Pull-up", Sets: 3, Reps: 10}}

	templateID, err := db.CreateWorkoutTemplate(ctx, 1, "Pull Day", exercises)
	if err != nil {
		t.Fatalf("CreateWorkoutTemplate returned error: %v", err)
	}

	// Deleting from chat 2 must fail (wrong owner).
	if err := db.DeleteWorkoutTemplate(ctx, 2, templateID); err == nil {
		t.Fatal("expected error when deleting template from wrong chat")
	}

	// Template must still exist for the original owner.
	tmpl, err := db.GetWorkoutTemplate(ctx, 1, "Pull Day")
	if err != nil {
		t.Fatalf("GetWorkoutTemplate returned error: %v", err)
	}
	if tmpl == nil {
		t.Fatal("expected template to still exist after failed cross-chat delete")
	}
}

func TestDeleteWorkout_ChatIsolation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workoutID := mustCreateWorkout(t, db, 1, "2026-05-01", "strength", 30, "Mine", nil)

	// Deleting from a different chat must fail.
	if err := db.DeleteWorkout(ctx, 2, workoutID); err == nil {
		t.Fatal("expected error when deleting workout from wrong chat")
	}

	// Workout must still be accessible by the original owner.
	workout, err := db.GetWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if workout == nil {
		t.Fatal("expected workout to still exist after failed cross-chat delete")
	}
}

func TestAddWorkoutSet_OrderedByInsertion(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	workoutID := mustCreateWorkout(t, db, 1, "2026-05-02", "strength", 60, "Order test", nil)

	id1 := mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: workoutID, Exercise: "Squat", Sets: 3, Reps: 5, WeightKg: 80})
	id2 := mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: workoutID, Exercise: "Bench Press", Sets: 3, Reps: 5, WeightKg: 60})
	id3 := mustAddWorkoutSet(t, db, storage.WorkoutSet{WorkoutID: workoutID, Exercise: "Row", Sets: 3, Reps: 8, WeightKg: 50})

	workout, err := db.GetWorkout(ctx, 1, workoutID)
	if err != nil {
		t.Fatalf("GetWorkout returned error: %v", err)
	}
	if len(workout.Sets) != 3 {
		t.Fatalf("expected 3 sets, got %d", len(workout.Sets))
	}
	wantOrder := []int64{id1, id2, id3}
	for i, wantID := range wantOrder {
		if workout.Sets[i].ID != wantID {
			t.Errorf("set[%d]: expected id %d, got %d", i, wantID, workout.Sets[i].ID)
		}
	}
}
