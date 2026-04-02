package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/news"
)

// NewsRepository defines the interface for news storage operations.
type NewsRepository interface {
	// Feeds
	CreateFeed(ctx context.Context, feed *news.Feed) (int64, error)
	GetFeed(ctx context.Context, id int64) (*news.Feed, error)
	GetFeedByURL(ctx context.Context, url string) (*news.Feed, error)
	GetEnabledFeeds(ctx context.Context) ([]news.Feed, error)
	UpdateFeed(ctx context.Context, feed *news.Feed) error
	DeleteFeed(ctx context.Context, id int64) error
	ListFeeds(ctx context.Context, limit, offset int) ([]news.Feed, error)

	// Items
	CreateItem(ctx context.Context, item *news.Item) (int64, error)
	GetItem(ctx context.Context, id int64) (*news.Item, error)
	ItemExistsByGUID(ctx context.Context, feedID int64, guid string) (bool, error)
	GetItemsByFeeds(ctx context.Context, feedIDs []int64, filter news.NewsFilter) ([]news.Item, error)
	GetItemByID(ctx context.Context, id int64) (*news.Item, error)
	DeleteOldItems(ctx context.Context, olderThan time.Time) (int64, error)

	// Subscriptions
	CreateSubscription(ctx context.Context, sub *news.Subscription) (int64, error)
	GetSubscription(ctx context.Context, chatID, feedID int64) (*news.Subscription, error)
	GetSubscriptions(ctx context.Context, chatID int64) ([]news.Subscription, error)
	UpdateSubscription(ctx context.Context, sub *news.Subscription) error
	DeleteSubscription(ctx context.Context, chatID, feedID int64) error

	// Read Status
	CreateReadStatus(ctx context.Context, chatID, itemID int64, saved bool) error
	GetReadStatus(ctx context.Context, chatID, itemID int64) (*news.ReadStatus, error)
	GetReadItemIDs(ctx context.Context, chatID int64) ([]int64, error)
	GetReadStatusMap(ctx context.Context, chatID int64) (map[int64]*news.ReadStatus, error)
	MarkAsSaved(ctx context.Context, chatID, itemID int64) error
	MarkAsUnsaved(ctx context.Context, chatID, itemID int64) error
	DeleteReadStatus(ctx context.Context, chatID, itemID int64) error

	// Cleanup
	DeleteUnreadItemsOlderThan(ctx context.Context, olderThan time.Time) (int64, error)
}

// NewsRepositoryImpl implements NewsRepository.
type NewsRepositoryImpl struct {
	db     *DB
	hasFTS bool
}

// NewNewsRepository creates a new NewsRepository.
func NewNewsRepository(db *DB) NewsRepository {
	return &NewsRepositoryImpl{db: db, hasFTS: db.HasFTS()}
}

// Feed operations

