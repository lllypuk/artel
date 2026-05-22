package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// WikiPage represents a wiki page.
type WikiPage struct {
	ID        int64
	ChatID    int64
	Path      string
	Title     string
	Content   string
	Tags      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ReadingItem represents an item in the reading list.
type ReadingItem struct {
	ID       int64
	ChatID   int64
	URL      string
	Title    string
	Summary  string
	Content  string
	Status   string // to-read / reading / done
	Priority string // high / medium / low
	Tags     string
	SavedAt  time.Time
	ReadAt   *time.Time
}

// --- Wiki Pages ---

// CreateWikiPage creates a new wiki page and returns its ID.
func (s *DB) CreateWikiPage(ctx context.Context, chatID int64, path, title, content, tags string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO wiki_pages (chat_id, path, title, content, tags) VALUES (?, ?, ?, ?, ?)`,
		chatID, path, title, content, tags)
	if err != nil {
		return 0, fmt.Errorf("create wiki page: %w", err)
	}
	return res.LastInsertId()
}

// GetWikiPage retrieves a wiki page by path.
func (s *DB) GetWikiPage(ctx context.Context, chatID int64, path string) (*WikiPage, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, chat_id, path, title, content, tags, created_at, updated_at
		 FROM wiki_pages WHERE chat_id = ? AND path = ?`,
		chatID, path)
	return scanWikiPageRow(row)
}

// GetWikiPageByID retrieves a wiki page by ID.
func (s *DB) GetWikiPageByID(ctx context.Context, chatID, id int64) (*WikiPage, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, chat_id, path, title, content, tags, created_at, updated_at
		 FROM wiki_pages WHERE chat_id = ? AND id = ?`,
		chatID, id)
	return scanWikiPageRow(row)
}

func scanWikiPageRow(row *sql.Row) (*WikiPage, error) {
	var p WikiPage
	var createdAt, updatedAt string
	err := row.Scan(&p.ID, &p.ChatID, &p.Path, &p.Title, &p.Content, &p.Tags, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan wiki page: %w", err)
	}
	p.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	p.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
	return &p, nil
}

// UpdateWikiPage updates the title, content and tags of an existing wiki page.
func (s *DB) UpdateWikiPage(ctx context.Context, chatID int64, path, title, content, tags string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE wiki_pages SET title = ?, content = ?, tags = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE chat_id = ? AND path = ?`,
		title, content, tags, chatID, path)
	if err != nil {
		return fmt.Errorf("update wiki page: %w", err)
	}
	return nil
}

// DeleteWikiPage deletes a wiki page and all its link records in a transaction.
func (s *DB) DeleteWikiPage(ctx context.Context, chatID int64, path string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wiki_links WHERE chat_id = ? AND (from_path = ? OR to_path = ?)`,
		chatID, path, path); err != nil {
		return fmt.Errorf("delete wiki links: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wiki_pages WHERE chat_id = ? AND path = ?`,
		chatID, path); err != nil {
		return fmt.Errorf("delete wiki page: %w", err)
	}
	return tx.Commit()
}

// ListWikiPages returns pages whose path starts with the given prefix (section listing).
// If prefix is empty, returns all pages for the chat.
func (s *DB) ListWikiPages(ctx context.Context, chatID int64, prefix string) ([]*WikiPage, error) {
	if prefix == "" {
		return s.AllWikiPages(ctx, chatID)
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, path, title, content, tags, created_at, updated_at
		 FROM wiki_pages WHERE chat_id = ? AND path LIKE ?
		 ORDER BY path`,
		chatID, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("list wiki pages: %w", err)
	}
	defer rows.Close()
	return scanWikiPageRows(rows)
}

// AllWikiPages returns all wiki pages for a chat ordered by path.
func (s *DB) AllWikiPages(ctx context.Context, chatID int64) ([]*WikiPage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, path, title, content, tags, created_at, updated_at
		 FROM wiki_pages WHERE chat_id = ?
		 ORDER BY path`,
		chatID)
	if err != nil {
		return nil, fmt.Errorf("all wiki pages: %w", err)
	}
	defer rows.Close()
	return scanWikiPageRows(rows)
}

