package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openDisciplineTestDB(t *testing.T) *storage.DB {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "discipline_test_*.db")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	db, err := storage.Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	return db
}

// ── Day Reviews ───────────────────────────────────────────────────────────────

func TestSaveDayReview_CreateAndGet(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	r := storage.DayReview{
		ChatID:  1,
		Date:    "2026-04-18",
		Planned: "написать тесты",
		Done:    "написал тесты",
		Score:   8,
		Mood:    "хорошее",
	}
	require.NoError(t, db.SaveDayReview(ctx, r))

	got, err := db.GetDayReview(ctx, 1, "2026-04-18")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, r.Planned, got.Planned)
	assert.Equal(t, r.Done, got.Done)
	assert.Equal(t, 8, got.Score)
	assert.Equal(t, "хорошее", got.Mood)
}

func TestSaveDayReview_Upsert(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	r := storage.DayReview{ChatID: 1, Date: "2026-04-18", Score: 5}
	require.NoError(t, db.SaveDayReview(ctx, r))

	// Update the same date.
	r.Score = 9
	r.Note = "отличный день"
	require.NoError(t, db.SaveDayReview(ctx, r))

	got, err := db.GetDayReview(ctx, 1, "2026-04-18")
	require.NoError(t, err)
	assert.Equal(t, 9, got.Score)
	assert.Equal(t, "отличный день", got.Note)
}

func TestGetDayReview_NotFound(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	got, err := db.GetDayReview(ctx, 1, "2026-01-01")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestListDayReviews(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		date := time.Date(2026, 4, i, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 1, Date: date, Score: i}))
	}

	reviews, err := db.ListDayReviews(ctx, 1, 3)
	require.NoError(t, err)
	assert.Len(t, reviews, 3)
	// Should be ordered by date DESC.
	assert.Equal(t, "2026-04-05", reviews[0].Date)
	assert.Equal(t, "2026-04-04", reviews[1].Date)
}

func TestListDayReviews_IsolatedByChatID(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 1, Date: "2026-04-18"}))
	require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 2, Date: "2026-04-18"}))

	reviews, err := db.ListDayReviews(ctx, 1, 10)
	require.NoError(t, err)
	assert.Len(t, reviews, 1)
}

func TestGetDayReviewStats_Streak(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	// Save reviews for today and the 2 days before.
	for i := range 3 {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 1, Date: date, Score: 7}))
	}

	stats, err := db.GetDayReviewStats(ctx, 1, 30)
	require.NoError(t, err)
	assert.Equal(t, 3, stats.Streak)
	assert.InDelta(t, 7.0, stats.AvgScore, 0.01)
	assert.Equal(t, 3, stats.Count)
}

func TestGetDayReviewStats_BrokenStreak(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	today := time.Now().Format("2006-01-02")
	twoDaysAgo := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
	require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 1, Date: today}))
	// Gap: yesterday is missing.
	require.NoError(t, db.SaveDayReview(ctx, storage.DayReview{ChatID: 1, Date: twoDaysAgo}))

	stats, err := db.GetDayReviewStats(ctx, 1, 30)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Streak) // streak broken by gap
}

func TestGetDayReviewStats_Empty(t *testing.T) {
	db := openDisciplineTestDB(t)
	stats, err := db.GetDayReviewStats(context.Background(), 1, 30)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Streak)
	assert.Equal(t, 0, stats.Count)
}

// ── Week Reviews ──────────────────────────────────────────────────────────────

func TestSaveWeekReview_CreateAndGet(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	r := storage.WeekReview{
		ChatID:       1,
		Week:         "2026-W16",
		Wins:         "завершил эпик D",
		Fails:        "не успел с тестами",
		Pattern:      "откладываю сложное на потом",
		NextWeekPlan: "начать с трудного",
		Score:        7,
	}
	require.NoError(t, db.SaveWeekReview(ctx, r))

	got, err := db.GetWeekReview(ctx, 1, "2026-W16")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, r.Wins, got.Wins)
	assert.Equal(t, r.Pattern, got.Pattern)
	assert.Equal(t, 7, got.Score)
}

func TestSaveWeekReview_Upsert(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.SaveWeekReview(ctx, storage.WeekReview{ChatID: 1, Week: "2026-W16", Score: 5}))
	require.NoError(t, db.SaveWeekReview(ctx, storage.WeekReview{ChatID: 1, Week: "2026-W16", Score: 8, Wins: "обновлено"}))

	got, err := db.GetWeekReview(ctx, 1, "2026-W16")
	require.NoError(t, err)
	assert.Equal(t, 8, got.Score)
	assert.Equal(t, "обновлено", got.Wins)
}

