package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite database.
type DB struct {
	db     *sql.DB
	hasFTS bool // true if FTS5 virtual table was created successfully
}

// Open creates or opens the SQLite database and runs migrations.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	s := &DB{db: db}
	if err := s.migrate(context.Background()); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, fmt.Errorf("migrate: %w (also failed to close: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the database.
func (s *DB) Close() error {
	return s.db.Close()
}

// HasFTS returns true if FTS5 full-text search is available.
func (s *DB) HasFTS() bool {
	return s.hasFTS
}

func (s *DB) migrate(ctx context.Context) error {
	// Core migrations (required)
	coreMigrations := []string{
		`CREATE TABLE IF NOT EXISTS conversations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			title TEXT DEFAULT '',
			model TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL REFERENCES conversations(id),
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			tool_call_id TEXT DEFAULT '',
			tool_calls_json TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversation_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conversations_chat ON conversations(chat_id)`,
		`CREATE TABLE IF NOT EXISTS notes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			tags TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_chat ON notes(chat_id)`,
		// News feeds table
		`CREATE TABLE IF NOT EXISTS news_feeds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			type TEXT NOT NULL CHECK(type IN ('rss', 'atom', 'json')),
			category TEXT DEFAULT '',
			language TEXT DEFAULT 'en',
			enabled INTEGER DEFAULT 1,
			proxy_url TEXT DEFAULT '',
			fetch_interval_seconds INTEGER DEFAULT 3600,
			last_fetched DATETIME,
			last_error TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		// Indexes for feeds
		`CREATE INDEX IF NOT EXISTS idx_news_feeds_url ON news_feeds(url)`,
		`CREATE INDEX IF NOT EXISTS idx_news_feeds_enabled ON news_feeds(enabled)`,
		// News items table
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

	// Execute core migrations (required)
	for _, m := range coreMigrations {
		if _, err := s.db.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("exec migration: %w", err)
		}
	}

	// Additive column migrations — tolerate "duplicate column" errors from older DBs.
	additiveMigrations := []string{
		`ALTER TABLE news_feeds ADD COLUMN proxy_url TEXT DEFAULT ''`,
	}
	for _, m := range additiveMigrations {
		_, _ = s.db.ExecContext(ctx, m) // ignore error if column already exists
	}

	// Execute FTS5 migrations (optional - may fail if FTS5 not compiled in)
	fts5Table := `CREATE VIRTUAL TABLE IF NOT EXISTS news_items_fts USING fts5(
		title, content, description,
		content_rowid=rowid,
		tokenize='porter unicode61'
	)`

	fts5Triggers := []string{
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
	}

	// Try to create FTS5 virtual table first.
	// Only create triggers if the table was created successfully.
	if _, err := s.db.ExecContext(ctx, fts5Table); err == nil {
		for _, trigger := range fts5Triggers {
			_, _ = s.db.ExecContext(ctx, trigger)
		}
		s.hasFTS = true
	} else {
		// FTS5 not available — drop any stale triggers that might reference
		// a non-existent news_items_fts table (left from a previous run).
		for _, name := range []string{"news_items_ai", "news_items_ad", "news_items_au"} {
			_, _ = s.db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name)
		}
	}
	return nil
}

// Conversation represents a chat conversation.
type Conversation struct {
	ID        int64
	ChatID    int64
	Title     string
	Model     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StoredMessage represents a stored message.
type StoredMessage struct {
	ID             int64
	ConversationID int64
	Role           string
	Content        string
	ToolCallID     string
	ToolCallsJSON  string
	CreatedAt      time.Time
}

// CreateConversation creates a new conversation.
func (s *DB) CreateConversation(ctx context.Context, chatID int64, model string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO conversations (chat_id, model) VALUES (?, ?)",
		chatID, model,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddMessage adds a message to a conversation.
func (s *DB) AddMessage(ctx context.Context, convID int64, role, content, toolCallID, toolCallsJSON string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO messages (conversation_id, role, content, tool_call_id, tool_calls_json) VALUES (?, ?, ?, ?, ?)",
		convID, role, content, toolCallID, toolCallsJSON,
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE conversations SET updated_at = CURRENT_TIMESTAMP WHERE id = ?", convID)
	return err
}

// GetMessages returns all messages for a conversation.
func (s *DB) GetMessages(ctx context.Context, convID int64) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, conversation_id, role, content, tool_call_id, tool_calls_json, created_at FROM messages WHERE conversation_id = ? ORDER BY id",
		convID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var msgs []StoredMessage
	for rows.Next() {
		var m StoredMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.ToolCallID, &m.ToolCallsJSON, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// GetActiveConversation returns the most recent conversation for a chat.
func (s *DB) GetActiveConversation(ctx context.Context, chatID int64) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRowContext(ctx,
		"SELECT id, chat_id, title, model, created_at, updated_at FROM conversations WHERE chat_id = ? ORDER BY updated_at DESC, id DESC LIMIT 1",
		chatID,
	).Scan(&c.ID, &c.ChatID, &c.Title, &c.Model, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Note represents a user note.
type Note struct {
	ID        int64
	ChatID    int64
	Title     string
	Content   string
	Tags      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateNote creates a new note.
func (s *DB) CreateNote(ctx context.Context, chatID int64, title, content, tags string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO notes (chat_id, title, content, tags) VALUES (?, ?, ?, ?)",
		chatID, title, content, tags,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SearchNotes searches notes by content or tags.
func (s *DB) SearchNotes(ctx context.Context, chatID int64, query string) ([]Note, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id, chat_id, title, content, tags, created_at, updated_at FROM notes WHERE chat_id = ? AND (title LIKE ? OR content LIKE ? OR tags LIKE ?) ORDER BY updated_at DESC",
		chatID, "%"+query+"%", "%"+query+"%", "%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var notes []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.ChatID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}