func scanWikiPageRows(rows *sql.Rows) ([]*WikiPage, error) {
	var pages []*WikiPage
	for rows.Next() {
		var p WikiPage
		var createdAt, updatedAt string
		if err := rows.Scan(&p.ID, &p.ChatID, &p.Path, &p.Title, &p.Content, &p.Tags, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan wiki page row: %w", err)
		}
		p.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		p.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updatedAt)
		pages = append(pages, &p)
	}
	return pages, rows.Err()
}

// RenameWikiPage renames a page and cascades the change to all wiki_links in a transaction.
func (s *DB) RenameWikiPage(ctx context.Context, chatID int64, oldPath, newPath string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`UPDATE wiki_pages SET path = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE chat_id = ? AND path = ?`,
		newPath, chatID, oldPath); err != nil {
		return fmt.Errorf("rename wiki page: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE wiki_links SET from_path = ? WHERE chat_id = ? AND from_path = ?`,
		newPath, chatID, oldPath); err != nil {
		return fmt.Errorf("update wiki_links from_path: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE wiki_links SET to_path = ? WHERE chat_id = ? AND to_path = ?`,
		newPath, chatID, oldPath); err != nil {
		return fmt.Errorf("update wiki_links to_path: %w", err)
	}
	// Rewrite [[oldPath]] → [[newPath]] in the content of all pages in this chat.
	oldLink := "[[" + oldPath + "]]"
	newLink := "[[" + newPath + "]]"
	if _, err := tx.ExecContext(ctx,
		`UPDATE wiki_pages SET content = REPLACE(content, ?, ?), updated_at = CURRENT_TIMESTAMP
		 WHERE chat_id = ? AND content LIKE ?`,
		oldLink, newLink, chatID, "%"+oldLink+"%"); err != nil {
		return fmt.Errorf("rewrite wiki page content: %w", err)
	}
	return tx.Commit()
}

// --- Wiki Links ---

// UpsertWikiLinks replaces all outgoing links from a page in a transaction.
func (s *DB) UpsertWikiLinks(ctx context.Context, chatID int64, fromPath string, toPaths []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM wiki_links WHERE chat_id = ? AND from_path = ?`,
		chatID, fromPath); err != nil {
		return fmt.Errorf("delete wiki links: %w", err)
	}
	for _, toPath := range toPaths {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO wiki_links (chat_id, from_path, to_path) VALUES (?, ?, ?)`,
			chatID, fromPath, toPath); err != nil {
			return fmt.Errorf("insert wiki link: %w", err)
		}
	}
	return tx.Commit()
}

// GetBacklinks returns all pages that link to the given path.
func (s *DB) GetBacklinks(ctx context.Context, chatID int64, toPath string) ([]*WikiPage, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.chat_id, p.path, p.title, p.content, p.tags, p.created_at, p.updated_at
		 FROM wiki_links l
		 JOIN wiki_pages p ON p.chat_id = l.chat_id AND p.path = l.from_path
		 WHERE l.chat_id = ? AND l.to_path = ?
		 ORDER BY p.path`,
		chatID, toPath)
	if err != nil {
		return nil, fmt.Errorf("get backlinks: %w", err)
	}
	defer rows.Close()
	return scanWikiPageRows(rows)
}

// SearchWikiPages searches wiki pages by query using FTS5 or LIKE fallback.
func (s *DB) SearchWikiPages(ctx context.Context, chatID int64, query string) ([]*WikiPage, error) {
	if query == "" {
		return s.AllWikiPages(ctx, chatID)
	}
	if s.hasWikiFTS {
		return s.searchWikiPagesFTS(ctx, chatID, query)
	}
	return s.searchWikiPagesLike(ctx, chatID, query)
}

func (s *DB) searchWikiPagesFTS(ctx context.Context, chatID int64, query string) ([]*WikiPage, error) {
	// Quote each term to prevent FTS5 operator injection from user input.
	safeQuery := buildFTSQuery(query)
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.chat_id, p.path, p.title, p.content, p.tags, p.created_at, p.updated_at
		 FROM wiki_pages_fts f
		 JOIN wiki_pages p ON p.id = f.rowid
		 WHERE p.chat_id = ? AND wiki_pages_fts MATCH ?
		 ORDER BY f.rank`,
		chatID, safeQuery)
	if err != nil {
		// FTS failed (e.g. malformed query) — fall back to LIKE search.
		return s.searchWikiPagesLike(ctx, chatID, query)
	}
	defer rows.Close()
	return scanWikiPageRows(rows)
}