func TestGetWeekReview_NotFound(t *testing.T) {
	db := openDisciplineTestDB(t)
	got, err := db.GetWeekReview(context.Background(), 1, "2020-W01")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Objectives ────────────────────────────────────────────────────────────────

func TestCreateAndGetObjective(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	obj := storage.Objective{
		ChatID:      1,
		Title:       "Запустить продукт",
		Description: "MVP за Q2",
		Horizon:     "quarter",
		Period:      "2026-Q2",
		Status:      "active",
	}
	id, err := db.CreateObjective(ctx, obj)
	require.NoError(t, err)
	assert.Positive(t, id)

	got, err := db.GetObjective(ctx, 1, id)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "Запустить продукт", got.Title)
	assert.Equal(t, "quarter", got.Horizon)
	assert.Equal(t, "active", got.Status)
}

func TestListObjectives_FilterByStatus(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	_, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "A", Status: "active", Horizon: "quarter"})
	require.NoError(t, err)
	id2, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "B", Status: "active", Horizon: "quarter"})
	require.NoError(t, err)
	require.NoError(t, db.UpdateObjective(ctx, 1, id2, "B", "", "done", 100))

	active, err := db.ListObjectives(ctx, 1, "active")
	require.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, "A", active[0].Title)

	all, err := db.ListObjectives(ctx, 1, "all")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestUpdateObjective(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	id, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "Цель", Horizon: "year", Status: "active"})
	require.NoError(t, err)

	require.NoError(t, db.UpdateObjective(ctx, 1, id, "Новая цель", "описание", "done", 100))

	got, err := db.GetObjective(ctx, 1, id)
	require.NoError(t, err)
	assert.Equal(t, "Новая цель", got.Title)
	assert.Equal(t, "done", got.Status)
	assert.Equal(t, 100, got.Progress)
}

func TestDeleteObjective(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	id, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "X", Horizon: "month", Status: "active"})
	require.NoError(t, err)

	require.NoError(t, db.DeleteObjective(ctx, 1, id))

	got, err := db.GetObjective(ctx, 1, id)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestObjective_NotFoundReturnsNil(t *testing.T) {
	db := openDisciplineTestDB(t)
	got, err := db.GetObjective(context.Background(), 1, 9999)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ── Key Results ───────────────────────────────────────────────────────────────

func TestKeyResults_AddAndUpdate(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	objID, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "OKR", Horizon: "quarter", Status: "active"})
	require.NoError(t, err)

	krID, err := db.AddKeyResult(ctx, storage.KeyResult{
		ObjectiveID: objID,
		Title:       "Написать 10 тестов",
		Deadline:    "2026-06-30",
	})
	require.NoError(t, err)
	assert.Positive(t, krID)

	require.NoError(t, db.UpdateKeyResult(ctx, 1, krID, 50, "на полпути"))

	obj, err := db.GetObjective(ctx, 1, objID)
	require.NoError(t, err)
	require.Len(t, obj.KeyResults, 1)
	assert.Equal(t, 50, obj.KeyResults[0].Progress)
	assert.Equal(t, "на полпути", obj.KeyResults[0].Note)
}

func TestKeyResults_DeleteCascade(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	objID, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "OKR", Horizon: "quarter", Status: "active"})
	require.NoError(t, err)
	_, err = db.AddKeyResult(ctx, storage.KeyResult{ObjectiveID: objID, Title: "KR1"})
	require.NoError(t, err)

	// Deleting objective should cascade-delete key results.
	require.NoError(t, db.DeleteObjective(ctx, 1, objID))

	got, err := db.GetObjective(ctx, 1, objID)
	require.NoError(t, err)
	assert.Nil(t, got) // objective gone, no panic
}

func TestGetKeyResult_IsolatedByChatID(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	// Create objective and KR for chat 1.
	objID, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "OKR", Horizon: "quarter", Status: "active"})
	require.NoError(t, err)
	krID, err := db.AddKeyResult(ctx, storage.KeyResult{ObjectiveID: objID, Title: "KR1"})
	require.NoError(t, err)

	// Chat 1 can see it.
	kr, err := db.GetKeyResult(ctx, 1, krID)
	require.NoError(t, err)
	assert.NotNil(t, kr)

	// Chat 2 cannot see it.
	kr2, err := db.GetKeyResult(ctx, 2, krID)
	require.NoError(t, err)
	assert.Nil(t, kr2)
}

