package news_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/news"
)

// ── Mock Fetcher ──────────────────────────────────────────────────────────────

type mockFetcher struct {
	data []byte
	err  error
}

func (f *mockFetcher) Fetch(_ context.Context, _, _ string) ([]byte, error) {
	return f.data, f.err
}

// ── Mock Parser ───────────────────────────────────────────────────────────────

type mockParser struct {
	items []news.Item
	err   error
}

func (p *mockParser) Parse(_ []byte, _ news.FeedType) ([]news.Item, error) {
	return p.items, p.err
}

// ── In-memory Repository ──────────────────────────────────────────────────────

type memRepo struct {
	feeds map[int64]*news.Feed
	items map[int64]*news.Item
	subs  map[[2]int64]*news.Subscription // key: [chatID, feedID]
	rs    map[[2]int64]*news.ReadStatus   // key: [chatID, itemID]
	nid   int64
}

func newMemRepo() *memRepo {
	return &memRepo{
		feeds: make(map[int64]*news.Feed),
		items: make(map[int64]*news.Item),
		subs:  make(map[[2]int64]*news.Subscription),
		rs:    make(map[[2]int64]*news.ReadStatus),
		nid:   1,
	}
}

func (r *memRepo) nextID() int64 { id := r.nid; r.nid++; return id }

func (r *memRepo) CreateFeed(_ context.Context, feed *news.Feed) (int64, error) {
	id := r.nextID()
	cp := *feed
	cp.ID = id
	r.feeds[id] = &cp
	return id, nil
}

func (r *memRepo) GetFeed(_ context.Context, id int64) (*news.Feed, error) {
	f, ok := r.feeds[id]
	if !ok {
		return nil, nil
	}
	cp := *f
	return &cp, nil
}

