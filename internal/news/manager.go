package news

import (
	"context"
	"fmt"
	"time"

	"log/slog"
)

// Repository defines the interface for news storage operations.
type Repository interface {
	// Feeds
	CreateFeed(ctx context.Context, feed *Feed) (int64, error)
	GetFeed(ctx context.Context, id int64) (*Feed, error)
	GetFeedByURL(ctx context.Context, url string) (*Feed, error)
	GetEnabledFeeds(ctx context.Context) ([]Feed, error)
	UpdateFeed(ctx context.Context, feed *Feed) error
	DeleteFeed(ctx context.Context, id int64) error
	ListFeeds(ctx context.Context, limit, offset int) ([]Feed, error)

	// Items
	CreateItem(ctx context.Context, item *Item) (int64, error)
	GetItem(ctx context.Context, id int64) (*Item, error)
	ItemExistsByGUID(ctx context.Context, feedID int64, guid string) (bool, error)
	GetItemsByFeeds(ctx context.Context, feedIDs []int64, filter NewsFilter) ([]Item, error)

	// Subscriptions
	CreateSubscription(ctx context.Context, sub *Subscription) (int64, error)
	GetSubscription(ctx context.Context, chatID, feedID int64) (*Subscription, error)
	GetSubscriptions(ctx context.Context, chatID int64) ([]Subscription, error)
	UpdateSubscription(ctx context.Context, sub *Subscription) error
	DeleteSubscription(ctx context.Context, chatID, feedID int64) error

	// Read Status
	CreateReadStatus(ctx context.Context, chatID, itemID int64, saved bool) error
	GetReadStatus(ctx context.Context, chatID, itemID int64) (*ReadStatus, error)
	GetReadItemIDs(ctx context.Context, chatID int64) ([]int64, error)
	GetReadStatusMap(ctx context.Context, chatID int64) (map[int64]*ReadStatus, error)
	MarkAsSaved(ctx context.Context, chatID, itemID int64) error
	MarkAsUnsaved(ctx context.Context, chatID, itemID int64) error
}

// NewsManager coordinates news operations.
type NewsManager struct {
	repo         Repository
	parser       Parser
	fetcher      Fetcher
	defaultProxy string // used when a feed has no ProxyURL of its own
	logger       *slog.Logger
}

// NewNewsManager creates a new NewsManager.
func NewNewsManager(repo Repository, parser Parser, fetcher Fetcher, defaultProxy string, logger *slog.Logger) *NewsManager {
	return &NewsManager{
		repo:         repo,
		parser:       parser,
		fetcher:      fetcher,
		defaultProxy: defaultProxy,
		logger:       logger,
	}
}

