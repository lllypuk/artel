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
	db *sql.DB
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

func (s *DB) migrate(ctx context.Context) error {
	migrations := []string{
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
	}

	for _, m := range migrations {
		if _, err := s.db.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("exec migration: %w", err)
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
