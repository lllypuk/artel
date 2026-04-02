package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/news"
	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func openNewsRepo(t *testing.T) (storage.NewsRepository, *storage.DB) {
	t.Helper()
	db := openTestDB(t)
	return storage.NewNewsRepository(db), db
}

func createFeed(t *testing.T, repo storage.NewsRepository, url, title string, enabled bool) int64 {
	t.Helper()
	id, err := repo.CreateFeed(context.Background(), &news.Feed{
		URL:           url,
		Title:         title,
		Type:          news.FeedTypeRSS,
		Enabled:       enabled,
		FetchInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("create feed %q: %v", url, err)
	}
	return id
}

func createItem(t *testing.T, repo storage.NewsRepository, feedID int64, guid, title string) int64 {
	t.Helper()
	id, err := repo.CreateItem(context.Background(), &news.Item{
		FeedID:      feedID,
		GUID:        guid,
		Title:       title,
		Link:        "https://example.com/" + guid,
		PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create item %q: %v", guid, err)
	}
	return id
}

// ── Feed CRUD ─────────────────────────────────────────────────────────────────

func TestNews_CreateAndGetFeed(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	id := createFeed(t, repo, "https://example.com/feed", "Test Feed", true)
	f, err := repo.GetFeed(ctx, id)
	if err != nil {
		t.Fatalf("get feed: %v", err)
	}
	if f == nil {
		t.Fatal("expected feed, got nil")
	}
	if f.ID != id {
		t.Errorf("expected id %d, got %d", id, f.ID)
	}
	if f.Title != "Test Feed" {
		t.Errorf("expected title 'Test Feed', got %q", f.Title)
	}
	if f.URL != "https://example.com/feed" {
		t.Errorf("unexpected URL: %q", f.URL)
	}
	if !f.Enabled {
		t.Error("expected feed to be enabled")
	}
	if f.FetchInterval != time.Hour {
		t.Errorf("expected fetch interval 1h, got %v", f.FetchInterval)
	}
}

func TestNews_GetFeed_NotFound(t *testing.T) {
	repo, _ := openNewsRepo(t)
	f, err := repo.GetFeed(context.Background(), 9999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil for missing feed, got %+v", f)
	}
}

func TestNews_GetFeedByURL(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	id := createFeed(t, repo, "https://feeds.example.com/rss", "My Feed", true)
	f, err := repo.GetFeedByURL(ctx, "https://feeds.example.com/rss")
	if err != nil {
		t.Fatalf("get feed by URL: %v", err)
	}
	if f == nil {
		t.Fatal("expected feed, got nil")
	}
	if f.ID != id {
		t.Errorf("expected id %d, got %d", id, f.ID)
	}
}

func TestNews_GetFeedByURL_NotFound(t *testing.T) {
	repo, _ := openNewsRepo(t)
	f, err := repo.GetFeedByURL(context.Background(), "https://nope.example.com/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f != nil {
		t.Errorf("expected nil, got %+v", f)
	}
}

func TestNews_GetEnabledFeeds(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	createFeed(t, repo, "https://a.com/feed", "Enabled A", true)
	createFeed(t, repo, "https://b.com/feed", "Enabled B", true)
	createFeed(t, repo, "https://c.com/feed", "Disabled C", false)

	feeds, err := repo.GetEnabledFeeds(ctx)
	if err != nil {
		t.Fatalf("get enabled feeds: %v", err)
	}
	if len(feeds) != 2 {
		t.Errorf("expected 2 enabled feeds, got %d", len(feeds))
	}
	for _, f := range feeds {
		if !f.Enabled {
			t.Errorf("GetEnabledFeeds returned a disabled feed: %+v", f)
		}
	}
}

func TestNews_UpdateFeed(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	id := createFeed(t, repo, "https://update.example.com/", "Old Title", true)
	f, _ := repo.GetFeed(ctx, id)
	f.Title = "New Title"
	f.LastError = "some error"
	if err := repo.UpdateFeed(ctx, f); err != nil {
		t.Fatalf("update feed: %v", err)
	}

	updated, _ := repo.GetFeed(ctx, id)
	if updated.Title != "New Title" {
		t.Errorf("expected updated title, got %q", updated.Title)
	}
	if updated.LastError != "some error" {
		t.Errorf("expected LastError to be set, got %q", updated.LastError)
	}
}

func TestNews_UpdateFeed_LastFetched(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	id := createFeed(t, repo, "https://lf.example.com/", "T", true)
	f, _ := repo.GetFeed(ctx, id)
	now := time.Now().Truncate(time.Second)
	f.LastFetched = &now
	if err := repo.UpdateFeed(ctx, f); err != nil {
		t.Fatalf("update feed: %v", err)
	}
	updated, _ := repo.GetFeed(ctx, id)
	if updated.LastFetched == nil {
		t.Fatal("expected LastFetched to be set")
	}
}

func TestNews_DeleteFeed(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	id := createFeed(t, repo, "https://del.example.com/", "Delete me", true)
	if err := repo.DeleteFeed(ctx, id); err != nil {
		t.Fatalf("delete feed: %v", err)
	}
	f, _ := repo.GetFeed(ctx, id)
	if f != nil {
		t.Errorf("expected nil after delete, got %+v", f)
	}
}

func TestNews_ListFeeds_Pagination(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	for i := range 5 {
		createFeed(t, repo, "https://list-"+string(rune('a'+i))+".example.com/", "Feed", true)
	}

	first, err := repo.ListFeeds(ctx, 3, 0)
	if err != nil {
		t.Fatalf("list feeds: %v", err)
	}
	if len(first) != 3 {
		t.Errorf("expected 3 feeds, got %d", len(first))
	}

	rest, err := repo.ListFeeds(ctx, 10, 3)
	if err != nil {
		t.Fatalf("list feeds offset: %v", err)
	}
	if len(rest) != 2 {
		t.Errorf("expected 2 remaining feeds, got %d", len(rest))
	}
}

func TestNews_ListFeeds_Empty(t *testing.T) {
	repo, _ := openNewsRepo(t)
	feeds, err := repo.ListFeeds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(feeds) != 0 {
		t.Errorf("expected 0 feeds, got %d", len(feeds))
	}
}

// ── Item CRUD ─────────────────────────────────────────────────────────────────

func TestNews_CreateAndGetItem(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://items.example.com/", "F", true)
	pub := time.Now().Truncate(time.Second)
	id, err := repo.CreateItem(ctx, &news.Item{
		FeedID:      feedID,
		GUID:        "item-guid-1",
		Title:       "My Article",
		Link:        "https://example.com/1",
		Description: "A description",
		Content:     "Full content",
		Author:      "Alice",
		Category:    "Tech",
		PublishedAt: pub,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	item, err := repo.GetItem(ctx, id)
	if err != nil {
		t.Fatalf("get item: %v", err)
	}
	if item == nil {
		t.Fatal("expected item, got nil")
	}
	if item.FeedID != feedID {
		t.Errorf("expected feed_id %d, got %d", feedID, item.FeedID)
	}
	if item.GUID != "item-guid-1" {
		t.Errorf("unexpected GUID: %q", item.GUID)
	}
	if item.Title != "My Article" {
		t.Errorf("unexpected title: %q", item.Title)
	}
	if item.Author != "Alice" {
		t.Errorf("unexpected author: %q", item.Author)
	}
	if item.Category != "Tech" {
		t.Errorf("unexpected category: %q", item.Category)
	}
}

func TestNews_GetItem_NotFound(t *testing.T) {
	repo, _ := openNewsRepo(t)
	item, err := repo.GetItem(context.Background(), 9999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if item != nil {
		t.Errorf("expected nil, got %+v", item)
	}
}

func TestNews_GetItemByID_SameAsGetItem(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://byid.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "guid-byid", "ByID Article")

	a, _ := repo.GetItem(ctx, itemID)
	b, _ := repo.GetItemByID(ctx, itemID)
	if a == nil || b == nil {
		t.Fatal("expected non-nil items")
	}
	if a.Title != b.Title {
		t.Errorf("GetItem and GetItemByID returned different titles: %q vs %q", a.Title, b.Title)
	}
}

func TestNews_ItemExistsByGUID_True(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://guid.example.com/", "F", true)
	createItem(t, repo, feedID, "unique-guid-123", "Article")

	exists, err := repo.ItemExistsByGUID(ctx, feedID, "unique-guid-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected item to exist")
	}
}

func TestNews_ItemExistsByGUID_False(t *testing.T) {
	repo, _ := openNewsRepo(t)
	feedID := createFeed(t, repo, "https://guid2.example.com/", "F", true)

	exists, err := repo.ItemExistsByGUID(context.Background(), feedID, "no-such-guid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected item to not exist")
	}
}

func TestNews_ItemExistsByGUID_DifferentFeed(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feed1 := createFeed(t, repo, "https://f1.example.com/", "F1", true)
	feed2 := createFeed(t, repo, "https://f2.example.com/", "F2", true)
	createItem(t, repo, feed1, "shared-guid", "Article in feed1")

	// Same GUID in feed2 should not exist.
	exists, err := repo.ItemExistsByGUID(ctx, feed2, "shared-guid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected GUID to not exist in feed2")
	}
}

func TestNews_CreateItem_DuplicateGUID_IsIgnored(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://dup.example.com/", "F", true)
	id1, err := repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "dup-guid", Title: "First", Link: "https://x.com/1", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create first item: %v", err)
	}
	// Second create with same GUID should not error (INSERT OR IGNORE).
	id2, err := repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "dup-guid", Title: "Second", Link: "https://x.com/2", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create duplicate item: %v", err)
	}
	// SQLite OR IGNORE returns 0 lastInsertId when row was ignored.
	_ = id1
	_ = id2
}