// FetchFeed updates a specific feed.
func (m *NewsManager) FetchFeed(ctx context.Context, feedID int64) error {
	m.logger.Info("fetching feed", "feed_id", feedID)

	feed, err := m.repo.GetFeed(ctx, feedID)
	if err != nil {
		m.logger.Error("get feed", "feed_id", feedID, "error", err)
		return err
	}
	if feed == nil {
		return NewFeedNotFoundError(feedID)
	}

	m.logger.Debug("fetching from URL", "url", feed.URL)

	proxyURL := feed.ProxyURL
	if proxyURL == "" {
		proxyURL = m.defaultProxy
	}
	data, err := m.fetcher.Fetch(ctx, feed.URL, proxyURL)
	if err != nil {
		feed.LastError = err.Error()
		if updateErr := m.repo.UpdateFeed(ctx, feed); updateErr != nil {
			m.logger.Error("update feed after fetch error", "feed_id", feedID, "error", updateErr)
		}

		m.logger.Error("fetch failed", "feed_id", feedID, "url", feed.URL, "error", err)
		return NewFetchError(err, feed.URL)
	}

	m.logger.Debug("parsing feed", "size", len(data), "type", feed.Type)

	items, err := m.parser.Parse(data, feed.Type)
	if err != nil {
		feed.LastError = err.Error()
		if updateErr := m.repo.UpdateFeed(ctx, feed); updateErr != nil {
			m.logger.Error("update feed after parse error", "feed_id", feedID, "error", updateErr)
		}

		m.logger.Error("parse failed", "feed_id", feedID, "type", feed.Type, "error", err)
		return NewParseError(err, string(feed.Type))
	}

	saved := 0
	for i := range items {
		items[i].FeedID = feed.ID
		exists, err := m.repo.ItemExistsByGUID(ctx, feed.ID, items[i].GUID)
		if err != nil {
			m.logger.Error("check item exists", "item_guid", items[i].GUID, "error", err)
			continue
		}
		if !exists {
			if _, err := m.repo.CreateItem(ctx, &items[i]); err != nil {
				m.logger.Error("create item", "item_title", items[i].Title, "error", err)
			} else {
				saved++
				m.logger.Debug("item saved", "title", items[i].Title, "guid", items[i].GUID)
			}
		}
	}

	now := time.Now()
	feed.LastFetched = &now
	feed.LastError = ""
	if err := m.repo.UpdateFeed(ctx, feed); err != nil {
		m.logger.Error("update feed", "feed_id", feedID, "error", err)
		return err
	}

	m.logger.Info("feed fetched successfully",
		"feed_id", feedID,
		"new_items", saved,
		"total_items", len(items),
	)
	return nil
}

