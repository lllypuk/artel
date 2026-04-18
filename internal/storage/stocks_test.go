package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveAndGetQuoteCache_Hit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	err := db.SaveQuoteCache(ctx, "AAPL", "yahoo", `{"price":150.0}`)
	require.NoError(t, err)

	data, err := db.GetQuoteCache(ctx, "AAPL", "yahoo", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, `{"price":150.0}`, data)
}

func TestGetQuoteCache_Miss(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	data, err := db.GetQuoteCache(ctx, "UNKNOWN", "yahoo", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "", data)
}

func TestGetQuoteCache_Expired(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Save with a TTL of 0 so it's immediately expired.
	err := db.SaveQuoteCache(ctx, "SBER", "moex_stock", `{"price":250.0}`)
	require.NoError(t, err)

	// TTL of 0 means any record is considered expired.
	data, err := db.GetQuoteCache(ctx, "SBER", "moex_stock", 0)
	require.NoError(t, err)
	assert.Equal(t, "", data)
}

func TestSaveQuoteCache_Upsert(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.SaveQuoteCache(ctx, "IMOEX", "moex_index", `{"price":3000.0}`))
	require.NoError(t, db.SaveQuoteCache(ctx, "IMOEX", "moex_index", `{"price":3100.0}`))

	data, err := db.GetQuoteCache(ctx, "IMOEX", "moex_index", 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, `{"price":3100.0}`, data)
}

func TestGetQuoteCache_ZeroTTL_AlwaysMiss(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	require.NoError(t, db.SaveQuoteCache(ctx, "MSFT", "yahoo", `{"price":400.0}`))

	// TTL=0 disables cache — always miss.
	data, err := db.GetQuoteCache(ctx, "MSFT", "yahoo", 0)
	require.NoError(t, err)
	assert.Equal(t, "", data)
}