func (r *memRepo) GetFeedByURL(_ context.Context, url string) (*news.Feed, error) {
	for _, f := range r.feeds {
		if f.URL == url {
			cp := *f
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *memRepo) GetEnabledFeeds(_ context.Context) ([]news.Feed, error) {
	var out []news.Feed
	for _, f := range r.feeds {
		if f.Enabled {
			out = append(out, *f)
		}
	}
	return out, nil
}

func (r *memRepo) UpdateFeed(_ context.Context, feed *news.Feed) error {
	if _, ok := r.feeds[feed.ID]; !ok {
		return errors.New("feed not found")
	}
	cp := *feed
	r.feeds[feed.ID] = &cp
	return nil
}

func (r *memRepo) DeleteFeed(_ context.Context, id int64) error {
	delete(r.feeds, id)
	return nil
}

func (r *memRepo) ListFeeds(_ context.Context, limit, offset int) ([]news.Feed, error) {
	var out []news.Feed
	i := 0
	for _, f := range r.feeds {
		if i < offset {
			i++
			continue
		}
		out = append(out, *f)
		if limit > 0 && len(out) >= limit {
			break
		}
		i++
	}
	return out, nil
}

func (r *memRepo) CreateItem(_ context.Context, item *news.Item) (int64, error) {
	id := r.nextID()
	cp := *item
	cp.ID = id
	r.items[id] = &cp
	return id, nil
}

func (r *memRepo) GetItem(_ context.Context, id int64) (*news.Item, error) {
	it, ok := r.items[id]
	if !ok {
		return nil, nil
	}
	cp := *it
	return &cp, nil
}

func (r *memRepo) ItemExistsByGUID(_ context.Context, feedID int64, guid string) (bool, error) {
	for _, it := range r.items {
		if it.FeedID == feedID && it.GUID == guid {
			return true, nil
		}
	}
	return false, nil
}

func (r *memRepo) GetItemsByFeeds(_ context.Context, feedIDs []int64, filter news.NewsFilter) ([]news.Item, error) {
	set := make(map[int64]bool, len(feedIDs))
	for _, id := range feedIDs {
		set[id] = true
	}
	var out []news.Item
	for _, it := range r.items {
		if set[it.FeedID] {
			out = append(out, *it)
		}
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (r *memRepo) CreateSubscription(_ context.Context, sub *news.Subscription) (int64, error) {
	id := r.nextID()
	cp := *sub
	cp.ID = id
	r.subs[[2]int64{sub.ChatID, sub.FeedID}] = &cp
	return id, nil
}

func (r *memRepo) GetSubscription(_ context.Context, chatID, feedID int64) (*news.Subscription, error) {
	sub, ok := r.subs[[2]int64{chatID, feedID}]
	if !ok {
		return nil, nil
	}
	cp := *sub
	return &cp, nil
}

func (r *memRepo) GetSubscriptions(_ context.Context, chatID int64) ([]news.Subscription, error) {
	var out []news.Subscription
	for k, sub := range r.subs {
		if k[0] == chatID {
			out = append(out, *sub)
		}
	}
	return out, nil
}

func (r *memRepo) UpdateSubscription(_ context.Context, sub *news.Subscription) error {
	key := [2]int64{sub.ChatID, sub.FeedID}
	if _, ok := r.subs[key]; !ok {
		return errors.New("subscription not found")
	}
	cp := *sub
	r.subs[key] = &cp
	return nil
}

func (r *memRepo) DeleteSubscription(_ context.Context, chatID, feedID int64) error {
	delete(r.subs, [2]int64{chatID, feedID})
	return nil
}

func (r *memRepo) CreateReadStatus(_ context.Context, chatID, itemID int64, saved bool) error {
	key := [2]int64{chatID, itemID}
	if _, ok := r.rs[key]; !ok {
		r.rs[key] = &news.ReadStatus{ChatID: chatID, ItemID: itemID}
	}
	r.rs[key].Saved = saved
	return nil
}

func (r *memRepo) GetReadStatus(_ context.Context, chatID, itemID int64) (*news.ReadStatus, error) {
	rs, ok := r.rs[[2]int64{chatID, itemID}]
	if !ok {
		return nil, nil
	}
	cp := *rs
	return &cp, nil
}

func (r *memRepo) GetReadItemIDs(_ context.Context, chatID int64) ([]int64, error) {
	var ids []int64
	for k := range r.rs {
		if k[0] == chatID {
			ids = append(ids, k[1])
		}
	}
	return ids, nil
}

func (r *memRepo) GetReadStatusMap(_ context.Context, chatID int64) (map[int64]*news.ReadStatus, error) {
	result := make(map[int64]*news.ReadStatus)
	for k, rs := range r.rs {
		if k[0] == chatID {
			cp := *rs
			result[k[1]] = &cp
		}
	}
	return result, nil
}

func (r *memRepo) MarkAsSaved(_ context.Context, chatID, itemID int64) error {
	if rs, ok := r.rs[[2]int64{chatID, itemID}]; ok {
		rs.Saved = true
	}
	return nil
}

func (r *memRepo) MarkAsUnsaved(_ context.Context, chatID, itemID int64) error {
	if rs, ok := r.rs[[2]int64{chatID, itemID}]; ok {
		rs.Saved = false
	}
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func managerLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newManager(repo news.Repository, fetcher news.Fetcher, parser news.Parser) *news.NewsManager {
	return news.NewNewsManager(repo, parser, fetcher, "", managerLogger())
}

// ── FetchFeed ─────────────────────────────────────────────────────────────────

func TestFetchFeed_NotFound(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	err := m.FetchFeed(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for nonexistent feed")
	}
}

func TestFetchFeed_FetchError(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://example.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	fetchErr := errors.New("connection refused")
	m := newManager(repo, &mockFetcher{err: fetchErr}, &mockParser{})
	err := m.FetchFeed(context.Background(), feedID)
	if err == nil {
		t.Fatal("expected error when fetching fails")
	}
	// Feed's LastError should be updated.
	f, _ := repo.GetFeed(context.Background(), feedID)
	if f.LastError == "" {
		t.Error("expected LastError to be set on fetch failure")
	}
}

func TestFetchFeed_ParseError(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://example.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	parseErr := errors.New("invalid XML")
	m := newManager(repo, &mockFetcher{data: []byte("data")}, &mockParser{err: parseErr})
	err := m.FetchFeed(context.Background(), feedID)
	if err == nil {
		t.Fatal("expected error when parsing fails")
	}
	f, _ := repo.GetFeed(context.Background(), feedID)
	if f.LastError == "" {
		t.Error("expected LastError to be set on parse failure")
	}
}

func TestFetchFeed_Success_SavesItems(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://example.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	parsedItems := []news.Item{
		{GUID: "g1", Title: "Article 1", PublishedAt: time.Now()},
		{GUID: "g2", Title: "Article 2", PublishedAt: time.Now()},
	}
	m := newManager(repo, &mockFetcher{data: []byte("xml")}, &mockParser{items: parsedItems})
	if err := m.FetchFeed(context.Background(), feedID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	saved := 0
	for _, it := range repo.items {
		if it.FeedID == feedID {
			saved++
		}
	}
	if saved != 2 {
		t.Errorf("expected 2 items saved, got %d", saved)
	}
}

func TestFetchFeed_Success_UpdatesLastFetched(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://example.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	m := newManager(repo, &mockFetcher{data: []byte("xml")}, &mockParser{items: []news.Item{}})
	_ = m.FetchFeed(context.Background(), feedID)

	f, _ := repo.GetFeed(context.Background(), feedID)
	if f.LastFetched == nil {
		t.Error("expected LastFetched to be set after successful fetch")
	}
	if f.LastError != "" {
		t.Errorf("expected LastError to be cleared, got %q", f.LastError)
	}
}

func TestFetchFeed_Deduplication(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://example.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	items := []news.Item{{GUID: "dup-guid", Title: "Article", PublishedAt: time.Now()}}
	m := newManager(repo, &mockFetcher{data: []byte("xml")}, &mockParser{items: items})

	// Fetch twice — second call should not duplicate the item.
	_ = m.FetchFeed(context.Background(), feedID)
	_ = m.FetchFeed(context.Background(), feedID)

	count := 0
	for _, it := range repo.items {
		if it.FeedID == feedID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 item (deduplicated), got %d", count)
	}
}

// ── FetchAllFeeds ─────────────────────────────────────────────────────────────

func TestFetchAllFeeds_OnlyEnabled(t *testing.T) {
	repo := newMemRepo()
	repo.CreateFeed(context.Background(), &news.Feed{URL: "https://a.com", Enabled: true, Type: news.FeedTypeRSS})  //nolint:errcheck
	repo.CreateFeed(context.Background(), &news.Feed{URL: "https://b.com", Enabled: true, Type: news.FeedTypeRSS})  //nolint:errcheck
	repo.CreateFeed(context.Background(), &news.Feed{URL: "https://c.com", Enabled: false, Type: news.FeedTypeRSS}) //nolint:errcheck

	items := []news.Item{{GUID: "g1", Title: "A"}}
	m := newManager(repo, &mockFetcher{data: []byte("xml")}, &mockParser{items: items})
	if err := m.FetchAllFeeds(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 enabled feeds × 1 item each = 2 items.
	if len(repo.items) != 2 {
		t.Errorf("expected 2 items (enabled feeds only), got %d", len(repo.items))
	}
}

// ── GetNews ───────────────────────────────────────────────────────────────────

func TestGetNews_NoSubscriptions(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	items, err := m.GetNews(context.Background(), 100, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for user with no subscriptions, got %d", len(items))
	}
}

func TestGetNews_WithSubscription(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	repo.CreateSubscription(context.Background(), &news.Subscription{ChatID: 200, FeedID: feedID, Enabled: true})              //nolint:errcheck
	repo.CreateItem(context.Background(), &news.Item{FeedID: feedID, GUID: "g1", Title: "Test Item", PublishedAt: time.Now()}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	items, err := m.GetNews(context.Background(), 200, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Item.Title != "Test Item" {
		t.Errorf("expected 'Test Item', got %q", items[0].Item.Title)
	}
}

func TestGetNews_ReadAndSavedStatus(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	repo.CreateSubscription(context.Background(), &news.Subscription{ChatID: 300, FeedID: feedID, Enabled: true}) //nolint:errcheck
	itemID, _ := repo.CreateItem(context.Background(), &news.Item{FeedID: feedID, GUID: "g1", PublishedAt: time.Now()})
	repo.CreateReadStatus(context.Background(), 300, itemID, true) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	items, err := m.GetNews(context.Background(), 300, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !items[0].Read {
		t.Error("expected item to be marked as read")
	}
	if !items[0].Saved {
		t.Error("expected item to be marked as saved")
	}
}

func TestGetNews_UnreadItem_HasLowScore(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	repo.CreateSubscription(context.Background(), &news.Subscription{ChatID: 400, FeedID: feedID, Enabled: true}) //nolint:errcheck
	// Recent item → high score.
	repo.CreateItem(context.Background(), &news.Item{FeedID: feedID, GUID: "g1", PublishedAt: time.Now()}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	items, err := m.GetNews(context.Background(), 400, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Score <= 0 {
		t.Errorf("expected positive score, got %.2f", items[0].Score)
	}
}

func TestGetNews_DisabledFeed_Excluded(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: false})
	// Disabled feed → items should not appear.
	repo.CreateItem(context.Background(), &news.Item{FeedID: feedID, GUID: "g1", PublishedAt: time.Now()}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	items, err := m.GetNews(context.Background(), 500, news.NewsFilter{Limit: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for disabled feed, got %d", len(items))
	}
}

// ── Subscribe ─────────────────────────────────────────────────────────────────

func TestSubscribe_NewSubscription(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.Subscribe(context.Background(), 600, feedID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub, _ := repo.GetSubscription(context.Background(), 600, feedID)
	if sub == nil {
		t.Fatal("expected subscription to be created")
	}
	if !sub.Enabled {
		t.Error("expected subscription to be enabled")
	}
}

func TestSubscribe_FeedNotFound(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.Subscribe(context.Background(), 600, 999); err == nil {
		t.Fatal("expected error for nonexistent feed")
	}
}

func TestSubscribe_ReEnablesDisabledSubscription(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	repo.CreateSubscription(context.Background(), &news.Subscription{ChatID: 700, FeedID: feedID, Enabled: false}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.Subscribe(context.Background(), 700, feedID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub, _ := repo.GetSubscription(context.Background(), 700, feedID)
	if !sub.Enabled {
		t.Error("expected subscription to be re-enabled")
	}
}

// ── Unsubscribe ───────────────────────────────────────────────────────────────

func TestUnsubscribe(t *testing.T) {
	repo := newMemRepo()
	feedID, _ := repo.CreateFeed(context.Background(), &news.Feed{URL: "https://x.com", Enabled: true})
	repo.CreateSubscription(context.Background(), &news.Subscription{ChatID: 800, FeedID: feedID, Enabled: true}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.Unsubscribe(context.Background(), 800, feedID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub, _ := repo.GetSubscription(context.Background(), 800, feedID)
	if sub != nil {
		t.Error("expected subscription to be deleted")
	}
}

// ── MarkAsRead / SaveItem / UnsaveItem ────────────────────────────────────────

func TestMarkAsRead(t *testing.T) {
	repo := newMemRepo()
	itemID, _ := repo.CreateItem(context.Background(), &news.Item{FeedID: 1, GUID: "g1", PublishedAt: time.Now()})

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.MarkAsRead(context.Background(), 900, itemID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rs, _ := repo.GetReadStatus(context.Background(), 900, itemID)
	if rs == nil {
		t.Fatal("expected read status to be created")
	}
}

func TestSaveItem(t *testing.T) {
	repo := newMemRepo()
	itemID, _ := repo.CreateItem(context.Background(), &news.Item{FeedID: 1, GUID: "g1", PublishedAt: time.Now()})

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.SaveItem(context.Background(), 1000, itemID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rs, _ := repo.GetReadStatus(context.Background(), 1000, itemID)
	if rs == nil {
		t.Fatal("expected read status to be created")
	}
	if !rs.Saved {
		t.Error("expected item to be saved")
	}
}

func TestUnsaveItem(t *testing.T) {
	repo := newMemRepo()
	itemID, _ := repo.CreateItem(context.Background(), &news.Item{FeedID: 1, GUID: "g1", PublishedAt: time.Now()})
	repo.CreateReadStatus(context.Background(), 1100, itemID, true) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	if err := m.UnsaveItem(context.Background(), 1100, itemID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rs, _ := repo.GetReadStatus(context.Background(), 1100, itemID)
	if rs.Saved {
		t.Error("expected item to be unsaved after UnsaveItem")
	}
}

// ── ListFeeds ─────────────────────────────────────────────────────────────────

func TestListFeeds(t *testing.T) {
	repo := newMemRepo()
	repo.CreateFeed(context.Background(), &news.Feed{URL: "https://a.com", Title: "Feed A", Enabled: true}) //nolint:errcheck
	repo.CreateFeed(context.Background(), &news.Feed{URL: "https://b.com", Title: "Feed B", Enabled: true}) //nolint:errcheck

	m := newManager(repo, &mockFetcher{}, &mockParser{})
	feeds, err := m.ListFeeds(context.Background(), 100, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(feeds) != 2 {
		t.Errorf("expected 2 feeds, got %d", len(feeds))
	}
}

// ── AddFeed ───────────────────────────────────────────────────────────────────

func TestAddFeed_KnownType_RSS(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	feed, err := m.AddFeed(context.Background(), "https://example.com/feed.rss", "rss")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed.ID == 0 {
		t.Error("expected non-zero feed ID")
	}
	if feed.Type != news.FeedTypeRSS {
		t.Errorf("expected RSS type, got %q", feed.Type)
	}
}

func TestAddFeed_KnownType_Atom(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	feed, err := m.AddFeed(context.Background(), "https://example.com/atom", "atom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed.Type != news.FeedTypeAtom {
		t.Errorf("expected Atom type, got %q", feed.Type)
	}
}

func TestAddFeed_AlreadyExists_ReturnsExisting(t *testing.T) {
	repo := newMemRepo()
	existingID, _ := repo.CreateFeed(context.Background(), &news.Feed{
		URL: "https://existing.com/feed", Enabled: true, Type: news.FeedTypeRSS,
	})
	m := newManager(repo, &mockFetcher{}, &mockParser{})
	feed, err := m.AddFeed(context.Background(), "https://existing.com/feed", "rss")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed.ID != existingID {
		t.Errorf("expected existing feed ID %d, got %d", existingID, feed.ID)
	}
}

func TestAddFeed_AutoDetect_Success(t *testing.T) {
	repo := newMemRepo()
	// Parser returns items without error → auto-detection succeeds.
	items := []news.Item{{GUID: "g1", Title: "Article"}}
	m := newManager(repo, &mockFetcher{data: []byte("xml")}, &mockParser{items: items})
	feed, err := m.AddFeed(context.Background(), "https://new.com/feed", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if feed == nil {
		t.Fatal("expected feed to be created")
	}
	if feed.ID == 0 {
		t.Error("expected non-zero feed ID")
	}
}

func TestAddFeed_AutoDetect_FetchError(t *testing.T) {
	repo := newMemRepo()
	m := newManager(repo, &mockFetcher{err: errors.New("no network")}, &mockParser{})
	_, err := m.AddFeed(context.Background(), "https://new.com/feed", "")
	if err == nil {
		t.Fatal("expected error when fetch fails during auto-detect")
	}
}
