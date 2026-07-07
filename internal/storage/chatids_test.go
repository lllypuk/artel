package storage

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func testDB(t *testing.T) *DB {
	db, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestGetOrCreateChatID_MapsAndReuses(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id1, err := db.GetOrCreateChatID(ctx, "mattermost", "channel-abc")
	require.NoError(t, err)
	require.Greater(t, id1, int64(0), "expected positive chat id")

	id2, err := db.GetOrCreateChatID(ctx, "mattermost", "channel-abc")
	require.NoError(t, err)
	require.Equal(t, id1, id2, "same external id should map to same internal id")

	id3, err := db.GetOrCreateChatID(ctx, "telegram", "12345")
	require.NoError(t, err)
	require.NotEqual(t, id1, id3, "different platform should map to different id")
}

func TestResolveChatID_RoundTrip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	id, err := db.GetOrCreateChatID(ctx, "mattermost", "channel-xyz")
	require.NoError(t, err)

	platform, externalID, err := db.ResolveChatID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "mattermost", platform)
	require.Equal(t, "channel-xyz", externalID)
}

func TestResolveChatID_NotFound(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, _, err := db.ResolveChatID(ctx, 999999)
	require.Error(t, err)
}

func TestGetOrCreateChatID_RequiresPlatformAndExternalID(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, err := db.GetOrCreateChatID(ctx, "", "x")
	require.Error(t, err)
	_, err = db.GetOrCreateChatID(ctx, "mattermost", "")
	require.Error(t, err)
}
