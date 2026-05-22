package storage

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	f, err := os.CreateTemp("", "wiki_test_*.db")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	f.Close()

	db, err := Open(f.Name())
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestWikiPageCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	// Create
	id, err := db.CreateWikiPage(ctx, chatID, "/dev/golang", "Go Notes", "Content here", "go,dev")
	require.NoError(t, err)
	assert.Positive(t, id)

	// Get by path
	page, err := db.GetWikiPage(ctx, chatID, "/dev/golang")
	require.NoError(t, err)
	require.NotNil(t, page)
	assert.Equal(t, "/dev/golang", page.Path)
	assert.Equal(t, "Go Notes", page.Title)

	// Get by ID
	page2, err := db.GetWikiPageByID(ctx, chatID, id)
	require.NoError(t, err)
	require.NotNil(t, page2)
	assert.Equal(t, id, page2.ID)

	// Update
	err = db.UpdateWikiPage(ctx, chatID, "/dev/golang", "Go Notes Updated", "New content", "go,dev,updated")
	require.NoError(t, err)
	page, err = db.GetWikiPage(ctx, chatID, "/dev/golang")
	require.NoError(t, err)
	assert.Equal(t, "Go Notes Updated", page.Title)
	assert.Equal(t, "New content", page.Content)

	// Delete
	err = db.DeleteWikiPage(ctx, chatID, "/dev/golang")
	require.NoError(t, err)
	page, err = db.GetWikiPage(ctx, chatID, "/dev/golang")
	require.NoError(t, err)
	assert.Nil(t, page)
}

func TestWikiPageList(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	paths := []string{"/dev/golang", "/dev/python", "/health/running", "/books/clean-code"}
	for _, p := range paths {
		_, err := db.CreateWikiPage(ctx, chatID, p, p, "", "")
		require.NoError(t, err)
	}

	// List section
	pages, err := db.ListWikiPages(ctx, chatID, "/dev")
	require.NoError(t, err)
	assert.Len(t, pages, 2)

	// All pages
	all, err := db.AllWikiPages(ctx, chatID)
	require.NoError(t, err)
	assert.Len(t, all, 4)

	// Different chat sees nothing
	other, err := db.AllWikiPages(ctx, 999)
	require.NoError(t, err)
	assert.Empty(t, other)
}

func TestWikiPageRename(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	// Create two pages
	_, err := db.CreateWikiPage(ctx, chatID, "/dev/golang", "Go", "content [[/dev/python]]", "")
	require.NoError(t, err)
	_, err = db.CreateWikiPage(ctx, chatID, "/dev/python", "Python", "content [[/dev/golang]]", "")
	require.NoError(t, err)

	// Add links: golang -> python, python -> golang
	err = db.UpsertWikiLinks(ctx, chatID, "/dev/golang", []string{"/dev/python"})
	require.NoError(t, err)
	err = db.UpsertWikiLinks(ctx, chatID, "/dev/python", []string{"/dev/golang"})
	require.NoError(t, err)

	// Rename golang -> programming/golang
	err = db.RenameWikiPage(ctx, chatID, "/dev/golang", "/programming/golang")
	require.NoError(t, err)

	// Old path gone, new path exists
	old, err := db.GetWikiPage(ctx, chatID, "/dev/golang")
	require.NoError(t, err)
	assert.Nil(t, old)

	newPage, err := db.GetWikiPage(ctx, chatID, "/programming/golang")
	require.NoError(t, err)
	require.NotNil(t, newPage)

	// Backlinks to /programming/golang from /dev/python should be updated
	backlinks, err := db.GetBacklinks(ctx, chatID, "/programming/golang")
	require.NoError(t, err)
	require.Len(t, backlinks, 1)
	assert.Equal(t, "/dev/python", backlinks[0].Path)

	// Links from /programming/golang should point to /dev/python
	// (the from_path in wiki_links was updated)
	backlinks2, err := db.GetBacklinks(ctx, chatID, "/dev/python")
	require.NoError(t, err)
	require.Len(t, backlinks2, 1)
	assert.Equal(t, "/programming/golang", backlinks2[0].Path)
}