func (s *DB) searchWikiPagesLike(ctx context.Context, chatID int64, query string) ([]*WikiPage, error) {
	like := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, path, title, content, tags, created_at, updated_at
		 FROM wiki_pages
		 WHERE chat_id = ? AND (title LIKE ? OR content LIKE ? OR path LIKE ? OR tags LIKE ?)
		 ORDER BY updated_at DESC`,
		chatID, like, like, like, like)
	if err != nil {
		return nil, fmt.Errorf("like search wiki pages: %w", err)
	}
	defer rows.Close()
	return scanWikiPageRows(rows)
}

// --- Reading List ---

// CreateReadingItem adds a new reading list item and returns its ID.
func (s *DB) CreateReadingItem(ctx context.Context, chatID int64, url, title, summary, content, status, priority, tags string) (int64, error) {
	if status == "" {
		status = "to-read"
	}
	if priority == "" {
		priority = "medium"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO reading_list (chat_id, url, title, summary, content, status, priority, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		chatID, url, title, summary, content, status, priority, tags)
	if err != nil {
		return 0, fmt.Errorf("create reading item: %w", err)
	}
	return res.LastInsertId()
}

// GetReadingItem retrieves a reading list item by ID.
func (s *DB) GetReadingItem(ctx context.Context, chatID, id int64) (*ReadingItem, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, chat_id, url, title, summary, content, status, priority, tags, saved_at, read_at
		 FROM reading_list WHERE chat_id = ? AND id = ?`,
		chatID, id)
	return scanReadingItemRow(row)
}

func scanReadingItemRow(row *sql.Row) (*ReadingItem, error) {
	var item ReadingItem
	var savedAt string
	var readAt sql.NullString
	err := row.Scan(&item.ID, &item.ChatID, &item.URL, &item.Title, &item.Summary,
		&item.Content, &item.Status, &item.Priority, &item.Tags, &savedAt, &readAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan reading item: %w", err)
	}
	item.SavedAt, _ = time.Parse("2006-01-02 15:04:05", savedAt)
	if readAt.Valid && readAt.String != "" {
		t, _ := time.Parse("2006-01-02 15:04:05", readAt.String)
		item.ReadAt = &t
	}
	return &item, nil
}

// UpdateReadingItem updates a reading list item, preserving fields that are left empty.
func (s *DB) UpdateReadingItem(ctx context.Context, chatID, id int64, title, status, priority, tags string, markRead bool) error {
	existing, err := s.GetReadingItem(ctx, chatID, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("reading item %d not found", id)
	}
	if title == "" {
		title = existing.Title
	}
	if status == "" {
		status = existing.Status
	}
	if priority == "" {
		priority = existing.Priority
	}
	if tags == "" {
		tags = existing.Tags
	}
	var readAt any
	if markRead || status == "done" {
		// Preserve existing read-at timestamp if already done; otherwise record now.
		if existing.ReadAt != nil {
			readAt = existing.ReadAt.UTC().Format("2006-01-02 15:04:05")
		} else {
			readAt = time.Now().UTC().Format("2006-01-02 15:04:05")
		}
	}
	// else: status != "done" and markRead is false → readAt stays nil → NULL in DB
	_, err = s.db.ExecContext(ctx,
		`UPDATE reading_list SET title = ?, status = ?, priority = ?, tags = ?, read_at = ?
		 WHERE chat_id = ? AND id = ?`,
		title, status, priority, tags, readAt, chatID, id)
	return err
}

