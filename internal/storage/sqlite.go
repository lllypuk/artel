package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database.
type DB struct {
	db          *sql.DB
	hasFTS      bool // true if FTS5 virtual table for news was created successfully
	hasNotesFTS bool // true if FTS5 virtual table for notes was created successfully
	hasWikiFTS  bool // true if FTS5 virtual table for wiki pages was created successfully
}

// Open creates or opens the SQLite database and runs migrations.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
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

// HasFTS returns true if FTS5 full-text search for news is available.
func (s *DB) HasFTS() bool {
	return s.hasFTS
}

// HasNotesFTS returns true if FTS5 full-text search for notes is available.
func (s *DB) HasNotesFTS() bool {
	return s.hasNotesFTS
}

// HasWikiFTS returns true if FTS5 full-text search for wiki pages is available.
func (s *DB) HasWikiFTS() bool {
	return s.hasWikiFTS
}

func dropTriggers(ctx context.Context, db *sql.DB, names ...string) {
	for _, name := range names {
		_, _ = db.ExecContext(ctx, "DROP TRIGGER IF EXISTS ["+name+"]")
	}
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
			tags TEXT DEFAULT '',
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
		// Reminders table
		`CREATE TABLE IF NOT EXISTS reminders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			text TEXT NOT NULL,
			trigger_at DATETIME NOT NULL,
			repeat_interval INTEGER DEFAULT 0,
			status TEXT DEFAULT 'pending' CHECK(status IN ('pending', 'done', 'cancelled')),
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_chat ON reminders(chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reminders_trigger ON reminders(trigger_at, status)`,
		// Stock quotes cache table
		`CREATE TABLE IF NOT EXISTS stock_quotes_cache (
			ticker     TEXT NOT NULL,
			source     TEXT NOT NULL,
			data_json  TEXT NOT NULL,
			fetched_at DATETIME NOT NULL,
			PRIMARY KEY (ticker, source)
		)`,
		// Day reviews table (discipline tool)
		`CREATE TABLE IF NOT EXISTS day_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			planned TEXT DEFAULT '',
			done TEXT DEFAULT '',
			not_done TEXT DEFAULT '',
			reason TEXT DEFAULT '',
			score INTEGER DEFAULT 0,
			mood TEXT DEFAULT '',
			note TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(chat_id, date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_day_reviews_chat_date ON day_reviews(chat_id, date DESC)`,
		// Week reviews table (discipline tool)
		`CREATE TABLE IF NOT EXISTS week_reviews (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			week TEXT NOT NULL,
			wins TEXT DEFAULT '',
			fails TEXT DEFAULT '',
			pattern TEXT DEFAULT '',
			next_week_plan TEXT DEFAULT '',
			score INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(chat_id, week)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_week_reviews_chat ON week_reviews(chat_id, week DESC)`,
		// Objectives table (goals tool)
		`CREATE TABLE IF NOT EXISTS objectives (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			horizon TEXT DEFAULT 'quarter',
			period TEXT DEFAULT '',
			status TEXT DEFAULT 'active' CHECK(status IN ('active','paused','done','archived')),
			progress INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_objectives_chat ON objectives(chat_id, status)`,
		// Key results table (goals tool)
		`CREATE TABLE IF NOT EXISTS key_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			objective_id INTEGER NOT NULL REFERENCES objectives(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			progress INTEGER DEFAULT 0,
			deadline TEXT DEFAULT '',
			note TEXT DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_key_results_objective ON key_results(objective_id)`,
		// Usage statistics table
		`CREATE TABLE IF NOT EXISTS usage_stats (
			date TEXT PRIMARY KEY,
			prompt_tokens INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			requests_count INTEGER DEFAULT 0
		)`,
		// Workouts table (H-1)
		`CREATE TABLE IF NOT EXISTS workouts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			date TEXT NOT NULL,
			type TEXT NOT NULL,
			duration_min INTEGER DEFAULT 0,
			note TEXT DEFAULT '',
			goal_id INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workouts_chat_date ON workouts(chat_id, date DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_workouts_type ON workouts(chat_id, type)`,
		// Workout sets table (H-1)
		`CREATE TABLE IF NOT EXISTS workout_sets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			workout_id INTEGER NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
			exercise TEXT NOT NULL,
			sets INTEGER DEFAULT 0,
			reps INTEGER DEFAULT 0,
			weight_kg REAL DEFAULT 0,
			distance_km REAL DEFAULT 0,
			note TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workout_sets_workout ON workout_sets(workout_id)`,
		`CREATE INDEX IF NOT EXISTS idx_workout_sets_exercise ON workout_sets(exercise)`,
		// Workout templates table (H-3)
		`CREATE TABLE IF NOT EXISTS workout_templates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			exercises TEXT NOT NULL DEFAULT '[]',
			UNIQUE(chat_id, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workout_templates_chat ON workout_templates(chat_id)`,
		// Monitor checks table (G-1)
		`CREATE TABLE IF NOT EXISTS monitor_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			site_url TEXT NOT NULL,
			site_name TEXT NOT NULL,
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status_code INTEGER DEFAULT 0,
			response_ms INTEGER DEFAULT 0,
			is_up INTEGER NOT NULL DEFAULT 1,
			error_text TEXT DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_monitor_checks_url ON monitor_checks(site_url, checked_at DESC)`,
		// Monitor state table: last known state per site (G-1)
		`CREATE TABLE IF NOT EXISTS monitor_state (
			site_url TEXT PRIMARY KEY,
			site_name TEXT NOT NULL DEFAULT '',
			is_up INTEGER NOT NULL DEFAULT 1,
			last_checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_status_code INTEGER DEFAULT 0,
			last_response_ms INTEGER DEFAULT 0,
			last_error TEXT DEFAULT '',
			down_since_at DATETIME
		)`,
		// Wiki pages table (K-1)
		`CREATE TABLE IF NOT EXISTS wiki_pages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id    INTEGER NOT NULL,
			path       TEXT NOT NULL,
			title      TEXT NOT NULL,
			content    TEXT NOT NULL DEFAULT '',
			tags       TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(chat_id, path)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wiki_pages_chat ON wiki_pages(chat_id)`,
		// Wiki links table (K-2)
		`CREATE TABLE IF NOT EXISTS wiki_links (
			chat_id   INTEGER NOT NULL,
			from_path TEXT NOT NULL,
			to_path   TEXT NOT NULL,
			PRIMARY KEY (chat_id, from_path, to_path)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_wiki_links_backlinks ON wiki_links(chat_id, to_path)`,
		// Reading list table (K-4)
		`CREATE TABLE IF NOT EXISTS reading_list (
			id       INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id  INTEGER NOT NULL,
			url      TEXT NOT NULL DEFAULT '',
			title    TEXT NOT NULL,
			summary  TEXT NOT NULL DEFAULT '',
			content  TEXT NOT NULL DEFAULT '',
			status   TEXT NOT NULL DEFAULT 'to-read',
			priority TEXT NOT NULL DEFAULT 'medium',
			tags     TEXT NOT NULL DEFAULT '',
			saved_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			read_at  DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_reading_list_chat ON reading_list(chat_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reading_list_status ON reading_list(chat_id, status)`,
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
		`ALTER TABLE news_feeds ADD COLUMN tags TEXT DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN summary_text TEXT DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN summarized_until_id INTEGER DEFAULT 0`,
	}
	for _, m := range additiveMigrations {
		_, _ = s.db.ExecContext(ctx, m) // ignore error if column already exists
	}

	// Notes FTS5 (optional)
	notesFTS5Table := `CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
		title, content, tags,
		tokenize='porter unicode61'
	)`
	notesFTS5Triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS notes_ai AFTER INSERT ON notes BEGIN
			INSERT INTO notes_fts(rowid, title, content, tags)
			VALUES (new.id, new.title, new.content, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
			DELETE FROM notes_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS notes_au AFTER UPDATE ON notes BEGIN
			DELETE FROM notes_fts WHERE rowid = old.id;
			INSERT INTO notes_fts(rowid, title, content, tags)
			VALUES (new.id, new.title, new.content, new.tags);
		END`,
	}
	if _, err := s.db.ExecContext(ctx, notesFTS5Table); err == nil {
		dropTriggers(ctx, s.db, "notes_ai", "notes_ad", "notes_au")
		for _, trigger := range notesFTS5Triggers {
			_, _ = s.db.ExecContext(ctx, trigger)
		}
		// Populate FTS index for notes that existed before FTS was added.
		var ftsCount, notesCount int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes_fts").Scan(&ftsCount)
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes").Scan(&notesCount)
		if ftsCount == 0 && notesCount > 0 {
			_, _ = s.db.ExecContext(ctx,
				"INSERT INTO notes_fts(rowid, title, content, tags) SELECT id, title, content, tags FROM notes")
		}
		s.hasNotesFTS = true
	} else {
		dropTriggers(ctx, s.db, "notes_ai", "notes_ad", "notes_au")
	}

	// Wiki pages FTS5 (optional)
	wikiFTS5Table := `CREATE VIRTUAL TABLE IF NOT EXISTS wiki_pages_fts USING fts5(
		path, title, content, tags,
		tokenize='porter unicode61'
	)`
	wikiFTS5Triggers := []string{
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_ai AFTER INSERT ON wiki_pages BEGIN
			INSERT INTO wiki_pages_fts(rowid, path, title, content, tags)
			VALUES (new.id, new.path, new.title, new.content, new.tags);
		END`,
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_ad AFTER DELETE ON wiki_pages BEGIN
			DELETE FROM wiki_pages_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS wiki_pages_au AFTER UPDATE ON wiki_pages BEGIN
			DELETE FROM wiki_pages_fts WHERE rowid = old.id;
			INSERT INTO wiki_pages_fts(rowid, path, title, content, tags)
			VALUES (new.id, new.path, new.title, new.content, new.tags);
		END`,
	}
	if _, err := s.db.ExecContext(ctx, wikiFTS5Table); err == nil {
		dropTriggers(ctx, s.db, "wiki_pages_ai", "wiki_pages_ad", "wiki_pages_au")
		for _, trigger := range wikiFTS5Triggers {
			_, _ = s.db.ExecContext(ctx, trigger)
		}
		// Populate FTS index for pages that existed before FTS was added.
		var ftsCnt, pagesCnt int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_pages_fts").Scan(&ftsCnt)
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM wiki_pages").Scan(&pagesCnt)
		if ftsCnt == 0 && pagesCnt > 0 {
			_, _ = s.db.ExecContext(ctx,
				"INSERT INTO wiki_pages_fts(rowid, path, title, content, tags) SELECT id, path, title, content, tags FROM wiki_pages")
		}
		s.hasWikiFTS = true
	} else {
		dropTriggers(ctx, s.db, "wiki_pages_ai", "wiki_pages_ad", "wiki_pages_au")
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
			DELETE FROM news_items_fts WHERE rowid = old.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS news_items_au AFTER UPDATE ON news_items BEGIN
			DELETE FROM news_items_fts WHERE rowid = old.id;
			INSERT INTO news_items_fts(rowid, title, content, description)
			VALUES (new.id, new.title, new.content, new.description);
		END`,
	}

	// Try to create FTS5 virtual table first.
	// Only create triggers if the table was created successfully.
	if _, err := s.db.ExecContext(ctx, fts5Table); err == nil {
		dropTriggers(ctx, s.db, "news_items_ai", "news_items_ad", "news_items_au")
		for _, trigger := range fts5Triggers {
			_, _ = s.db.ExecContext(ctx, trigger)
		}
		s.hasFTS = true
	} else {
		// FTS5 not available — drop any stale triggers that might reference
		// a non-existent news_items_fts table (left from a previous run).
		dropTriggers(ctx, s.db, "news_items_ai", "news_items_ad", "news_items_au")
	}
	return nil
}