func TestUpdateKeyResult_IsolatedByChatID(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	objID, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "OKR", Horizon: "quarter", Status: "active"})
	require.NoError(t, err)
	krID, err := db.AddKeyResult(ctx, storage.KeyResult{ObjectiveID: objID, Title: "KR1", Progress: 10})
	require.NoError(t, err)

	// Chat 2 cannot update chat 1's KR.
	err = db.UpdateKeyResult(ctx, 2, krID, 99, "hacked")
	assert.Error(t, err, "update by wrong chat should fail")

	// Original values unchanged.
	kr, err := db.GetKeyResult(ctx, 1, krID)
	require.NoError(t, err)
	assert.Equal(t, 10, kr.Progress)
}

func TestDeleteKeyResult_IsolatedByChatID(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	objID, err := db.CreateObjective(ctx, storage.Objective{ChatID: 1, Title: "OKR", Horizon: "quarter", Status: "active"})
	require.NoError(t, err)
	krID, err := db.AddKeyResult(ctx, storage.KeyResult{ObjectiveID: objID, Title: "KR1"})
	require.NoError(t, err)

	// Chat 2 cannot delete chat 1's KR (no error, but no rows affected).
	require.NoError(t, db.DeleteKeyResult(ctx, 2, krID))

	// KR still exists for chat 1.
	kr, err := db.GetKeyResult(ctx, 1, krID)
	require.NoError(t, err)
	assert.NotNil(t, kr)
}

// ── GetStaleObjectives (P-5) ──────────────────────────────────────────────────

func TestGetStaleObjectives_Empty(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	// Threshold far in the future: every objective would be considered stale.
	threshold := time.Now().Add(24 * time.Hour)
	result, err := db.GetStaleObjectives(ctx, 1, threshold)
	require.NoError(t, err)
	assert.Empty(t, result, "no objectives → empty result")
}

func TestGetStaleObjectives_ReturnsStale(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	// Create two active objectives.
	_, err := db.CreateObjective(ctx, storage.Objective{
		ChatID: 1, Title: "Old goal", Horizon: "quarter", Status: "active",
	})
	require.NoError(t, err)
	_, err = db.CreateObjective(ctx, storage.Objective{
		ChatID: 1, Title: "Another goal", Horizon: "year", Status: "active",
	})
	require.NoError(t, err)

	// Threshold far in the future → both are stale (updated_at = created_at ≈ now).
	threshold := time.Now().Add(time.Hour)
	result, err := db.GetStaleObjectives(ctx, 1, threshold)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetStaleObjectives_SkipsDone(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	id, err := db.CreateObjective(ctx, storage.Objective{
		ChatID: 1, Title: "Done goal", Horizon: "quarter", Status: "active",
	})
	require.NoError(t, err)
	require.NoError(t, db.UpdateObjective(ctx, 1, id, "Done goal", "", "done", 100))

	threshold := time.Now().Add(time.Hour)
	result, err := db.GetStaleObjectives(ctx, 1, threshold)
	require.NoError(t, err)
	assert.Empty(t, result, "done objectives must not appear in stale list")
}

func TestGetStaleObjectives_SkipsRecentlyUpdated(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	_, err := db.CreateObjective(ctx, storage.Objective{
		ChatID: 1, Title: "Fresh goal", Horizon: "quarter", Status: "active",
	})
	require.NoError(t, err)

	// Threshold in the past → objective was just created, so updated_at > threshold.
	threshold := time.Now().Add(-time.Hour)
	result, err := db.GetStaleObjectives(ctx, 1, threshold)
	require.NoError(t, err)
	assert.Empty(t, result, "recently created/updated objective must not be stale")
}

func TestGetStaleObjectives_IsolatedByChat(t *testing.T) {
	db := openDisciplineTestDB(t)
	ctx := context.Background()

	_, err := db.CreateObjective(ctx, storage.Objective{
		ChatID: 1, Title: "Chat1 goal", Horizon: "quarter", Status: "active",
	})
	require.NoError(t, err)
	_, err = db.CreateObjective(ctx, storage.Objective{
		ChatID: 2, Title: "Chat2 goal", Horizon: "quarter", Status: "active",
	})
	require.NoError(t, err)

	threshold := time.Now().Add(time.Hour)
	result, err := db.GetStaleObjectives(ctx, 1, threshold)
	require.NoError(t, err)
	assert.Len(t, result, 1, "only chat 1 objectives should appear")
	assert.Equal(t, "Chat1 goal", result[0].Title)
}
