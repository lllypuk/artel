# План разработки: Агрегация новостей

**Версия:** 1.1
**Дата:** 2026-04-02
**Статус:** Этап 1 (MVP) выполнен ✅

## Статус выполнения

- ✅ **Этап 1: MVP** - завершён 2026-04-02
- ⏳ **Этап 2: Улучшенный UX** - ожидает реализации
- ⏳ **Этап 3-7** - в планах

## Оглавление

- [1. Архитектура системы](#1-архитектура-системы)
- [2. Структура БД](#2-структура-бд)
- [3. API команд](#3-api-команд)
- [4. Конфигурация источников новостей](#4-конфигурация-источников-новостей)
- [5. Обработка ошибок и логирование](#5-обработка-ошибок-и-логирование)
- [6. Тестирование](#6-тестирование)
- [7. Roadmap](#7-roadmap)
- [8. Зависимости между этапами](#8-зависимости-между-этапами)

---

## 1. Архитектура системы

### 1.1. Обзор компонентов

```
┌─────────────────────────────────────────────────────────────────┐
│                         Telegram Bot                            │
│                    (/news, /subscribe, /unread, ...)           │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Agent Core                              │
│                      (ReAct Loop + LLM)                         │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│                    News Aggregation Tool                        │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │   Feeds      │  │   Parser     │  │  Fetcher     │          │
│  │   Manager    │  │  (gofeed)    │  │  (HTTP)      │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
└────────────────────────┬────────────────────────────────────────┘
                         │
         ┌───────────────┼───────────────┐
         ▼               ▼               ▼
┌──────────────────┐ ┌──────────┐ ┌──────────────────┐
│    SQLite DB     │ │  Cache   │ │  Scheduler      │
│  (feeds, items,  │ │ (in-mem) │ │  (cron fetch)    │
│   read_status)   │ │ optional)│ │                  │
└──────────────────┘ └──────────┘ └──────────────────┘
```

### 1.2. Файловая структура проекта

```
internal/
├── news/
│   ├── manager.go          # NewsManager: главный координатор
│   ├── feed.go             # Feed: структура RSS/Atom фида
│   ├── item.go             # Item: структура новости
│   ├── parser.go           # Parser на базе gofeed
│   ├── fetcher.go          # Fetcher: HTTP клиент для получения фидов
│   ├── cache.go            # Cache: кэширование фидов (опционально)
│   ├── scheduler.go        # Scheduler: планировщик обновлений
│   └── subscription.go     # Subscription: подписки пользователя
├── tools/
│   ├── news.go             # NewsTool: интеграция с Tool интерфейсом
│   └── news_test.go
├── storage/
│   ├── sqlite.go           # (существующий, расширить миграциями)
│   └── news.go             # NewsRepository: CRUD для новостей
├── config/
│   └── config.go           # (добавить NewsConfig)
└── telegram/
    └── bot.go              # (добавить команды новостей)

data/
└── news/
    ├── feeds.json          # предустановленные источники
    └── logs/               # логи парсера

docs/
└── plans/
    └── news_aggregation.md # этот документ

config.example.yaml         # (добавить секцию news)
```

### 1.3. Ключевые структуры данных

#### Feed (фид/источник)

```go
// internal/news/feed.go
package news

import "time"

type Feed struct {
    ID          int64     `json:"id" db:"id"`
    URL         string    `json:"url" db:"url"`
    Title       string    `json:"title" db:"title"`
    Description string    `json:"description" db:"description"`
    Type        FeedType  `json:"type" db:"type"`
    Category    string    `json:"category" db:"category"`
    Language    string    `json:"language" db:"language"`
    Enabled     bool      `json:"enabled" db:"enabled"`
    FetchInterval time.Duration `json:"fetch_interval" db:"fetch_interval"`
    LastFetched  *time.Time `json:"last_fetched" db:"last_fetched"`
    LastError    string    `json:"last_error,omitempty" db:"last_error"`
    CreatedAt    time.Time `json:"created_at" db:"created_at"`
    UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

type FeedType string

const (
    FeedTypeRSS  FeedType = "rss"
    FeedTypeAtom FeedType = "atom"
    FeedTypeJSON FeedType = "json" // JSON Feed (https://jsonfeed.org)
)
```

#### Item (новость/статья)

```go
// internal/news/item.go
package news

import "time"

type Item struct {
    ID          int64      `json:"id" db:"id"`
    FeedID      int64      `json:"feed_id" db:"feed_id"`
    GUID        string     `json:"guid" db:"guid"` // уникальный ID из фида
    Title       string     `json:"title" db:"title"`
    Link        string     `json:"link" db:"link"`
    Description string     `json:"description" db:"description"` // краткое описание
    Content     string     `json:"content" db:"content"`        // полный текст
    Author      string     `json:"author" db:"author"`
    Category    string     `json:"category" db:"category"`
    PublishedAt time.Time  `json:"published_at" db:"published_at"`
    CreatedAt   time.Time  `json:"created_at" db:"created_at"`
    ImageURL    string     `json:"image_url,omitempty" db:"image_url"`
}

// ItemScore используется для ранжирования новостей
type ItemScore struct {
    ItemID     int64   `json:"item_id"`
    Relevance  float64 `json:"relevance"`  // релевантность запросу
    Popularity float64 `json:"popularity"` // на основе кликов/сохранений
    Freshness  float64 `json:"freshness"`  // новизна
    FinalScore float64 `json:"final_score"` // взвешенная сумма
}
```

#### Subscription (подписка)

```go
// internal/news/subscription.go
package news

import "time"

type Subscription struct {
    ID           int64      `json:"id" db:"id"`
    ChatID       int64      `json:"chat_id" db:"chat_id"`
    FeedID       int64      `json:"feed_id" db:"feed_id"`
    Enabled      bool       `json:"enabled" db:"enabled"`
    Notify       bool       `json:"notify" db:"notify"` // push-уведомления
    FilterKeywords string   `json:"filter_keywords" db:"filter_keywords"` // запятые
    FilterCategory string   `json:"filter_category" db:"filter_category"`
    CreatedAt    time.Time  `json:"created_at" db:"created_at"`
    UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// ReadStatus отслеживает прочитанные новости
type ReadStatus struct {
    ID        int64     `json:"id" db:"id"`
    ChatID    int64     `json:"chat_id" db:"chat_id"`
    ItemID    int64     `json:"item_id" db:"item_id"`
    ReadAt    time.Time `json:"read_at" db:"read_at"`
    Saved     bool      `json:"saved" db:"saved"` // сохранена в избранное
}
```

### 1.4. Компоненты системы

#### NewsManager (главный координатор)

```go
// internal/news/manager.go
package news

import (
    "context"
    "log/slog"
    "time"
)

type NewsManager struct {
    repo      Repository
    parser    Parser
    fetcher   Fetcher
    scheduler *Scheduler
    logger    *slog.Logger
}

func NewNewsManager(repo Repository, parser Parser, fetcher Fetcher, logger *slog.Logger) *NewsManager {
    return &NewsManager{
        repo:    repo,
        parser:  parser,
        fetcher: fetcher,
        logger:  logger,
    }
}

// FetchFeed обновляет конкретный фид
func (m *NewsManager) FetchFeed(ctx context.Context, feedID int64) error {
    feed, err := m.repo.GetFeed(ctx, feedID)
    if err != nil {
        return err
    }

    data, err := m.fetcher.Fetch(ctx, feed.URL)
    if err != nil {
        feed.LastError = err.Error()
        m.repo.UpdateFeed(ctx, feed)
        return err
    }

    items, err := m.parser.Parse(data, feed.Type)
    if err != nil {
        feed.LastError = err.Error()
        m.repo.UpdateFeed(ctx, feed)
        return err
    }

    // Сохраняем новые новости
    saved := 0
    for _, item := range items {
        item.FeedID = feed.ID
        exists, err := m.repo.ItemExistsByGUID(ctx, feed.ID, item.GUID)
        if err != nil {
            m.logger.Error("check item exists", "error", err)
            continue
        }
        if !exists {
            if err := m.repo.CreateItem(ctx, &item); err != nil {
                m.logger.Error("create item", "error", err)
            } else {
                saved++
            }
        }
    }

    feed.LastFetched = &[]time.Time{time.Now()}[0]
    feed.LastError = ""
    if err := m.repo.UpdateFeed(ctx, feed); err != nil {
        return err
    }

    m.logger.Info("feed fetched", "feed_id", feedID, "new_items", saved)
    return nil
}

// FetchAllFeeds обновляет все активные фиды
func (m *NewsManager) FetchAllFeeds(ctx context.Context) error {
    feeds, err := m.repo.GetEnabledFeeds(ctx)
    if err != nil {
        return err
    }

    var lastErr error
    for _, feed := range feeds {
        if err := m.FetchFeed(ctx, feed.ID); err != nil {
            lastErr = err
        }
    }
    return lastErr
}

// GetNews возвращает новости с фильтрацией
func (m *NewsManager) GetNews(ctx context.Context, chatID int64, filter NewsFilter) ([]ItemWithScore, error) {
    // 1. Получаем подписки пользователя
    subs, err := m.repo.GetSubscriptions(ctx, chatID)
    if err != nil {
        return nil, err
    }

    feedIDs := make([]int64, 0, len(subs))
    for _, sub := range subs {
        if sub.Enabled {
            feedIDs = append(feedIDs, sub.FeedID)
        }
    }

    // 2. Получаем новости из подписанных фидов
    items, err := m.repo.GetItemsByFeeds(ctx, feedIDs, filter)
    if err != nil {
        return nil, err
    }

    // 3. Отмечаем прочитанные
    readItemIDs, err := m.repo.GetReadItemIDs(ctx, chatID)
    if err != nil {
        return nil, err
    }
    readSet := make(map[int64]bool)
    for _, id := range readItemIDs {
        readSet[id] = true
    }

    // 4. Подготавливаем результат
    result := make([]ItemWithScore, 0, len(items))
    for _, item := range items {
        result = append(result, ItemWithScore{
            Item:  item,
            Read:  readSet[item.ID],
            Score: calculateScore(&item),
        })
    }

    return result, nil
}

// MarkAsRead отмечает новость как прочитанную
func (m *NewsManager) MarkAsRead(ctx context.Context, chatID, itemID int64) error {
    return m.repo.CreateReadStatus(ctx, chatID, itemID, false)
}

// SaveItem сохраняет новость в избранное
func (m *NewsManager) SaveItem(ctx context.Context, chatID, itemID int64) error {
    return m.repo.CreateReadStatus(ctx, chatID, itemID, true)
}

type ItemWithScore struct {
    Item  Item
    Read  bool
    Score float64
}

type NewsFilter struct {
    Limit       int
    Offset      int
    Category    string
    SearchQuery string
    UnreadOnly  bool
    SavedOnly   bool
    Since       *time.Time
}

func calculateScore(item *Item) float64 {
    age := time.Since(item.PublishedAt).Hours()
    freshness := 100.0 / (1.0 + age/24.0) // убывает за сутки
    return freshness
}
```
#### Parser (парсер на основе github.com/mmcdole/gofeed)

```go
// internal/news/parser.go
package news

import (
    "fmt"
    "time"

    "github.com/mmcdole/gofeed"
)

type Parser interface {
    Parse(data []byte, feedType FeedType) ([]Item, error)
}

type GofeedParser struct {
    parser *gofeed.Parser
}

func NewGofeedParser() *GofeedParser {
    return &GofeedParser{
        parser: gofeed.NewParser(),
    }
}

func (p *GofeedParser) Parse(data []byte, _ FeedType) ([]Item, error) {
    feed, err := p.parser.ParseString(string(data))
    if err != nil {
        return nil, fmt.Errorf("parse feed: %w", err)
    }

    items := make([]Item, 0, len(feed.Items))
    for _, fi := range feed.Items {
        pubDate := time.Time{}
        if fi.PublishedParsed != nil {
            pubDate = *fi.PublishedParsed
        } else if fi.UpdatedParsed != nil {
            pubDate = *fi.UpdatedParsed
        }

        var category string
        if len(fi.Categories) > 0 {
            category = fi.Categories[0]
        }

        content := fi.Content
        if content == "" {
            content = fi.Description
        }

        guid := fi.GUID
        if guid == "" {
            guid = fi.Link
        }

        items = append(items, Item{
            GUID:        guid,
            Title:       fi.Title,
            Link:        fi.Link,
            Description: fi.Description,
            Content:     content,
            Author:      getAuthor(fi),
            Category:    category,
            PublishedAt: pubDate,
            ImageURL:    getImage(fi),
        })
    }

    return items, nil
}

func getAuthor(item *gofeed.Item) string {
    if item.Author != nil {
        return item.Author.Name
    }
    return ""
}

func getImage(item *gofeed.Item) string {
    if item.Image != nil {
        return item.Image.URL
    }
    return ""
}
```

#### Fetcher (HTTP клиент)

```go
// internal/news/fetcher.go
package news

import (
    "context"
    "fmt"
    "io"
    "net/http"
    "time"
)

type Fetcher interface {
    Fetch(ctx context.Context, url string) ([]byte, error)
}

type HTTPFetcher struct {
    client    *http.Client
    userAgent string
    timeout   time.Duration
}

func NewHTTPFetcher(timeout time.Duration) *HTTPFetcher {
    return &HTTPFetcher{
        client: &http.Client{
            Timeout: timeout,
        },
        userAgent: "Mozilla/5.0 (compatible; agent-shatrov/1.0; +https://github.com/lllypuk/agent-shatrov)",
        timeout:   timeout,
    }
}

func (f *HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }

    req.Header.Set("User-Agent", f.userAgent)
    req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json")

    resp, err := f.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("fetch: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    // Limit response size to prevent memory issues
    const maxResponseSize = 10 * 1024 * 1024 // 10MB
    limited := io.LimitReader(resp.Body, maxResponseSize)
    return io.ReadAll(limited)
}
```

#### Repository (хранилище новостей)

```go
// internal/storage/news.go
package storage

import (
    "context"
    "database/sql"
    "time"
)

// NewsRepository определяет интерфейс для работы с новостями
type NewsRepository interface {
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
    GetItemByID(ctx context.Context, id int64) (*Item, error)
    DeleteOldItems(ctx context.Context, olderThan time.Time) (int64, error)

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
    MarkAsSaved(ctx context.Context, chatID, itemID int64) error
    MarkAsUnsaved(ctx context.Context, chatID, itemID int64) error
    DeleteReadStatus(ctx context.Context, chatID, itemID int64) error

    // Cleanup
    DeleteUnreadItemsOlderThan(ctx context.Context, olderThan time.Time) (int64, error)
}

type NewsFilter struct {
    Limit       int
    Offset      int
    Category    string
    SearchQuery string
    UnreadOnly  bool
    SavedOnly   bool
    Since       *time.Time
}

// NewsRepositoryImpl реализует NewsRepository
type NewsRepositoryImpl struct {
    db *DB
}

func NewNewsRepository(db *DB) NewsRepository {
    return &NewsRepositoryImpl{db: db}
}

// ... реализации методов (см. раздел 2)
```

---

## 2. Структура БД

### 2.1. Миграции

Добавить в `internal/storage/sqlite.go` в `migrate()`:

```go
// Добавить к migrations в migrate():
{
    // Feeds table
    `CREATE TABLE IF NOT EXISTS news_feeds (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        url TEXT NOT NULL UNIQUE,
        title TEXT NOT NULL,
        description TEXT DEFAULT '',
        type TEXT NOT NULL CHECK(type IN ('rss', 'atom', 'json')),
        category TEXT DEFAULT '',
        language TEXT DEFAULT 'en',
        enabled INTEGER DEFAULT 1,
        fetch_interval_seconds INTEGER DEFAULT 3600,
        last_fetched DATETIME,
        last_error TEXT DEFAULT '',
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
    )`,

    // Indexes for feeds
    `CREATE INDEX IF NOT EXISTS idx_news_feeds_url ON news_feeds(url)`,
    `CREATE INDEX IF NOT EXISTS idx_news_feeds_enabled ON news_feeds(enabled)`,

    // Items table
    `CREATE TABLE IF NOT EXISTS news_items (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        feed_id INTEGER NOT NULL REFERENCES news_feeds(id) ON DELETE CASCADE,
        guid TEXT NOT NULL,
        title TEXT NOT NULL,
        link TEXT NOT NULL,
        description TEXT DEFAULT '',
        content TEXT DEFAULT '',
        author TEXT DEFAULT '',
        category TEXT DEFAULT '',
        image_url TEXT DEFAULT '',
        published_at DATETIME NOT NULL,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        UNIQUE(feed_id, guid)
    )`,

    // Indexes for items
    `CREATE INDEX IF NOT EXISTS idx_news_items_feed ON news_items(feed_id)`,
    `CREATE INDEX IF NOT EXISTS idx_news_items_published ON news_items(published_at DESC)`,
    `CREATE INDEX IF NOT EXISTS idx_news_items_guid ON news_items(feed_id, guid)`,

    // Full-text search for items
    `CREATE VIRTUAL TABLE IF NOT EXISTS news_items_fts USING fts5(
        title, content, description,
        content_rowid=rowid,
        tokenize='porter unicode61'
    )`,

    // Triggers for FTS
    `CREATE TRIGGER IF NOT EXISTS news_items_ai AFTER INSERT ON news_items BEGIN
        INSERT INTO news_items_fts(rowid, title, content, description)
        VALUES (new.id, new.title, new.content, new.description);
    END`,

    `CREATE TRIGGER IF NOT EXISTS news_items_ad AFTER DELETE ON news_items BEGIN
        INSERT INTO news_items_fts(news_items_fts, rowid, title, content, description)
        VALUES ('delete', old.id, old.title, old.content, old.description);
    END`,

    `CREATE TRIGGER IF NOT EXISTS news_items_au AFTER UPDATE ON news_items BEGIN
        INSERT INTO news_items_fts(news_items_fts, rowid, title, content, description)
        VALUES ('delete', old.id, old.title, old.content, old.description);
        INSERT INTO news_items_fts(rowid, title, content, description)
        VALUES (new.id, new.title, new.content, new.description);
    END`,

    // Subscriptions table
    `CREATE TABLE IF NOT EXISTS news_subscriptions (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        chat_id INTEGER NOT NULL,
        feed_id INTEGER NOT NULL REFERENCES news_feeds(id) ON DELETE CASCADE,
        enabled INTEGER DEFAULT 1,
        notify INTEGER DEFAULT 0,
        filter_keywords TEXT DEFAULT '',
        filter_category TEXT DEFAULT '',
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        UNIQUE(chat_id, feed_id)
    )`,

    // Indexes for subscriptions
    `CREATE INDEX IF NOT EXISTS idx_news_subscriptions_chat ON news_subscriptions(chat_id)`,
    `CREATE INDEX IF NOT EXISTS idx_news_subscriptions_feed ON news_subscriptions(feed_id)`,

    // Read status table
    `CREATE TABLE IF NOT EXISTS news_read_status (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        chat_id INTEGER NOT NULL,
        item_id INTEGER NOT NULL REFERENCES news_items(id) ON DELETE CASCADE,
        read_at DATETIME DEFAULT CURRENT_TIMESTAMP,
        saved INTEGER DEFAULT 0,
        UNIQUE(chat_id, item_id)
    )`,

    // Indexes for read status
    `CREATE INDEX IF NOT EXISTS idx_news_read_status_chat ON news_read_status(chat_id)`,
    `CREATE INDEX IF NOT EXISTS idx_news_read_status_item ON news_read_status(item_id)`,
    `CREATE INDEX IF NOT EXISTS idx_news_read_status_saved ON news_read_status(saved) WHERE saved = 1`,
}
```

### 2.2. Реализация NewsRepository

```go
// internal/storage/news.go (продолжение)

func (r *NewsRepositoryImpl) CreateFeed(ctx context.Context, feed *Feed) (int64, error) {
    res, err := r.db.db.ExecContext(ctx,
        `INSERT INTO news_feeds (url, title, description, type, category, language, enabled, fetch_interval_seconds)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
        feed.URL, feed.Title, feed.Description, feed.Type, feed.Category, feed.Language,
        boolToInt(feed.Enabled), int(feed.FetchIntervalSeconds()/time.Second),
    )
    if err != nil {
        return 0, fmt.Errorf("create feed: %w", err)
    }
    return res.LastInsertId()
}

func (r *NewsRepositoryImpl) GetFeed(ctx context.Context, id int64) (*Feed, error) {
    var feed Feed
    var lastFetched sql.NullTime
    err := r.db.db.QueryRowContext(ctx,
        `SELECT id, url, title, description, type, category, language, enabled,
                fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
         FROM news_feeds WHERE id = ?`,
        id,
    ).Scan(
        &feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
        &feed.Category, &feed.Language, &feed.Enabled,
        &feed.FetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
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
    feed.FetchInterval = time.Duration(feed.FetchInterval) * time.Second
    return &feed, nil
}

func (r *NewsRepositoryImpl) GetFeedByURL(ctx context.Context, url string) (*Feed, error) {
    var feed Feed
    var lastFetched sql.NullTime
    err := r.db.db.QueryRowContext(ctx,
        `SELECT id, url, title, description, type, category, language, enabled,
                fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
         FROM news_feeds WHERE url = ?`,
        url,
    ).Scan(
        &feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
        &feed.Category, &feed.Language, &feed.Enabled,
        &feed.FetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
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
    feed.FetchInterval = time.Duration(feed.FetchInterval) * time.Second
    return &feed, nil
}

func (r *NewsRepositoryImpl) GetEnabledFeeds(ctx context.Context) ([]Feed, error) {
    rows, err := r.db.db.QueryContext(ctx,
        `SELECT id, url, title, description, type, category, language, enabled,
                fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
         FROM news_feeds WHERE enabled = 1`,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var feeds []Feed
    for rows.Next() {
        var feed Feed
        var lastFetched sql.NullTime
        if err := rows.Scan(
            &feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
            &feed.Category, &feed.Language, &feed.Enabled,
            &feed.FetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
        ); err != nil {
            return nil, err
        }
        if lastFetched.Valid {
            feed.LastFetched = &lastFetched.Time
        }
        feed.FetchInterval = time.Duration(feed.FetchInterval) * time.Second
        feeds = append(feeds, feed)
    }
    return feeds, rows.Err()
}

func (r *NewsRepositoryImpl) UpdateFeed(ctx context.Context, feed *Feed) error {
    _, err := r.db.db.ExecContext(ctx,
        `UPDATE news_feeds
         SET url = ?, title = ?, description = ?, type = ?, category = ?,
             language = ?, enabled = ?, fetch_interval_seconds = ?,
             last_fetched = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
         WHERE id = ?`,
        feed.URL, feed.Title, feed.Description, feed.Type, feed.Category,
        feed.Language, boolToInt(feed.Enabled), int(feed.FetchIntervalSeconds()/time.Second),
        feed.LastFetched, feed.LastError, feed.ID,
    )
    return err
}

func (r *NewsRepositoryImpl) ListFeeds(ctx context.Context, limit, offset int) ([]Feed, error) {
    rows, err := r.db.db.QueryContext(ctx,
        `SELECT id, url, title, description, type, category, language, enabled,
                fetch_interval_seconds, last_fetched, last_error, created_at, updated_at
         FROM news_feeds ORDER BY title LIMIT ? OFFSET ?`,
        limit, offset,
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var feeds []Feed
    for rows.Next() {
        var feed Feed
        var lastFetched sql.NullTime
        if err := rows.Scan(
            &feed.ID, &feed.URL, &feed.Title, &feed.Description, &feed.Type,
            &feed.Category, &feed.Language, &feed.Enabled,
            &feed.FetchInterval, &lastFetched, &feed.LastError, &feed.CreatedAt, &feed.UpdatedAt,
        ); err != nil {
            return nil, err
        }
        if lastFetched.Valid {
            feed.LastFetched = &lastFetched.Time
        }
        feed.FetchInterval = time.Duration(feed.FetchInterval) * time.Second
        feeds = append(feeds, feed)
    }
    return feeds, rows.Err()
}

func (r *NewsRepositoryImpl) CreateItem(ctx context.Context, item *Item) (int64, error) {
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

func (r *NewsRepositoryImpl) GetItemsByFeeds(ctx context.Context, feedIDs []int64, filter NewsFilter) ([]Item, error) {
    query := `SELECT id, feed_id, guid, title, link, description, content, author, category, image_url, published_at, created_at
              FROM news_items WHERE feed_id IN (` + placeholders(len(feedIDs)) + `)`

    args := make([]any, 0, len(feedIDs)+1)
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
        query += ` AND id IN (SELECT rowid FROM news_items_fts WHERE news_items_fts MATCH ?)`
        args = append(args, filter.SearchQuery)
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
    defer rows.Close()

    var items []Item
    for rows.Next() {
        var item Item
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

// ... остальные методы аналогично

func boolToInt(b bool) int {
    if b {
        return 1
    }
    return 0
}

func intToBool(i int) bool {
    return i != 0
}

func placeholders(n int) string {
    if n <= 0 {
        return ""
    }
    return strings.Repeat("?,", n-1) + "?"
}
```

---

## 3. API команд

### 3.1. Telegram команды

| Команда | Описание | Пример |
|---------|----------|--------|
| `/news` | Показать последние новости | `/news` |
| `/news <query>` | Поиск по новостям | `/news golang` |
| `/unread` | Непрочитанные новости | `/unread` |
| `/saved` | Сохранённые новости | `/saved` |
| `/feeds` | Список доступных фидов | `/feeds` |
| `/subscribe <feed_id>` | Подписаться на фид | `/subscribe 1` |
| `/unsubscribe <feed_id>` | Отписаться от фида | `/unsubscribe 1` |
| `/discover <category>` | Найти фиды по категории | `/discover tech` |
| `/addfeed <url>` | Добавить кастомный фид | `/addfeed https://example.com/rss` |
| `/markread <item_id>` | Отметить как прочитанное | `/markread 123` |
| `/save <item_id>` | Сохранить в избранное | `/save 123` |

### 3.2. Интеграция с Tool интерфейсом

```go
// internal/tools/news.go
package tools

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/lllypuk/agent-shatrov/internal/config"
    "github.com/lllypuk/agent-shatrov/internal/news"
    "github.com/lllypuk/agent-shatrov/internal/storage"
)

type NewsTool struct {
    manager *news.NewsManager
    cfg     *config.NewsConfig
}

func NewNewsTool(db *storage.DB, cfg *config.NewsConfig) *NewsTool {
    newsRepo := storage.NewNewsRepository(db)
    parser := news.NewGofeedParser()
    fetcher := news.NewHTTPFetcher(30 * time.Second)
    logger := slog.Default()

    manager := news.NewNewsManager(newsRepo, parser, fetcher, logger)
    return &NewsTool{manager: manager, cfg: cfg}
}

type newsArgs struct {
    Action   string `json:"action"`  // list, subscribe, unsubscribe, search, unread, saved, discover, addfeed, markread, save
    ChatID   int64  `json:"chat_id"`
    Query    string `json:"query"`
    FeedID   int64  `json:"feed_id"`
    URL      string `json:"url"`
    ItemID   int64  `json:"item_id"`
    Limit    int    `json:"limit"`
    Category string `json:"category"`
}

func (n *NewsTool) Name() string { return "news" }

func (n *NewsTool) Description() string {
    return "Manage news subscriptions and read articles. Actions: list (show news), search (search by query), unread (unread news), saved (saved articles), subscribe (to feed), unsubscribe (from feed), discover (find feeds), addfeed (add custom RSS), markread (mark as read), save (save to favorites)."
}

func (n *NewsTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "action": map[string]any{
                "type": "string",
                "enum": []string{"list", "search", "unread", "saved", "subscribe", "unsubscribe", "discover", "addfeed", "markread", "save"},
                "description": "Action to perform",
            },
            "chat_id": map[string]any{
                "type":        "integer",
                "description": "User chat ID",
            },
            "query": map[string]any{
                "type":        "string",
                "description": "Search query (for search/discover)",
            },
            "feed_id": map[string]any{
                "type":        "integer",
                "description": "Feed ID (for subscribe/unsubscribe)",
            },
            "url": map[string]any{
                "type":        "string",
                "description": "Feed URL (for addfeed)",
            },
            "item_id": map[string]any{
                "type":        "integer",
                "description": "Item ID (for markread/save)",
            },
            "limit": map[string]any{
                "type":        "integer",
                "description": "Number of results (default: 10)",
            },
            "category": map[string]any{
                "type":        "string",
                "description": "Category filter (for discover)",
            },
        },
        "required": []string{"action", "chat_id"},
    }
}

func (n *NewsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
    var a newsArgs
    if err := json.Unmarshal(args, &a); err != nil {
        return "", fmt.Errorf("parse args: %w", err)
    }

    if a.Limit == 0 {
        a.Limit = 10
    }

    switch a.Action {
    case "list":
        return n.listNews(ctx, a.ChatID, a.Limit)

    case "search":
        return n.searchNews(ctx, a.ChatID, a.Query, a.Limit)

    case "unread":
        return n.unreadNews(ctx, a.ChatID, a.Limit)

    case "saved":
        return n.savedNews(ctx, a.ChatID, a.Limit)

    case "subscribe":
        return n.subscribe(ctx, a.ChatID, a.FeedID)

    case "unsubscribe":
        return n.unsubscribe(ctx, a.ChatID, a.FeedID)

    case "discover":
        return n.discoverFeeds(ctx, a.Category)

    case "addfeed":
        return n.addFeed(ctx, a.ChatID, a.URL)

    case "markread":
        return n.markRead(ctx, a.ChatID, a.ItemID)

    case "save":
        return n.saveItem(ctx, a.ChatID, a.ItemID)

    default:
        return "", fmt.Errorf("unknown action: %s", a.Action)
    }
}

func (n *NewsTool) listNews(ctx context.Context, chatID int64, limit int) (string, error) {
    filter := news.NewsFilter{Limit: limit}
    items, err := n.manager.GetNews(ctx, chatID, filter)
    if err != nil {
        return "", fmt.Errorf("get news: %w", err)
    }
    return formatNewsItems(items, "Latest news")
}

func (n *NewsTool) searchNews(ctx context.Context, chatID int64, query string, limit int) (string, error) {
    filter := news.NewsFilter{Limit: limit, SearchQuery: query}
    items, err := n.manager.GetNews(ctx, chatID, filter)
    if err != nil {
        return "", fmt.Errorf("search news: %w", err)
    }
    return formatNewsItems(items, fmt.Sprintf("Search: %s", query))
}

func (n *NewsTool) unreadNews(ctx context.Context, chatID int64, limit int) (string, error) {
    filter := news.NewsFilter{Limit: limit, UnreadOnly: true}
    items, err := n.manager.GetNews(ctx, chatID, filter)
    if err != nil {
        return "", fmt.Errorf("get unread: %w", err)
    }
    return formatNewsItems(items, "Unread news")
}

func (n *NewsTool) savedNews(ctx context.Context, chatID int64, limit int) (string, error) {
    filter := news.NewsFilter{Limit: limit, SavedOnly: true}
    items, err := n.manager.GetNews(ctx, chatID, filter)
    if err != nil {
        return "", fmt.Errorf("get saved: %w", err)
    }
    return formatNewsItems(items, "Saved articles")
}

func (n *NewsTool) subscribe(ctx context.Context, chatID, feedID int64) (string, error) {
    // Implementation
    sub := &news.Subscription{
        ChatID: chatID,
        FeedID: feedID,
        Enabled: true,
    }
    // ... save to DB
    return fmt.Sprintf("Subscribed to feed %d", feedID), nil
}

// ... остальные методы

func formatNewsItems(items []news.ItemWithScore, title string) string {
    if len(items) == 0 {
        return fmt.Sprintf("%s\nNo items found.", title)
    }

    var buf strings.Builder
    fmt.Fprintf(&buf, "%s (%d items)\n\n", title, len(items))

    for i, iw := range items {
        fmt.Fprintf(&buf, "%d. %s\n", i+1, iw.Item.Title)
        if !iw.Read {
            buf.WriteString("   🆕 ")
        }
        fmt.Fprintf(&buf, "   %s\n", iw.Item.Link)
        fmt.Fprintf(&buf, "   %s\n", iw.Item.PublishedAt.Format("2006-01-02 15:04"))
        if iw.Item.Category != "" {
            fmt.Fprintf(&buf, "   📁 %s\n", iw.Item.Category)
        }
        buf.WriteByte('\n')
    }

    return buf.String()
}
```

### 3.3. HTTP API (опционально, для внешних интеграций)

```
GET  /api/v1/news                    # список новостей
GET  /api/v1/news/:id                # детальная информация
GET  /api/v1/news/search?q=:query    # поиск
POST /api/v1/news/:id/read           # отметить прочитанным
POST /api/v1/news/:id/save           # сохранить

GET  /api/v1/feeds                   # список фидов
POST /api/v1/feeds                   # добавить фид
DELETE /api/v1/feeds/:id             # удалить фид

GET  /api/v1/subscriptions           # подписки пользователя
POST /api/v1/subscriptions           # подписаться
DELETE /api/v1/subscriptions/:id     # отписаться
```

---

## 4. Конфигурация источников новостей

### 4.1. Секция конфигурации

Добавить в `config.yaml`:

```yaml
news:
  enabled: true

  # Интервал автоматического обновления фидов
  fetch_interval_minutes: 30

  # Максимальный возраст новостей для хранения
  max_item_age_days: 30

  # Максимальное количество новостей на фид
  max_items_per_feed: 1000

  # HTTP таймаут для получения фидов
  fetch_timeout_seconds: 30

  # Предустановленные источники новостей
  feeds:
    # Tech news
    - url: "https://techcrunch.com/feed/"
      title: "TechCrunch"
      category: "tech"
      language: "en"
      enabled: true

    - url: "https://www.theverge.com/rss/index.xml"
      title: "The Verge"
      category: "tech"
      language: "en"
      enabled: true

    - url: "https://feeds.arstechnica.com/arstechnica/index"
      title: "Ars Technica"
      category: "tech"
      language: "en"
      enabled: true

    # Open Source / Dev
    - url: "https://lwn.net/headlines/newrss"
      title: "LWN.net"
      category: "opensource"
      language: "en"
      enabled: true

    - url: "https://opensource.com/feeds"
      title: "Opensource.com"
      category: "opensource"
      language: "en"
      enabled: true

    # Go
    - url: "https://go.dev/blog/feed.atom"
      title: "Go Blog"
      category: "golang"
      language: "en"
      enabled: true

    - url: "https://blog.golang.org/feed.atom"
      title: "Go Blog (Old)"
      category: "golang"
      language: "en"
      enabled: true

    # Russian news (опционально)
    - url: "https://habr.com/ru/rss/articles/?fl=ru"
      title: "Habr: Ru Articles"
      category: "tech"
      language: "ru"
      enabled: false

    - url: "https://3dnews.ru/news/rss/"
      title: "3DNews"
      category: "tech"
      language: "ru"
      enabled: false

  # Категории для discover
  categories:
    tech:
      name: "Technology"
      feeds: ["techcrunch", "theverge", "arstechnica"]
    opensource:
      name: "Open Source"
      feeds: ["lwn", "opensource.com"]
    golang:
      name: "Go Programming"
      feeds: ["go-blog"]

  # Настройки очистки
  cleanup:
    enabled: true
    run_at_hour: 3  # запуск в 3 ночи
    delete_unread_older_than_days: 7
    delete_all_older_than_days: 30
```

### 4.2. Структуры конфигурации

```go
// internal/config/config.go (добавить)

type NewsConfig struct {
    Enabled              bool               `yaml:"enabled"`
    FetchIntervalMinutes int                `yaml:"fetch_interval_minutes"`
    MaxItemAgeDays       int                `yaml:"max_item_age_days"`
    MaxItemsPerFeed      int                `yaml:"max_items_per_feed"`
    FetchTimeoutSeconds  int                `yaml:"fetch_timeout_seconds"`
    Feeds                []FeedConfig       `yaml:"feeds"`
    Categories           map[string]CategoryConfig `yaml:"categories"`
    Cleanup              CleanupConfig      `yaml:"cleanup"`
}

type FeedConfig struct {
    URL         string `yaml:"url"`
    Title       string `yaml:"title"`
    Category    string `yaml:"category"`
    Language    string `yaml:"language"`
    Enabled     bool   `yaml:"enabled"`
}

type CategoryConfig struct {
    Name  string   `yaml:"name"`
    Feeds []string `yaml:"feeds"`
}

type CleanupConfig struct {
    Enabled                     bool `yaml:"enabled"`
    RunAtHour                   int  `yaml:"run_at_hour"`
    DeleteUnreadOnlyOlderThanDays int `yaml:"delete_unread_older_than_days"`
    DeleteAllOlderThanDays      int  `yaml:"delete_all_older_than_days"`
}
```

### 4.3. Предустановленные источники (data/feeds.json)

```json
{
  "version": "1.0",
  "last_updated": "2026-04-01T00:00:00Z",
  "feeds": [
    {
      "id": "hackernews",
      "url": "https://hnrss.org/frontpage",
      "title": "Hacker News",
      "description": "Top stories from Hacker News",
      "category": "tech",
      "language": "en",
      "type": "rss",
      "icon_url": "https://news.ycombinator.com/favicon.ico"
    },
    {
      "id": "reddit-golang",
      "url": "https://www.reddit.com/r/golang/.rss",
      "title": "r/golang",
      "description": "Go discussions on Reddit",
      "category": "golang",
      "language": "en",
      "type": "rss"
    },
    {
      "id": "github-trending",
      "url": "https://github-trending-api.now.sh/",
      "title": "GitHub Trending",
      "description": "Trending repositories",
      "category": "dev",
      "language": "en",
      "type": "json"
    }
  ],
  "categories": {
    "tech": {
      "name": "Technology",
      "icon": "💻"
    },
    "opensource": {
      "name": "Open Source",
      "icon": "🔓"
    },
    "golang": {
      "name": "Go Programming",
      "icon": "🐹"
    },
    "ai": {
      "name": "AI & Machine Learning",
      "icon": "🤖"
    },
    "science": {
      "name": "Science",
      "icon": "🔬"
    }
  }
}
```

---

## 5. Обработка ошибок и логирование

### 5.1. Типы ошибок

```go
// internal/news/errors.go
package news

import "fmt"

type NewsError struct {
    Code    string
    Message string
    Err     error
}

func (e *NewsError) Error() string {
    if e.Err != nil {
        return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
    }
    return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *NewsError) Unwrap() error {
    return e.Err
}

// Коды ошибок
const (
    ErrCodeFetchFailed      = "FETCH_FAILED"
    ErrCodeParseFailed      = "PARSE_FAILED"
    ErrCodeFeedNotFound     = "FEED_NOT_FOUND"
    ErrCodeFeedExists       = "FEED_EXISTS"
    ErrCodeItemNotFound     = "ITEM_NOT_FOUND"
    ErrCodeInvalidURL       = "INVALID_URL"
    ErrCodeInvalidFeedType  = "INVALID_FEED_TYPE"
    ErrCodeSubscriptionNotFound = "SUBSCRIPTION_NOT_FOUND"
    ErrCodeDatabase         = "DATABASE_ERROR"
)

func NewFetchError(err error, url string) *NewsError {
    return &NewsError{
        Code:    ErrCodeFetchFailed,
        Message: fmt.Sprintf("failed to fetch feed %s", url),
        Err:     err,
    }
}

func NewParseError(err error, feedType string) *NewsError {
    return &NewsError{
        Code:    ErrCodeParseFailed,
        Message: fmt.Sprintf("failed to parse %s feed", feedType),
        Err:     err,
    }
}

func NewFeedNotFoundError(id int64) *NewsError {
    return &NewsError{
        Code:    ErrCodeFeedNotFound,
        Message: fmt.Sprintf("feed %d not found", id),
    }
}
```

### 5.2. Логирование

Использовать `log/slog` (Go 1.21+):

```go
// В NewsManager
import "log/slog"

func (m *NewsManager) FetchFeed(ctx context.Context, feedID int64) error {
    m.logger.Info("fetching feed", "feed_id", feedID)

    feed, err := m.repo.GetFeed(ctx, feedID)
    if err != nil {
        m.logger.Error("get feed", "feed_id", feedID, "error", err)
        return err
    }

    m.logger.Debug("fetching from URL", "url", feed.URL)

    data, err := m.fetcher.Fetch(ctx, feed.URL)
    if err != nil {
        feed.LastError = err.Error()
        m.repo.UpdateFeed(ctx, feed)

        m.logger.Error("fetch failed",
            "feed_id", feedID,
            "url", feed.URL,
            "error", err,
        )
        return NewFetchError(err, feed.URL)
    }

    m.logger.Debug("parsing feed", "size", len(data), "type", feed.Type)

    items, err := m.parser.Parse(data, feed.Type)
    if err != nil {
        feed.LastError = err.Error()
        m.repo.UpdateFeed(ctx, feed)

        m.logger.Error("parse failed",
            "feed_id", feedID,
            "type", feed.Type,
            "error", err,
        )
        return NewParseError(err, string(feed.Type))
    }

    saved := 0
    for _, item := range items {
        exists, err := m.repo.ItemExistsByGUID(ctx, feed.ID, item.GUID)
        if err != nil {
            m.logger.Error("check item exists", "item_guid", item.GUID, "error", err)
            continue
        }
        if !exists {
            if err := m.repo.CreateItem(ctx, &item); err != nil {
                m.logger.Error("create item", "item_title", item.Title, "error", err)
            } else {
                saved++
                m.logger.Debug("item saved", "title", item.Title, "guid", item.GUID)
            }
        }
    }

    feed.LastFetched = &[]time.Time{time.Now()}[0]
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
```

### 5.3. Метрики (опционально)

```go
// internal/news/metrics.go
package news

import "time"

type Metrics struct {
    FeedsFetched      int64
    FeedsFailed       int64
    ItemsSaved        int64
    ItemsDuplicate    int64
    FetchDuration     time.Duration
    LastFetchTime     time.Time
}

func (m *Metrics) RecordFetch(duration time.Duration, success bool, newItemCount int) {
    m.FeedsFetched++
    if !success {
        m.FeedsFailed++
    }
    m.ItemsSaved += int64(newItemCount)
    m.FetchDuration += duration
    m.LastFetchTime = time.Now()
}
```

---

## 6. Тестирование

### 6.1. Unit-тесты для Parser

```go
// internal/news/parser_test.go
package news

import (
    "testing"
)

func TestGofeedParser(t *testing.T) {
    rssData := `<?xml version="1.0" encoding="UTF-8"?>
    <rss version="2.0">
        <channel>
            <title>Test Feed</title>
            <link>https://example.com</link>
            <item>
                <title>Test Article</title>
                <link>https://example.com/article</link>
                <description>Test description</description>
                <pubDate>Mon, 02 Jan 2006 15:04:05 MST</pubDate>
                <guid>unique-id-123</guid>
            </item>
        </channel>
    </rss>`

    parser := NewGofeedParser()
    items, err := parser.Parse([]byte(rssData), FeedTypeRSS)
    
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }
    
    if len(items) != 1 {
        t.Errorf("Expected 1 item, got %d", len(items))
    }
    
    if items[0].Title != "Test Article" {
        t.Errorf("Expected title 'Test Article', got '%s'", items[0].Title)
    }
    
    if items[0].GUID != "unique-id-123" {
        t.Errorf("Expected GUID 'unique-id-123', got '%s'", items[0].GUID)
    }
}

func TestGofeedParser_Atom(t *testing.T) {
    atomData := `<?xml version="1.0" encoding="UTF-8"?>
    <feed xmlns="http://www.w3.org/2005/Atom">
        <title>Test Feed</title>
        <entry>
            <title>Test Article</title>
            <link href="https://example.com/article"/>
            <content>Test content</content>
            <published>2026-04-01T12:00:00Z</published>
            <id>urn:uuid:12345678-1234-1234-1234-123456789012</id>
        </entry>
    </feed>`

    parser := NewGofeedParser()
    items, err := parser.Parse([]byte(atomData), FeedTypeAtom)
    
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }
    
    if len(items) != 1 {
        t.Errorf("Expected 1 item, got %d", len(items))
    }
}

func TestGofeedParser_JSONFeed(t *testing.T) {
    jsonData := `{
        "version": "https://jsonfeed.org/version/1",
        "title": "Test Feed",
        "items": [
            {
                "id": "123",
                "url": "https://example.com/article",
                "title": "Test Article",
                "content_html": "<p>Test content</p>",
                "date_published": "2026-04-01T12:00:00Z"
            }
        ]
    }`

    parser := NewGofeedParser()
    items, err := parser.Parse([]byte(jsonData), FeedTypeJSON)
    
    if err != nil {
        t.Fatalf("Parse() error = %v", err)
    }
    
    if len(items) != 1 {
        t.Errorf("Expected 1 item, got %d", len(items))
    }
}
```

### 6.2. Тесты с моками

```go
// internal/news/manager_test.go
package news

import (
    "context"
    "testing"
    "time"
)

type MockRepository struct {
    feeds map[int64]*Feed
    items map[int64][]Item
}

func (m *MockRepository) GetFeed(ctx context.Context, id int64) (*Feed, error) {
    feed, ok := m.feeds[id]
    if !ok {
        return nil, nil
    }
    return feed, nil
}

func (m *MockRepository) UpdateFeed(ctx context.Context, feed *Feed) error {
    m.feeds[feed.ID] = feed
    return nil
}

// ... остальные методы

func TestNewsManager_FetchFeed(t *testing.T) {
    ctx := context.Background()

    mockRepo := &MockRepository{
        feeds: map[int64]*Feed{
            1: {
                ID:  1,
                URL: "https://example.com/feed",
                Type: FeedTypeRSS,
            },
        },
    }

    mockFetcher := &MockFetcher{
        data: []byte(`<?xml version="1.0"?><rss><channel><item><title>Test</title></item></channel></rss>`),
    }

    parser := NewGofeedParser()
    manager := NewNewsManager(mockRepo, parser, mockFetcher, slog.Default())

    err := manager.FetchFeed(ctx, 1)
    if err != nil {
        t.Fatalf("FetchFeed() error = %v", err)
    }
}

type MockFetcher struct {
    data []byte
    err  error
}

func (m *MockFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
    return m.data, m.err
}
```

### 6.3. Интеграционные тесты

```go
// internal/storage/news_integration_test.go
package storage

import (
    "context"
    "testing"
    "time"
)

func TestNewsRepository(t *testing.T) {
    db, err := Open(":memory:")
    if err != nil {
        t.Fatalf("Open() error = %v", err)
    }
    defer db.Close()

    ctx := context.Background()
    repo := NewNewsRepository(db)

    // Create feed
    feed := &Feed{
        URL:         "https://example.com/rss",
        Title:       "Test Feed",
        Description: "Test",
        Type:        FeedTypeRSS,
        Category:    "tech",
        Language:    "en",
        Enabled:     true,
    }

    feedID, err := repo.CreateFeed(ctx, feed)
    if err != nil {
        t.Fatalf("CreateFeed() error = %v", err)
    }

    // Get feed
    fetched, err := repo.GetFeed(ctx, feedID)
    if err != nil {
        t.Fatalf("GetFeed() error = %v", err)
    }
    if fetched.Title != "Test Feed" {
        t.Errorf("Expected title 'Test Feed', got '%s'", fetched.Title)
    }

    // Create item
    item := &Item{
        FeedID:      feedID,
        GUID:        "test-guid",
        Title:       "Test Item",
        Link:        "https://example.com/item",
        PublishedAt: time.Now(),
    }

    itemID, err := repo.CreateItem(ctx, item)
    if err != nil {
        t.Fatalf("CreateItem() error = %v", err)
    }

    // Check exists
    exists, err := repo.ItemExistsByGUID(ctx, feedID, "test-guid")
    if err != nil {
        t.Fatalf("ItemExistsByGUID() error = %v", err)
    }
    if !exists {
        t.Error("Expected item to exist")
    }

    // Duplicate should not create
    _, err = repo.CreateItem(ctx, item)
    if err != nil {
        t.Errorf("Duplicate item should not error, got %v", err)
    }
}
```

### 6.4. Тесты для Tool интерфейса

```go
// internal/tools/news_test.go
package tools

import (
    "context"
    "encoding/json"
    "testing"

    "github.com/lllypuk/agent-shatrov/internal/config"
)

func TestNewsTool_ParseArgs(t *testing.T) {
    tests := []struct {
        name    string
        args    string
        want    newsArgs
        wantErr bool
    }{
        {
            name: "list action",
            args: `{"action":"list","chat_id":123}`,
            want: newsArgs{Action: "list", ChatID: 123},
        },
        {
            name: "search action",
            args: `{"action":"search","chat_id":123,"query":"golang"}`,
            want: newsArgs{Action: "search", ChatID: 123, Query: "golang"},
        },
        {
            name: "missing action",
            args: `{"chat_id":123}`,
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            var got newsArgs
            err := json.Unmarshal([]byte(tt.args), &got)

            if (err != nil) != tt.wantErr {
                t.Errorf("ParseArgs() error = %v, wantErr %v", err, tt.wantErr)
                return
            }

            if !tt.wantErr && got != tt.want {
                t.Errorf("ParseArgs() = %+v, want %+v", got, tt.want)
            }
        })
    }
}
```

---

## 7. Roadmap

### Этап 1: MVP (Mинимально жизнеспособный продукт) ✅ **ЗАВЕРШЁН**

**Срок:** 1-2 недели
**Приоритет:** P0 (критический)

**Статус:** ✅ Выполнено 2026-04-02

- [x] Базовые структуры данных (Feed, Item, Subscription)
- [x] RSS/Atom/JSON Feed парсер (github.com/mmcdole/gofeed)
- [x] HTTP Fetcher
- [x] Миграции БД (таблицы feeds, items, subscriptions, read_status)
- [x] NewsRepository (CRUD операции)
- [x] NewsManager (fetch, parse, store)
- [x] NewsTool (интеграция с Tool интерфейсом)
- [x] Конфигурация предустановленных фидов (6 источников)
- [x] Автоматическое обновление фидов (каждые 30 минут)
- [x] Проверка lint и test (без ошибок)

**Примечание:** Telegram команды реализованы через LLM tool интерфейс (actions: list, search, unread, saved, subscribe, unsubscribe, discover, addfeed, markread, save, unsave).

**Критерии готовности:**
- Пользователь может подписаться на фид
- Новости автоматически обновляются раз в час
- Непрочитанные новости отображаются в `/news`
- Пройдены `make lint test`

---

### Этап 2: Улучшенный UX

**Срок:** 1 неделя
**Приоритет:** P1 (высокий)

- [ ] Команда `/unread` (только непрочитанные)
- [ ] Команда `/saved` (избранное)
- [ ] Команда `/markread` и `/save`
- [ ] FTS (полнотекстовый поиск) по новостям
- [ ] Команда `/discover` (поиск фидов по категории)
- [ ] Категоризация новостей
- [ ] Красивое форматирование сообщений (эмодзи, разделители)
- [ ] Пагинация (лимиты 10/20/50 новостей)

---

### Этап 3: JSON Feed и кэширование

**Срок:** 3-5 дней
**Приоритет:** P2 (средний)

- [ ] Извлечение изображений из новостей
- [ ] Предпросмотр контента (og:tag, meta description)
- [ ] Команда `/addfeed` (добавление произвольного RSS)

---

### Этап 4: Планировщик и уведомления

**Срок:** 1 неделя
**Приоритет:** P2 (средний)

- [ ] Scheduler (горутины с time.Ticker)
- [ ] Конфигурируемый интервал обновления
- [ ] Push-уведомления о новых новостях
- [ ] Дайджест (ежедневная/еженедельная рассылка)
- [ ] Настройка частоты обновлений на фид

---

### Этап 5: Продвинутые фильтры и рекомендации

**Срок:** 1-2 недели
**Приоритет:** P3 (низкий)

- [ ] Фильтрация по ключевым словам
- [ ] Фильтрация по авторам
- [ ] Blacklist/whitelist источников
- [ ] Ранжирование по популярности (клики/сохранения)
- [ ] Персональные рекомендации (простой ML)
- [ ] Поиск по тегам

---

### Этап 6: Улучшения пользовательского опыта

**Срок:** 3-5 дней  
**Приоритет:** P3 (низкий)

- [ ] Логи в отдельный файл (`data/news.log`)
- [ ] Статистика использования (популярные фиды)
- [ ] Оповещения о проблемных фидах

---

### Этап 7: Оптимизация и масштабирование

**Срок:** 1 неделя
**Приоритет:** P4 (будущее)

- [ ] Горячее кэширование (sync.Map)
- [ ] Параллельный fetch фидов (worker pool)
- [ ] Удаление старых новостей (cleanup job)
- [ ] Индексация БД (оптимизация запросов)
- [ ] Rate limiting для fetch

---

## 8. Зависимости между этапами

```
Этап 1 (MVP)
    ↓
Этап 2 (UX) ───→ Этап 3 (JSON Feed)
    ↓                  ↓
Этап 4 (Scheduler) ──┘
    ↓
Этап 5 (Фильтры) ───→ Этап 6 (Улучшения UX)
    ↓
Этап 7 (Оптимизация)
```

**Критические зависимости:**

1. **Этап 2** зависит от **Этапа 1** (базовая функциональность)
2. **Этап 4** зависит от **Этапа 1** (NewsManager должен работать)
3. **Этап 5** зависит от **Этапа 2** (структура новостей определена)
4. **Этап 7** зависит от всех предыдущих (есть что оптимизировать)

**Параллельная разработка:**
- Этап 2 и Этап 3 могут разрабатываться параллельно
- Этап 5 и Этап 6 могут начаться после Этапа 4

---

## Приложение А: Дополнительные зависимости для go.mod

```
require (
    // Существующие зависимости
    github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
    github.com/mattn/go-sqlite3 v1.14.18
    gopkg.in/yaml.v3 v3.0.1

    // Новые зависимости для новостей
    github.com/mmcdole/gofeed v1.2.1  // RSS/Atom/JSON Feed парсер
)

---

## Приложение Б: Примеры использования

### Б.1. Пример диалога в Telegram

```
User: /news
Bot: Latest news (10 items)

1. Go 1.21 Released with New Features
   🆕 https://go.dev/blog/go1.21
   2026-04-01 10:00
   📁 golang

2. Google Announces New AI Model
   🆕 https://blog.google/ai/new-model
   2026-04-01 09:30
   📁 tech

...

User: /search golang
Bot: Search: golang (5 items)

1. Go 1.21 Released with New Features
   https://go.dev/blog/go1.21
   2026-04-01 10:00
   📁 golang

...

User: /feeds
Bot: Available feeds (15)

1. TechCrunch [tech] ✅ subscribed
2. The Verge [tech]
3. Go Blog [golang] ✅ subscribed
...

User: /subscribe 2
Bot: ✓ Subscribed to "The Verge"
```

### Б.2. Пример конфигурации с несколькими пользователями

```yaml
news:
  enabled: true

  # Глобальные настройки
  fetch_interval_minutes: 30
  max_item_age_days: 30

  # Фиды, доступные всем
  feeds:
    - url: "https://go.dev/blog/feed.atom"
      title: "Go Blog"
      category: "golang"
      enabled: true

  # Персональные настройки (опционально)
  user_settings:
    123456789:  # chat_id
      default_category: "golang"
      limit: 20
      notify: true
```

---

## Чек-лист для завершения этапа 1 (MVP) ✅

- [x] Созданы все структуры данных в `internal/news/`
- [x] Реализован `GofeedParser` на базе github.com/mmcdole/gofeed
- [x] Реализован `HTTPFetcher` с timeout
- [x] Миграции добавлены в `sqlite.go`
- [x] Реализован `NewsRepository` со всеми методами
- [x] Реализован `NewsManager` с `FetchFeed` и `GetNews`
- [x] `NewsTool` интегрирован с Tool интерфейсом
- [x] Доступны команды через LLM (list, search, unread, saved, subscribe, unsubscribe, discover, addfeed, markread, save, unsave)
- [x] Конфигурация `news:` добавлена в `config.example.yaml`
- [x] Существующие тесты проходят после изменений
- [x] Пройдены `make lint` (без ошибок)
- [x] Пройдены `make test` (все тесты pass)
- [x] Автоматическое обновление фидов (каждые 30 минут)

---

## 📋 Отчёт о выполнении Этапа 1 (MVP)

### Реализованные компоненты

#### Структуры данных (`internal/news/`)
- ✅ `feed.go` - структура Feed с типами RSS/Atom/JSON
- ✅ `item.go` - структура Item для новостей
- ✅ `subscription.go` - Subscription и ReadStatus
- ✅ `errors.go` - типы ошибок с кодами
- ✅ `filter.go` - NewsFilter для запросов

#### Компоненты агрегации
- ✅ `parser.go` - GofeedParser на базе github.com/mmcdole/gofeed
- ✅ `fetcher.go` - HTTPFetcher с таймаутом и User-Agent
- ✅ `manager.go` - NewsManager для координации операций

#### Хранилище (`internal/storage/`)
- ✅ `sqlite.go` - миграции БД (таблицы feeds, items, subscriptions, read_status)
- ✅ `news.go` - NewsRepository с полным CRUD

#### Интеграция (`internal/tools/`)
- ✅ `news.go` - NewsTool интегрирован с Tool интерфейсом

#### Конфигурация
- ✅ `config.example.yaml` - секция `news:` с 6 предустановленными фидами
- ✅ `config.go` - NewsConfig struct

#### Главный цикл (`cmd/agent/`)
- ✅ Автоматическое обновление фидов каждые 30 минут
- ✅ Инициализация предустановленных фидов при запуске

### Доступные действия через NewsTool

| Action | Описание | Аргументы |
|--------|----------|----------|
| `list` | Показать последние новости | `limit` (default: 10) |
| `search` | Поиск по новостям | `query`, `limit` |
| `unread` | Непрочитанные новости | `limit` |
| `saved` | Сохранённые статьи | `limit` |
| `subscribe` | Подписаться на фид | `feed_id` |
| `unsubscribe` | Отписаться от фида | `feed_id` |
| `discover` | Список доступных фидов | `category` |
| `addfeed` | Добавить кастомный RSS | `url` |
| `markread` | Отметить как прочитанное | `item_id` |
| `save` | Сохранить в избранное | `item_id` |
| `unsave` | Удалить из избранного | `item_id` |

### Предустановленные источники новостей

1. TechCrunch (tech) - https://techcrunch.com/feed/
2. The Verge (tech) - https://www.theverge.com/rss/index.xml
3. Ars Technica (tech) - https://feeds.arstechnica.com/arstechnica/index
4. LWN.net (opensource) - https://lwn.net/headlines/newrss
5. Opensource.com (opensource) - https://opensource.com/feeds
6. Go Blog (golang) - https://go.dev/blog/feed.atom

### Статус качества

- ✅ `make lint` - 0 ошибок
- ✅ `make test` - все тесты проходят

### Технические детали

- **Парсер:** github.com/mmcdole/gofeed v1.3.0
- **FTS5:** Опциональный (игнорирует ошибку если не скомпилирован)
- **Автообновление:** 30 минут (настраивается через конфиг)
- **Полнотекстовый поиск:** Через SQLite FTS5 (если доступен)

---

**Документ обновлён:** 2026-04-02
**Версия:** 1.0