// Conversation represents a chat conversation.
type Conversation struct {
	ID                int64
	ChatID            int64
	Title             string
	Model             string
	SummaryText       string // summary of older messages
	SummarizedUntilID int64  // messages up to and including this ID are summarised
	CreatedAt         time.Time
	UpdatedAt         time.Time
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
		`SELECT id, chat_id, title, model,
		        COALESCE(summary_text, ''), COALESCE(summarized_until_id, 0),
		        created_at, updated_at
		 FROM conversations
		 WHERE chat_id = ?
		 ORDER BY updated_at DESC, id DESC LIMIT 1`,
		chatID,
	).Scan(&c.ID, &c.ChatID, &c.Title, &c.Model,
		&c.SummaryText, &c.SummarizedUntilID,
		&c.CreatedAt, &c.UpdatedAt)
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
// Uses FTS5 full-text search when available and query is non-empty;
// falls back to LIKE for empty queries or when FTS5 is not compiled in.
func (s *DB) SearchNotes(ctx context.Context, chatID int64, query string) ([]Note, error) {
	if query != "" && s.hasNotesFTS {
		return s.searchNotesFTS(ctx, chatID, query)
	}
	return s.searchNotesLike(ctx, chatID, query)
}

// searchNotesLike performs a basic LIKE search (fallback).
func (s *DB) searchNotesLike(ctx context.Context, chatID int64, query string) ([]Note, error) {
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

// searchNotesFTS performs FTS5 full-text search ordered by relevance.
func (s *DB) searchNotesFTS(ctx context.Context, chatID int64, query string) ([]Note, error) {
	ftsQuery := buildFTSQuery(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT n.id, n.chat_id, n.title, n.content, n.tags, n.created_at, n.updated_at
		 FROM notes_fts
		 JOIN notes n ON n.id = notes_fts.rowid
		 WHERE n.chat_id = ? AND notes_fts MATCH ?
		 ORDER BY notes_fts.rank
		 LIMIT 50`,
		chatID, ftsQuery,
	)
	if err != nil {
		// FTS query can fail on malformed input — fall back to LIKE
		return s.searchNotesLike(ctx, chatID, query)
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

// buildFTSQuery converts a plain-text search string into a safe FTS5 query.
// Each word is double-quoted to prevent injection of FTS5 operators.
func buildFTSQuery(q string) string {
	words := strings.Fields(q)
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		w = strings.ReplaceAll(w, `"`, `""`)
		quoted = append(quoted, `"`+w+`"`)
	}
	return strings.Join(quoted, " ")
}

// UpdateNote updates title, content and tags of an existing note.
func (s *DB) UpdateNote(ctx context.Context, id, chatID int64, title, content, tags string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE notes SET title=?, content=?, tags=?, updated_at=CURRENT_TIMESTAMP WHERE id=? AND chat_id=?",
		title, content, tags, id, chatID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("note %d not found or access denied", id)
	}
	return nil
}