// FetchAllFeeds updates all enabled feeds.
func (m *NewsManager) FetchAllFeeds(ctx context.Context) error {
	feeds, err := m.repo.GetEnabledFeeds(ctx)
	if err != nil {
		return fmt.Errorf("get enabled feeds: %w", err)
	}

	m.logger.Info("fetching all feeds", "count", len(feeds))

	var lastErr error
	for _, feed := range feeds {
		if err := m.FetchFeed(ctx, feed.ID); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// GetNews returns news with filtering from all enabled feeds.
func (m *NewsManager) GetNews(ctx context.Context, chatID int64, filter NewsFilter) ([]ItemWithScore, error) {
	// Get all enabled feeds
	feeds, err := m.repo.GetEnabledFeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("get enabled feeds: %w", err)
	}

	feedIDs := make([]int64, 0, len(feeds))
	for _, feed := range feeds {
		feedIDs = append(feedIDs, feed.ID)
	}

	// Set chatID for DB-level filtering of unread/saved
	filter.ChatID = chatID

	// Get news from all enabled feeds (DB handles unread/saved filtering)
	items, err := m.repo.GetItemsByFeeds(ctx, feedIDs, filter)
	if err != nil {
		return nil, fmt.Errorf("get items: %w", err)
	}

	// Load all read/saved statuses in one query (no N+1)
	statusMap, err := m.repo.GetReadStatusMap(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("get read status map: %w", err)
	}

	// Prepare result
	result := make([]ItemWithScore, 0, len(items))
	for _, item := range items {
		read := false
		saved := false
		if status, ok := statusMap[item.ID]; ok {
			read = true
			saved = status.Saved
		}

		result = append(result, ItemWithScore{
			Item:  item,
			Read:  read,
			Saved: saved,
			Score: calculateScore(&item),
		})
	}

	return result, nil
}

// MarkAsRead marks a news item as read.
func (m *NewsManager) MarkAsRead(ctx context.Context, chatID, itemID int64) error {
	return m.repo.CreateReadStatus(ctx, chatID, itemID, false)
}

// SaveItem saves a news item to favorites.
func (m *NewsManager) SaveItem(ctx context.Context, chatID, itemID int64) error {
	if err := m.repo.CreateReadStatus(ctx, chatID, itemID, true); err != nil {
		return fmt.Errorf("create read status: %w", err)
	}
	return m.repo.MarkAsSaved(ctx, chatID, itemID)
}

// UnsaveItem removes a news item from favorites.
func (m *NewsManager) UnsaveItem(ctx context.Context, chatID, itemID int64) error {
	return m.repo.MarkAsUnsaved(ctx, chatID, itemID)
}

// Subscribe subscribes a user to a feed.
func (m *NewsManager) Subscribe(ctx context.Context, chatID, feedID int64) error {
	// Check if feed exists
	feed, err := m.repo.GetFeed(ctx, feedID)
	if err != nil {
		return fmt.Errorf("get feed: %w", err)
	}
	if feed == nil {
		return NewFeedNotFoundError(feedID)
	}

	// Check if already subscribed
	existing, err := m.repo.GetSubscription(ctx, chatID, feedID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	if existing != nil {
		// Enable if exists
		existing.Enabled = true
		return m.repo.UpdateSubscription(ctx, existing)
	}

	// Create new subscription
	sub := &Subscription{
		ChatID:  chatID,
		FeedID:  feedID,
		Enabled: true,
	}
	_, err = m.repo.CreateSubscription(ctx, sub)
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}

	return nil
}

// Unsubscribe unsubscribes a user from a feed.
func (m *NewsManager) Unsubscribe(ctx context.Context, chatID, feedID int64) error {
	return m.repo.DeleteSubscription(ctx, chatID, feedID)
}

// ListFeeds lists all available feeds.
func (m *NewsManager) ListFeeds(ctx context.Context, limit, offset int) ([]Feed, error) {
	return m.repo.ListFeeds(ctx, limit, offset)
}

// UpdateFeedTags updates the tags of a feed.
func (m *NewsManager) UpdateFeedTags(ctx context.Context, feedID int64, tags string) error {
	feed, err := m.repo.GetFeed(ctx, feedID)
	if err != nil {
		return fmt.Errorf("get feed: %w", err)
	}
	if feed == nil {
		return NewFeedNotFoundError(feedID)
	}
	feed.Tags = tags
	return m.repo.UpdateFeed(ctx, feed)
}

// AddFeed adds a new feed.
func (m *NewsManager) AddFeed(ctx context.Context, url, feedTypeStr string) (*Feed, error) {
	// Check if already exists
	existing, err := m.repo.GetFeedByURL(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("get feed by URL: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	// Determine feed type
	var feedType FeedType
	switch feedTypeStr {
	case "rss":
		feedType = FeedTypeRSS
	case "atom":
		feedType = FeedTypeAtom
	case "json":
		feedType = FeedTypeJSON
	default:
		// Try to auto-detect by fetching
		data, err := m.fetcher.Fetch(ctx, url, "")
		if err != nil {
			return nil, fmt.Errorf("fetch feed: %w", err)
		}
		_, err = m.parser.Parse(data, FeedTypeRSS)
		if err == nil {
			feedType = FeedTypeRSS
		} else {
			_, err = m.parser.Parse(data, FeedTypeAtom)
			if err == nil {
				feedType = FeedTypeAtom
			} else {
				_, err = m.parser.Parse(data, FeedTypeJSON)
				if err == nil {
					feedType = FeedTypeJSON
				} else {
					return nil, NewInvalidFeedTypeError(feedTypeStr)
				}
			}
		}
	}

	// Create feed
	feed := &Feed{
		URL:           url,
		Title:         url, // Will be updated on first fetch
		Type:          feedType,
		Enabled:       true,
		FetchInterval: time.Hour,
	}
	id, err := m.repo.CreateFeed(ctx, feed)
	if err != nil {
		return nil, fmt.Errorf("create feed: %w", err)
	}
	feed.ID = id

	return feed, nil
}

// ItemWithScore represents an item with its read status and score.
type ItemWithScore struct {
	Item  Item
	Read  bool
	Saved bool
	Score float64
}

// calculateScore calculates a relevance score for an item.
func calculateScore(item *Item) float64 {
	age := time.Since(item.PublishedAt).Hours()
	freshness := 100.0 / (1.0 + age/24.0) // Decays over 24 hours
	return freshness
}