// DeleteReadingItem deletes a reading list item.
func (s *DB) DeleteReadingItem(ctx context.Context, chatID, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM reading_list WHERE chat_id = ? AND id = ?`,
		chatID, id)
	return err
}

// ListReadingItems returns reading list items with optional filters.
func (s *DB) ListReadingItems(ctx context.Context, chatID int64, status, priority, tags string) ([]*ReadingItem, error) {
	q := `SELECT id, chat_id, url, title, summary, content, status, priority, tags, saved_at, read_at
	      FROM reading_list WHERE chat_id = ?`
	args := []any{chatID}
	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	if priority != "" {
		q += " AND priority = ?"
		args = append(args, priority)
	}
	if tags != "" {
		q += " AND tags LIKE ?"
		args = append(args, "%"+tags+"%")
	}
	q += " ORDER BY saved_at DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list reading items: %w", err)
	}
	defer rows.Close()
	return scanReadingItemRows(rows)
}

// SearchReadingItems searches reading list by query using LIKE.
func (s *DB) SearchReadingItems(ctx context.Context, chatID int64, query string) ([]*ReadingItem, error) {
	if query == "" {
		return s.ListReadingItems(ctx, chatID, "", "", "")
	}
	like := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, chat_id, url, title, summary, content, status, priority, tags, saved_at, read_at
		 FROM reading_list
		 WHERE chat_id = ? AND (title LIKE ? OR summary LIKE ? OR url LIKE ? OR tags LIKE ?)
		 ORDER BY saved_at DESC`,
		chatID, like, like, like, like)
	if err != nil {
		return nil, fmt.Errorf("search reading items: %w", err)
	}
	defer rows.Close()
	return scanReadingItemRows(rows)
}

func scanReadingItemRows(rows *sql.Rows) ([]*ReadingItem, error) {
	var items []*ReadingItem
	for rows.Next() {
		var item ReadingItem
		var savedAt string
		var readAt sql.NullString
		if err := rows.Scan(&item.ID, &item.ChatID, &item.URL, &item.Title, &item.Summary,
			&item.Content, &item.Status, &item.Priority, &item.Tags, &savedAt, &readAt); err != nil {
			return nil, fmt.Errorf("scan reading item row: %w", err)
		}
		item.SavedAt, _ = time.Parse("2006-01-02 15:04:05", savedAt)
		if readAt.Valid && readAt.String != "" {
			t, _ := time.Parse("2006-01-02 15:04:05", readAt.String)
			item.ReadAt = &t
		}
		items = append(items, &item)
	}
	return items, rows.Err()
}

// ReadingListDigest returns a count of to-read items grouped by tag.
func (s *DB) ReadingListDigest(ctx context.Context, chatID int64) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT tags FROM reading_list WHERE chat_id = ? AND status IN ('to-read', 'reading')`,
		chatID)
	if err != nil {
		return nil, fmt.Errorf("reading list digest: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var tags string
		if err := rows.Scan(&tags); err != nil {
			return nil, fmt.Errorf("scan tags: %w", err)
		}
		tagList := parseTagList(tags)
		if len(tagList) == 0 {
			counts["(без тегов)"]++
		} else {
			for _, tag := range tagList {
				counts[tag]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

// parseTagList parses a tags string (comma-separated or JSON array) into a slice.
func parseTagList(tags string) []string {
	tags = strings.TrimSpace(tags)
	if tags == "" || tags == "[]" || tags == "null" {
		return nil
	}
	if strings.HasPrefix(tags, "[") {
		tags = strings.Trim(tags, "[]")
		tags = strings.ReplaceAll(tags, `"`, "")
	}
	var result []string
	for t := range strings.SplitSeq(tags, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}