func (r *NewsRepositoryImpl) CreateFeed(ctx context.Context, feed *news.Feed) (int64, error) {
	res, err := r.db.db.ExecContext(ctx,
		`INSERT INTO news_feeds (url, title, description, type, category, language, enabled, fetch_interval_seconds)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		feed.URL, feed.Title, feed.Description, feed.Type, feed.Category, feed.Language,
		boolToInt(feed.Enabled), feed.FetchIntervalSeconds(),
	)
	if err != nil {
		return 0, fmt.Errorf("create feed: %w", err)
	}
	return res.LastInsertId()
}

func (r *NewsRepositoryImpl) GetFeed(ctx context.Context, id int64) (*news.Feed, error) {
	var feed news.Feed
	var lastFetched sql.NullTime
	var fetchInterval int64

	err := r.db.db.QueryRowContext(ctx,
		`SELECT id, url, title, description, type, category, language, enabled,
				fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
		 FROM news_feeds WHERE id = ?`,
		id,
	).Scan(
		&feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
		&feed.Category, &feed.Language, &feed.Enabled,
		&fetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastFetched.Valid {
		feed.LastFetched = &lastFetched.Time
	}
	feed.SetFetchIntervalFromSeconds(fetchInterval)
	return &feed, nil
}

func (r *NewsRepositoryImpl) GetFeedByURL(ctx context.Context, url string) (*news.Feed, error) {
	var feed news.Feed
	var lastFetched sql.NullTime
	var fetchInterval int64

	err := r.db.db.QueryRowContext(ctx,
		`SELECT id, url, title, description, type, category, language, enabled,
				fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
		 FROM news_feeds WHERE url = ?`,
		url,
	).Scan(
		&feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
		&feed.Category, &feed.Language, &feed.Enabled,
		&fetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastFetched.Valid {
		feed.LastFetched = &lastFetched.Time
	}
	feed.SetFetchIntervalFromSeconds(fetchInterval)
	return &feed, nil
}

func (r *NewsRepositoryImpl) GetEnabledFeeds(ctx context.Context) ([]news.Feed, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT id, url, title, description, type, category, language, enabled,
				fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
		 FROM news_feeds WHERE enabled = 1`,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
			// Log warning but do not fail
		}
	}()

	var feeds []news.Feed
	for rows.Next() {
		var feed news.Feed
		var lastFetched sql.NullTime
		var fetchInterval int64
		if err := rows.Scan(
			&feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
			&feed.Category, &feed.Language, &feed.Enabled,
			&fetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if lastFetched.Valid {
			feed.LastFetched = &lastFetched.Time
		}
		feed.SetFetchIntervalFromSeconds(fetchInterval)
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (r *NewsRepositoryImpl) UpdateFeed(ctx context.Context, feed *news.Feed) error {
	_, err := r.db.db.ExecContext(ctx,
		`UPDATE news_feeds
		 SET url = ?, title = ?, description = ?, type = ?, category = ?,
		     language = ?, enabled = ?, fetch_interval_seconds = ?,
		     last_fetched = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		feed.URL, feed.Title, feed.Description, feed.Type, feed.Category,
		feed.Language, boolToInt(feed.Enabled), feed.FetchIntervalSeconds(),
		feed.LastFetched, feed.LastError, feed.ID,
	)
	return err
}

func (r *NewsRepositoryImpl) DeleteFeed(ctx context.Context, id int64) error {
	_, err := r.db.db.ExecContext(ctx, `DELETE FROM news_feeds WHERE id = ?`, id)
	return err
}

func (r *NewsRepositoryImpl) ListFeeds(ctx context.Context, limit, offset int) ([]news.Feed, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT id, url, title, description, type, category, language, enabled,
				fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
		 FROM news_feeds ORDER BY title LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
			// Log warning but do not fail
		}
	}()

	var feeds []news.Feed
	for rows.Next() {
		var feed news.Feed
		var lastFetched sql.NullTime
		var fetchInterval int64
		if err := rows.Scan(
			&feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
			&feed.Category, &feed.Language, &feed.Enabled,
			&fetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if lastFetched.Valid {
			feed.LastFetched = &lastFetched.Time
		}
		feed.SetFetchIntervalFromSeconds(fetchInterval)
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

// Item operations

func (r *NewsRepositoryImpl) CreateItem(ctx context.Context, item *news.Item) (int64, error) {
	res, err := r.db.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO news_items
		 (feed_id, guid, title, link, description, content, author, category, image_url, published_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.FeedID, item.GUID, item.Title, item.Link, item.Description,
		item.Content, item.Author, item.Category, item.ImageURL, item.PublishedAt,
	)
	if err != nil {
		return 0, fmt.Errorf("create item: %w", err)
	}
	return res.LastInsertId()
}

func (r *NewsRepositoryImpl) GetItem(ctx context.Context, id int64) (*news.Item, error) {
	var item news.Item
	err := r.db.db.QueryRowContext(ctx,
		`SELECT id, feed_id, guid, title, link, description, content, author, category, image_url, published_at, created_at
		 FROM news_items WHERE id = ?`,
		id,
	).Scan(
		&item.ID, &item.FeedID, &item.GUID, &item.Title, &item.Link,
		&item.Description, &item.Content, &item.Author, &item.Category,
		&item.ImageURL, &item.PublishedAt, &item.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *NewsRepositoryImpl) GetItemByID(ctx context.Context, id int64) (*news.Item, error) {
	return r.GetItem(ctx, id)
}

func (r *NewsRepositoryImpl) ItemExistsByGUID(ctx context.Context, feedID int64, guid string) (bool, error) {
	var exists bool
	err := r.db.db.QueryRowContext(ctx,
		`SELECT 1 FROM news_items WHERE feed_id = ? AND guid = ? LIMIT 1`,
		feedID, guid,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return exists, err
}

func (r *NewsRepositoryImpl) GetItemsByFeeds(ctx context.Context, feedIDs []int64, filter news.NewsFilter) ([]news.Item, error) {
	if len(feedIDs) == 0 {
		return []news.Item{}, nil
	}

	// #nosec G202 - safe concatenation for placeholders only
	query := `SELECT id, feed_id, guid, title, link, description, content, author, category, image_url, published_at, created_at
	          FROM news_items WHERE feed_id IN (` + placeholders(len(feedIDs)) + `)`

	args := make([]any, 0, len(feedIDs)+5)
	for _, id := range feedIDs {
		args = append(args, id)
	}

	if filter.Category != "" {
		query += ` AND category = ?`
		args = append(args, filter.Category)
	}

	if filter.Since != nil {
		query += ` AND published_at >= ?`
		args = append(args, filter.Since)
	}

	if filter.SearchQuery != "" {
		if r.hasFTS {
			query += ` AND id IN (SELECT rowid FROM news_items_fts WHERE news_items_fts MATCH ?)`
			args = append(args, filter.SearchQuery)
		} else {
			// Fallback: plain LIKE search when FTS5 is not available
			query += ` AND (title LIKE ? OR description LIKE ? OR content LIKE ?)`
			like := "%" + filter.SearchQuery + "%"
			args = append(args, like, like, like)
		}
	}

	// DB-level unread/saved filtering (requires ChatID)
	if filter.ChatID != 0 {
		if filter.UnreadOnly {
			query += ` AND id NOT IN (SELECT item_id FROM news_read_status WHERE chat_id = ?)`
			args = append(args, filter.ChatID)
		}
		if filter.SavedOnly {
			query += ` AND id IN (SELECT item_id FROM news_read_status WHERE chat_id = ? AND saved = 1)`
			args = append(args, filter.ChatID)
		}
	}

	query += ` ORDER BY published_at DESC`

	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}

	rows, err := r.db.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
			// Log warning but do not fail
		}
	}()

	var items []news.Item
	for rows.Next() {
		var item news.Item
		if err := rows.Scan(
			&item.ID, &item.FeedID, &item.GUID, &item.Title, &item.Link,
			&item.Description, &item.Content, &item.Author, &item.Category,
			&item.ImageURL, &item.PublishedAt, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *NewsRepositoryImpl) DeleteOldItems(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := r.db.db.ExecContext(ctx,
		`DELETE FROM news_items WHERE created_at < ?`,
		olderThan,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Subscription operations

func (r *NewsRepositoryImpl) CreateSubscription(ctx context.Context, sub *news.Subscription) (int64, error) {
	res, err := r.db.db.ExecContext(ctx,
		`INSERT INTO news_subscriptions (chat_id, feed_id, enabled, notify, filter_keywords, filter_category)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sub.ChatID, sub.FeedID, boolToInt(sub.Enabled), boolToInt(sub.Notify),
		sub.FilterKeywords, sub.FilterCategory,
	)
	if err != nil {
		return 0, fmt.Errorf("create subscription: %w", err)
	}
	return res.LastInsertId()
}

func (r *NewsRepositoryImpl) GetSubscription(ctx context.Context, chatID, feedID int64) (*news.Subscription, error) {
	var sub news.Subscription
	err := r.db.db.QueryRowContext(ctx,
		`SELECT id, chat_id, feed_id, enabled, notify, filter_keywords, filter_category, created_at, updated_at
		 FROM news_subscriptions WHERE chat_id = ? AND feed_id = ?`,
		chatID, feedID,
	).Scan(
		&sub.ID, &sub.ChatID, &sub.FeedID, &sub.Enabled, &sub.Notify,
		&sub.FilterKeywords, &sub.FilterCategory, &sub.CreatedAt, &sub.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

func (r *NewsRepositoryImpl) GetSubscriptions(ctx context.Context, chatID int64) ([]news.Subscription, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT id, chat_id, feed_id, enabled, notify, filter_keywords, filter_category, created_at, updated_at
		 FROM news_subscriptions WHERE chat_id = ?`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
			// Log warning but do not fail
		}
	}()

	var subs []news.Subscription
	for rows.Next() {
		var sub news.Subscription
		if err := rows.Scan(
			&sub.ID, &sub.ChatID, &sub.FeedID, &sub.Enabled, &sub.Notify,
			&sub.FilterKeywords, &sub.FilterCategory, &sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (r *NewsRepositoryImpl) UpdateSubscription(ctx context.Context, sub *news.Subscription) error {
	_, err := r.db.db.ExecContext(ctx,
		`UPDATE news_subscriptions
		 SET enabled = ?, notify = ?, filter_keywords = ?, filter_category = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE chat_id = ? AND feed_id = ?`,
		boolToInt(sub.Enabled), boolToInt(sub.Notify), sub.FilterKeywords, sub.FilterCategory,
		sub.ChatID, sub.FeedID,
	)
	return err
}

func (r *NewsRepositoryImpl) DeleteSubscription(ctx context.Context, chatID, feedID int64) error {
	_, err := r.db.db.ExecContext(ctx,
		`DELETE FROM news_subscriptions WHERE chat_id = ? AND feed_id = ?`,
		chatID, feedID,
	)
	return err
}

// Read status operations

func (r *NewsRepositoryImpl) CreateReadStatus(ctx context.Context, chatID, itemID int64, saved bool) error {
	_, err := r.db.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO news_read_status (chat_id, item_id, saved)
		 VALUES (?, ?, COALESCE((SELECT saved FROM news_read_status WHERE chat_id = ? AND item_id = ?), ?))`,
		chatID, itemID, chatID, itemID, boolToInt(saved),
	)
	return err
}

func (r *NewsRepositoryImpl) GetReadStatus(ctx context.Context, chatID, itemID int64) (*news.ReadStatus, error) {
	var status news.ReadStatus
	err := r.db.db.QueryRowContext(ctx,
		`SELECT id, chat_id, item_id, read_at, saved FROM news_read_status WHERE chat_id = ? AND item_id = ?`,
		chatID, itemID,
	).Scan(&status.ID, &status.ChatID, &status.ItemID, &status.ReadAt, &status.Saved)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &status, nil
}

func (r *NewsRepositoryImpl) GetReadItemIDs(ctx context.Context, chatID int64) ([]int64, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT item_id FROM news_read_status WHERE chat_id = ?`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
			// Log warning but do not fail
		}
	}()

	var itemIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		itemIDs = append(itemIDs, id)
	}
	return itemIDs, rows.Err()
}

func (r *NewsRepositoryImpl) GetReadStatusMap(ctx context.Context, chatID int64) (map[int64]*news.ReadStatus, error) {
	rows, err := r.db.db.QueryContext(ctx,
		`SELECT id, chat_id, item_id, read_at, saved FROM news_read_status WHERE chat_id = ?`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err // ignore error
		}
	}()

	result := make(map[int64]*news.ReadStatus)
	for rows.Next() {
		var status news.ReadStatus
		if err := rows.Scan(&status.ID, &status.ChatID, &status.ItemID, &status.ReadAt, &status.Saved); err != nil {
			return nil, err
		}
		result[status.ItemID] = &status
	}
	return result, rows.Err()
}

func (r *NewsRepositoryImpl) MarkAsSaved(ctx context.Context, chatID, itemID int64) error {
	_, err := r.db.db.ExecContext(ctx,
		`UPDATE news_read_status SET saved = 1 WHERE chat_id = ? AND item_id = ?`,
		chatID, itemID,
	)
	return err
}

func (r *NewsRepositoryImpl) MarkAsUnsaved(ctx context.Context, chatID, itemID int64) error {
	_, err := r.db.db.ExecContext(ctx,
		`UPDATE news_read_status SET saved = 0 WHERE chat_id = ? AND item_id = ?`,
		chatID, itemID,
	)
	return err
}

func (r *NewsRepositoryImpl) DeleteReadStatus(ctx context.Context, chatID, itemID int64) error {
	_, err := r.db.db.ExecContext(ctx,
		`DELETE FROM news_read_status WHERE chat_id = ? AND item_id = ?`,
		chatID, itemID,
	)
	return err
}

// Cleanup operations

func (r *NewsRepositoryImpl) DeleteUnreadItemsOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := r.db.db.ExecContext(ctx,
		`DELETE FROM news_items WHERE id NOT IN (SELECT item_id FROM news_read_status) AND created_at < ?`,
		olderThan,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Helper functions

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}