func TestNews_GetItemsByFeeds_Basic(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	f1 := createFeed(t, repo, "https://filt1.example.com/", "F1", true)
	f2 := createFeed(t, repo, "https://filt2.example.com/", "F2", true)
	createItem(t, repo, f1, "g1", "Article 1")
	createItem(t, repo, f1, "g2", "Article 2")
	createItem(t, repo, f2, "g3", "Article 3")

	items, err := repo.GetItemsByFeeds(ctx, []int64{f1}, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 items from feed1, got %d", len(items))
	}

	all, err := repo.GetItemsByFeeds(ctx, []int64{f1, f2}, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("expected 3 items from both feeds, got %d", len(all))
	}
}

func TestNews_GetItemsByFeeds_EmptyFeedIDs(t *testing.T) {
	repo, _ := openNewsRepo(t)
	items, err := repo.GetItemsByFeeds(context.Background(), []int64{}, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty feedIDs, got %d", len(items))
	}
}

func TestNews_GetItemsByFeeds_CategoryFilter(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://cat.example.com/", "F", true)
	_, err := repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "c1", Title: "Tech Article", Category: "tech",
		Link: "https://x.com/c1", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	_, err = repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "c2", Title: "Science Article", Category: "science",
		Link: "https://x.com/c2", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	items, err := repo.GetItemsByFeeds(ctx, []int64{feedID}, news.NewsFilter{Category: "tech", Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 tech item, got %d", len(items))
	}
	if items[0].Category != "tech" {
		t.Errorf("unexpected category: %q", items[0].Category)
	}
}

func TestNews_GetItemsByFeeds_Limit(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://limit.example.com/", "F", true)
	for i := range 5 {
		createItem(t, repo, feedID, "g-lim-"+string(rune('a'+i)), "Item")
	}

	items, err := repo.GetItemsByFeeds(ctx, []int64{feedID}, news.NewsFilter{Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items (limit), got %d", len(items))
	}
}

func TestNews_GetItemsByFeeds_UnreadFilter(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://unread.example.com/", "F", true)
	item1 := createItem(t, repo, feedID, "unread-g1", "Read Article")
	createItem(t, repo, feedID, "unread-g2", "Unread Article")

	// Mark item1 as read.
	if err := repo.CreateReadStatus(ctx, 42, item1, false); err != nil {
		t.Fatalf("create read status: %v", err)
	}

	items, err := repo.GetItemsByFeeds(ctx, []int64{feedID}, news.NewsFilter{
		ChatID: 42, UnreadOnly: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 unread item, got %d", len(items))
	}
	if items[0].GUID != "unread-g2" {
		t.Errorf("expected unread item, got GUID %q", items[0].GUID)
	}
}

func TestNews_GetItemsByFeeds_SavedFilter(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://saved.example.com/", "F", true)
	item1 := createItem(t, repo, feedID, "saved-g1", "Saved Article")
	createItem(t, repo, feedID, "saved-g2", "Not Saved Article")

	if err := repo.CreateReadStatus(ctx, 55, item1, true); err != nil { // saved=true
		t.Fatalf("create read status: %v", err)
	}

	items, err := repo.GetItemsByFeeds(ctx, []int64{feedID}, news.NewsFilter{
		ChatID: 55, SavedOnly: true, Limit: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 saved item, got %d", len(items))
	}
	if items[0].GUID != "saved-g1" {
		t.Errorf("expected saved item, got GUID %q", items[0].GUID)
	}
}

func TestNews_GetItemsByFeeds_SearchQuery_FTS(t *testing.T) {
	repo, db := openNewsRepo(t)
	if !db.HasFTS() {
		t.Skip("FTS5 not available")
	}
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://fts.example.com/", "F", true)
	_, err := repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "fts1", Title: "Golang concurrency patterns",
		Link: "https://x.com/fts1", Content: "goroutines channels select", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	_, err = repo.CreateItem(ctx, &news.Item{
		FeedID: feedID, GUID: "fts2", Title: "Python basics",
		Link: "https://x.com/fts2", Content: "lists dictionaries", PublishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}

	items, err := repo.GetItemsByFeeds(ctx, []int64{feedID}, news.NewsFilter{
		SearchQuery: "goroutines", Limit: 10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item matching 'goroutines', got %d", len(items))
	}
	if items[0].GUID != "fts1" {
		t.Errorf("unexpected item: %q", items[0].GUID)
	}
}

func TestNews_DeleteOldItems(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://old.example.com/", "F", true)
	for i := range 3 {
		_, err := repo.CreateItem(ctx, &news.Item{
			FeedID: feedID, GUID: "del-g" + string(rune('0'+i)), Title: "Item",
			Link:        "https://x.com/" + string(rune('0'+i)),
			PublishedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
	}

	// Cutoff in the future → all 3 items are older than cutoff → deleted.
	cutoff := time.Now().Add(time.Hour)
	deleted, err := repo.DeleteOldItems(ctx, cutoff)
	if err != nil {
		t.Fatalf("delete old items: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deleted items, got %d", deleted)
	}
}

// ── Subscription CRUD ─────────────────────────────────────────────────────────

func TestNews_CreateAndGetSubscription(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://sub.example.com/", "F", true)
	id, err := repo.CreateSubscription(ctx, &news.Subscription{
		ChatID:         999,
		FeedID:         feedID,
		Enabled:        true,
		Notify:         true,
		FilterKeywords: "go,rust",
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero subscription id")
	}

	sub, err := repo.GetSubscription(ctx, 999, feedID)
	if err != nil {
		t.Fatalf("get subscription: %v", err)
	}
	if sub == nil {
		t.Fatal("expected subscription, got nil")
	}
	if sub.ChatID != 999 || sub.FeedID != feedID {
		t.Errorf("unexpected sub: chatID=%d feedID=%d", sub.ChatID, sub.FeedID)
	}
	if !sub.Enabled || !sub.Notify {
		t.Errorf("unexpected enabled/notify: %v/%v", sub.Enabled, sub.Notify)
	}
	if sub.FilterKeywords != "go,rust" {
		t.Errorf("unexpected filter_keywords: %q", sub.FilterKeywords)
	}
}

func TestNews_GetSubscription_NotFound(t *testing.T) {
	repo, _ := openNewsRepo(t)
	sub, err := repo.GetSubscription(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub != nil {
		t.Errorf("expected nil, got %+v", sub)
	}
}

func TestNews_GetSubscriptions(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	f1 := createFeed(t, repo, "https://gs1.example.com/", "F1", true)
	f2 := createFeed(t, repo, "https://gs2.example.com/", "F2", true)
	repo.CreateSubscription(ctx, &news.Subscription{ChatID: 111, FeedID: f1, Enabled: true})        //nolint:errcheck
	repo.CreateSubscription(ctx, &news.Subscription{ChatID: 111, FeedID: f2, Enabled: true})        //nolint:errcheck
	_, _ = repo.CreateSubscription(ctx, &news.Subscription{ChatID: 222, FeedID: f1, Enabled: true}) // different user

	subs, err := repo.GetSubscriptions(ctx, 111)
	if err != nil {
		t.Fatalf("get subscriptions: %v", err)
	}
	if len(subs) != 2 {
		t.Errorf("expected 2 subscriptions for chat 111, got %d", len(subs))
	}
}

func TestNews_UpdateSubscription(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://updsub.example.com/", "F", true)
	repo.CreateSubscription(ctx, &news.Subscription{ChatID: 333, FeedID: feedID, Enabled: true}) //nolint:errcheck

	sub, _ := repo.GetSubscription(ctx, 333, feedID)
	sub.Enabled = false
	sub.FilterKeywords = "linux"
	if err := repo.UpdateSubscription(ctx, sub); err != nil {
		t.Fatalf("update subscription: %v", err)
	}

	updated, _ := repo.GetSubscription(ctx, 333, feedID)
	if updated.Enabled {
		t.Error("expected subscription to be disabled after update")
	}
	if updated.FilterKeywords != "linux" {
		t.Errorf("expected filter_keywords 'linux', got %q", updated.FilterKeywords)
	}
}

func TestNews_DeleteSubscription(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://delsub.example.com/", "F", true)
	repo.CreateSubscription(ctx, &news.Subscription{ChatID: 444, FeedID: feedID, Enabled: true}) //nolint:errcheck

	if err := repo.DeleteSubscription(ctx, 444, feedID); err != nil {
		t.Fatalf("delete subscription: %v", err)
	}
	sub, _ := repo.GetSubscription(ctx, 444, feedID)
	if sub != nil {
		t.Errorf("expected nil after delete, got %+v", sub)
	}
}

// ── Read Status ───────────────────────────────────────────────────────────────

func TestNews_CreateAndGetReadStatus(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://rs.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "rs-g1", "Article")

	if err := repo.CreateReadStatus(ctx, 500, itemID, false); err != nil {
		t.Fatalf("create read status: %v", err)
	}

	rs, err := repo.GetReadStatus(ctx, 500, itemID)
	if err != nil {
		t.Fatalf("get read status: %v", err)
	}
	if rs == nil {
		t.Fatal("expected read status, got nil")
	}
	if rs.ChatID != 500 || rs.ItemID != itemID {
		t.Errorf("unexpected read status: chatID=%d itemID=%d", rs.ChatID, rs.ItemID)
	}
	if rs.Saved {
		t.Error("expected saved=false")
	}
}

func TestNews_GetReadStatus_NotFound(t *testing.T) {
	repo, _ := openNewsRepo(t)
	rs, err := repo.GetReadStatus(context.Background(), 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rs != nil {
		t.Errorf("expected nil, got %+v", rs)
	}
}

func TestNews_GetReadItemIDs(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://rids.example.com/", "F", true)
	id1 := createItem(t, repo, feedID, "rids-g1", "A1")
	id2 := createItem(t, repo, feedID, "rids-g2", "A2")
	id3 := createItem(t, repo, feedID, "rids-g3", "A3")

	repo.CreateReadStatus(ctx, 600, id1, false) //nolint:errcheck
	repo.CreateReadStatus(ctx, 600, id2, false) //nolint:errcheck
	// id3 not read.

	ids, err := repo.GetReadItemIDs(ctx, 600)
	if err != nil {
		t.Fatalf("get read item IDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 read item IDs, got %d", len(ids))
	}
	idSet := map[int64]bool{id1: true, id2: true}
	for _, id := range ids {
		if !idSet[id] {
			t.Errorf("unexpected item ID in read list: %d", id)
		}
	}
	_ = id3
}

func TestNews_GetReadStatusMap(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://rsmap.example.com/", "F", true)
	id1 := createItem(t, repo, feedID, "rsmap-g1", "A1")
	id2 := createItem(t, repo, feedID, "rsmap-g2", "A2")

	repo.CreateReadStatus(ctx, 700, id1, false) //nolint:errcheck
	repo.CreateReadStatus(ctx, 700, id2, true)  //nolint:errcheck

	m, err := repo.GetReadStatusMap(ctx, 700)
	if err != nil {
		t.Fatalf("get read status map: %v", err)
	}
	if len(m) != 2 {
		t.Errorf("expected 2 entries in map, got %d", len(m))
	}
	if rs, ok := m[id2]; !ok || !rs.Saved {
		t.Errorf("expected id2 to be saved in map")
	}
}

func TestNews_MarkAsSaved(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://save.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "save-g1", "Article")

	repo.CreateReadStatus(ctx, 800, itemID, false) //nolint:errcheck
	if err := repo.MarkAsSaved(ctx, 800, itemID); err != nil {
		t.Fatalf("mark as saved: %v", err)
	}

	rs, _ := repo.GetReadStatus(ctx, 800, itemID)
	if !rs.Saved {
		t.Error("expected saved=true after MarkAsSaved")
	}
}

func TestNews_MarkAsUnsaved(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://unsave.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "unsave-g1", "Article")

	repo.CreateReadStatus(ctx, 900, itemID, true) //nolint:errcheck
	if err := repo.MarkAsUnsaved(ctx, 900, itemID); err != nil {
		t.Fatalf("mark as unsaved: %v", err)
	}

	rs, _ := repo.GetReadStatus(ctx, 900, itemID)
	if rs.Saved {
		t.Error("expected saved=false after MarkAsUnsaved")
	}
}

func TestNews_DeleteReadStatus(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://delrs.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "delrs-g1", "Article")
	repo.CreateReadStatus(ctx, 1000, itemID, false) //nolint:errcheck

	if err := repo.DeleteReadStatus(ctx, 1000, itemID); err != nil {
		t.Fatalf("delete read status: %v", err)
	}
	rs, _ := repo.GetReadStatus(ctx, 1000, itemID)
	if rs != nil {
		t.Errorf("expected nil after delete, got %+v", rs)
	}
}

func TestNews_DeleteUnreadItemsOlderThan(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://cleanup.example.com/", "F", true)
	// Create 2 unread items and 1 item marked as read.
	id1 := createItem(t, repo, feedID, "cleanup-g1", "Unread 1")
	createItem(t, repo, feedID, "cleanup-g2", "Unread 2")
	id3 := createItem(t, repo, feedID, "cleanup-g3", "Read")

	// Mark id3 as read.
	if err := repo.CreateReadStatus(ctx, 77, id3, false); err != nil {
		t.Fatalf("create read status: %v", err)
	}

	// Cutoff in the future → deletes items created before now+1h that are NOT in read_status.
	cutoff := time.Now().Add(time.Hour)
	deleted, err := repo.DeleteUnreadItemsOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("delete unread items: %v", err)
	}
	// Only the 2 unread items should be deleted; the read one is protected.
	if deleted != 2 {
		t.Errorf("expected 2 deleted unread items, got %d", deleted)
	}
	// The read item should still exist.
	item, _ := repo.GetItem(ctx, id3)
	if item == nil {
		t.Error("expected read item to survive cleanup")
	}
	_ = id1
}

// ── Feed deletion cascades to items and subscriptions ─────────────────────────

func TestNews_DeleteFeed_CascadesToItems(t *testing.T) {
	repo, _ := openNewsRepo(t)
	ctx := context.Background()

	feedID := createFeed(t, repo, "https://cascade.example.com/", "F", true)
	itemID := createItem(t, repo, feedID, "cascade-g1", "Article")

	if err := repo.DeleteFeed(ctx, feedID); err != nil {
		t.Fatalf("delete feed: %v", err)
	}
	item, _ := repo.GetItem(ctx, itemID)
	if item != nil {
		t.Errorf("expected item to be cascade-deleted, got %+v", item)
	}
}