func TestWikiPageDeleteCleansLinks(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	_, err := db.CreateWikiPage(ctx, chatID, "/a", "A", "", "")
	require.NoError(t, err)
	_, err = db.CreateWikiPage(ctx, chatID, "/b", "B", "", "")
	require.NoError(t, err)

	err = db.UpsertWikiLinks(ctx, chatID, "/a", []string{"/b"})
	require.NoError(t, err)

	// Delete /a — links should be cleaned
	err = db.DeleteWikiPage(ctx, chatID, "/a")
	require.NoError(t, err)

	backlinks, err := db.GetBacklinks(ctx, chatID, "/b")
	require.NoError(t, err)
	assert.Empty(t, backlinks)
}

func TestWikiPageSearch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	_, err := db.CreateWikiPage(ctx, chatID, "/dev/golang", "Go Notes", "goroutine channels", "go")
	require.NoError(t, err)
	_, err = db.CreateWikiPage(ctx, chatID, "/health/running", "Running", "marathon training", "sport")
	require.NoError(t, err)

	results, err := db.SearchWikiPages(ctx, chatID, "goroutine")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "/dev/golang", results[0].Path)

	// Empty query returns all
	all, err := db.SearchWikiPages(ctx, chatID, "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestReadingListCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	// Create
	id, err := db.CreateReadingItem(ctx, chatID, "https://example.com", "Example Article", "summary", "content", "to-read", "high", "go,tech")
	require.NoError(t, err)
	assert.Positive(t, id)

	// Get
	item, err := db.GetReadingItem(ctx, chatID, id)
	require.NoError(t, err)
	require.NotNil(t, item)
	assert.Equal(t, "Example Article", item.Title)
	assert.Equal(t, "to-read", item.Status)
	assert.Equal(t, "high", item.Priority)

	// Update status
	err = db.UpdateReadingItem(ctx, chatID, id, "", "reading", "", "", false)
	require.NoError(t, err)
	item, err = db.GetReadingItem(ctx, chatID, id)
	require.NoError(t, err)
	assert.Equal(t, "reading", item.Status)

	// Mark as done
	err = db.UpdateReadingItem(ctx, chatID, id, "", "done", "", "", true)
	require.NoError(t, err)
	item, err = db.GetReadingItem(ctx, chatID, id)
	require.NoError(t, err)
	assert.Equal(t, "done", item.Status)
	assert.NotNil(t, item.ReadAt)

	// Delete
	err = db.DeleteReadingItem(ctx, chatID, id)
	require.NoError(t, err)
	item, err = db.GetReadingItem(ctx, chatID, id)
	require.NoError(t, err)
	assert.Nil(t, item)
}

func TestReadingListFilter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	_, _ = db.CreateReadingItem(ctx, chatID, "https://a.com", "Article A", "", "", "to-read", "high", "go")
	_, _ = db.CreateReadingItem(ctx, chatID, "https://b.com", "Article B", "", "", "reading", "medium", "rust")
	_, _ = db.CreateReadingItem(ctx, chatID, "https://c.com", "Article C", "", "", "to-read", "low", "go")

	// Filter by status
	items, err := db.ListReadingItems(ctx, chatID, "to-read", "", "")
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// Filter by tags
	items, err = db.ListReadingItems(ctx, chatID, "", "", "rust")
	require.NoError(t, err)
	assert.Len(t, items, 1)

	// Filter by priority
	items, err = db.ListReadingItems(ctx, chatID, "", "high", "")
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestReadingListDigest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	const chatID int64 = 1

	_, _ = db.CreateReadingItem(ctx, chatID, "", "A", "", "", "to-read", "medium", "go,tech")
	_, _ = db.CreateReadingItem(ctx, chatID, "", "B", "", "", "to-read", "medium", "go")
	_, _ = db.CreateReadingItem(ctx, chatID, "", "C", "", "", "done", "medium", "go")  // done — не учитывается
	_, _ = db.CreateReadingItem(ctx, chatID, "", "D", "", "", "to-read", "medium", "") // без тегов

	digest, err := db.ReadingListDigest(ctx, chatID)
	require.NoError(t, err)

	assert.Equal(t, 2, digest["go"])
	assert.Equal(t, 1, digest["tech"])
	assert.Equal(t, 1, digest["(без тегов)"])
	// done item is not counted
	assert.NotEqual(t, 3, digest["go"])
}

func TestParseTagList(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"go,tech", []string{"go", "tech"}},
		{`["go","tech"]`, []string{"go", "tech"}},
		{"", nil},
		{"[]", nil},
		{"null", nil},
		{" go , tech ", []string{"go", "tech"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseTagList(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