// DeleteNote deletes a note by id (only if it belongs to chatID).
func (s *DB) DeleteNote(ctx context.Context, id, chatID int64) error {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM notes WHERE id=? AND chat_id=?",
		id, chatID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("note %d not found or access denied", id)
	}
	return nil
}

// GetNote returns a single note by id.
func (s *DB) GetNote(ctx context.Context, id, chatID int64) (*Note, error) {
	var n Note
	err := s.db.QueryRowContext(ctx,
		"SELECT id, chat_id, title, content, tags, created_at, updated_at FROM notes WHERE id=? AND chat_id=?",
		id, chatID,
	).Scan(&n.ID, &n.ChatID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("note %d not found", id)
	}
	return &n, err
}

// GetMessagesSince returns messages with id > afterID ordered by id.
// When afterID is 0 all messages are returned (same as GetMessages).
func (s *DB) GetMessagesSince(ctx context.Context, convID, afterID int64) ([]StoredMessage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, conversation_id, role, content, tool_call_id, tool_calls_json, created_at
		 FROM messages WHERE conversation_id = ? AND id > ? ORDER BY id`,
		convID, afterID,
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

// GetMessageCount returns the total number of messages stored for a conversation.
func (s *DB) GetMessageCount(ctx context.Context, convID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE conversation_id = ?", convID,
	).Scan(&count)
	return count, err
}

// UpdateConversationSummary persists a new summary and the highest message id
// that the summary covers.
func (s *DB) UpdateConversationSummary(ctx context.Context, convID int64, summaryText string, summarizedUntilID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE conversations
		 SET summary_text = ?, summarized_until_id = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		summaryText, summarizedUntilID, convID,
	)
	return err
}

// parseSQLiteTime parses a datetime string as stored by SQLite (UTC, no timezone suffix).
// modernc.org/sqlite returns datetime columns as plain strings, not time.Time.
// When time.Time values are inserted by Go, the driver serialises them as RFC3339.
// CURRENT_TIMESTAMP / strftime produce "2006-01-02 15:04:05" (space, no zone).
var sqliteTimeFormats = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999",
}

func parseSQLiteTime(s string) (time.Time, error) {
	for _, layout := range sqliteTimeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q", s)
}
