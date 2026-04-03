package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordUsageAndGetToday(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Initially no usage today.
	stats, err := db.GetUsageToday(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.PromptTokens)
	assert.Equal(t, 0, stats.CompletionTokens)
	assert.Equal(t, 0, stats.RequestsCount)

	// Record first call.
	require.NoError(t, db.RecordUsage(ctx, 100, 50))

	stats, err = db.GetUsageToday(ctx)
	require.NoError(t, err)
	assert.Equal(t, 100, stats.PromptTokens)
	assert.Equal(t, 50, stats.CompletionTokens)
	assert.Equal(t, 1, stats.RequestsCount)

	// Record second call — should accumulate.
	require.NoError(t, db.RecordUsage(ctx, 200, 80))

	stats, err = db.GetUsageToday(ctx)
	require.NoError(t, err)
	assert.Equal(t, 300, stats.PromptTokens)
	assert.Equal(t, 130, stats.CompletionTokens)
	assert.Equal(t, 2, stats.RequestsCount)
}

func TestGetUsageLast7Days(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.RecordUsage(ctx, 500, 250))
	require.NoError(t, db.RecordUsage(ctx, 300, 100))

	week, err := db.GetUsageLast7Days(ctx)
	require.NoError(t, err)
	require.Len(t, week, 1)
	assert.Equal(t, 800, week[0].PromptTokens)
	assert.Equal(t, 350, week[0].CompletionTokens)
	assert.Equal(t, 2, week[0].RequestsCount)
}

func TestGetDBStats(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Create some data.
	convID, err := db.CreateConversation(ctx, 1, "test-model")
	require.NoError(t, err)
	require.NoError(t, db.AddMessage(ctx, convID, "user", "hello", "", ""))
	_, err = db.CreateNote(ctx, 1, "test", "body", "")
	require.NoError(t, err)

	stats, err := db.GetDBStats(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.ConversationsCount)
	assert.Equal(t, 1, stats.MessagesCount)
	assert.Equal(t, 1, stats.NotesCount)
	assert.Equal(t, 0, stats.RemindersCount)
}
