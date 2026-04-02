package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// NotesTool manages personal notes.
type NotesTool struct {
	db *storage.DB
}

// NewNotesTool creates a new notes tool.
func NewNotesTool(db *storage.DB) *NotesTool {
	return &NotesTool{db: db}
}

type notesArgs struct {
	Action  string `json:"action"` // create, search, list, get, edit, delete
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Tags    string `json:"tags"`
	Query   string `json:"query"`
	ChatID  int64  `json:"chat_id"`
}

func (n *NotesTool) Name() string { return "notes" }

// DB returns the underlying storage.DB (used in tests).
func (n *NotesTool) DB() *storage.DB { return n.db }
func (n *NotesTool) Description() string {
	return "Manage personal notes. Actions: " +
		"create (create a new note with title/content/tags), " +
		"search (full-text search notes by query — uses FTS5 ranked by relevance when available), " +
		"list (list all notes, most recent first), " +
		"get (get full note content by id), " +
		"edit (update title/content/tags of a note by id), " +
		"delete (delete a note by id)."
}

func (n *NotesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "search", "list", "get", "edit", "delete"},
				"description": "Action to perform",
			},
			"id": map[string]any{
				"type":        "integer",
				"description": "Note ID (for get, edit, delete)",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Note title (for create, edit)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Note content (for create, edit)",
			},
			"tags": map[string]any{
				"type":        "string",
				"description": "Comma-separated tags (for create, edit)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search) — supports multiple words, all must match",
			},
		},
		"required": []string{"action"},
	}
}

func (n *NotesTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a notesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	switch a.Action {
	case "create":
		return n.executeCreate(ctx, a)
	case "search":
		return n.executeSearch(ctx, a)
	case "list":
		return n.executeList(ctx, a)
	case "get":
		return n.executeGet(ctx, a)
	case "edit":
		return n.executeEdit(ctx, a)
	case "delete":
		return n.executeDelete(ctx, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (n *NotesTool) executeCreate(ctx context.Context, a notesArgs) (string, error) {
	if a.Title == "" {
		return "", fmt.Errorf("title is required for create")
	}
	id, err := n.db.CreateNote(ctx, a.ChatID, a.Title, a.Content, a.Tags)
	if err != nil {
		return "", fmt.Errorf("create note: %w", err)
	}
	return fmt.Sprintf("Note created (id: %d)", id), nil
}

func (n *NotesTool) executeSearch(ctx context.Context, a notesArgs) (string, error) {
	if a.Query == "" {
		return "", fmt.Errorf("query is required for search")
	}
	notes, err := n.db.SearchNotes(ctx, a.ChatID, a.Query)
	if err != nil {
		return "", fmt.Errorf("search notes: %w", err)
	}
	if len(notes) == 0 {
		return "No notes found.", nil
	}
	var result strings.Builder
	fmt.Fprintf(&result, "Found %d note(s):\n", len(notes))
	for _, note := range notes {
		fmt.Fprintf(&result, "- [%d] %s (tags: %s)\n  %s\n",
			note.ID, note.Title, note.Tags, truncate(note.Content, 150))
	}
	return result.String(), nil
}

func (n *NotesTool) executeList(ctx context.Context, a notesArgs) (string, error) {
	notes, err := n.db.SearchNotes(ctx, a.ChatID, "")
	if err != nil {
		return "", fmt.Errorf("list notes: %w", err)
	}
	if len(notes) == 0 {
		return "No notes yet.", nil
	}
	var result strings.Builder
	fmt.Fprintf(&result, "%d note(s):\n", len(notes))
	for _, note := range notes {
		fmt.Fprintf(&result, "- [%d] %s (tags: %s) %s\n",
			note.ID, note.Title, note.Tags, note.CreatedAt.Format("2006-01-02"))
	}
	return result.String(), nil
}

func (n *NotesTool) executeGet(ctx context.Context, a notesArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for get")
	}
	note, err := n.db.GetNote(ctx, a.ID, a.ChatID)
	if err != nil {
		return "", fmt.Errorf("get note: %w", err)
	}
	return fmt.Sprintf("[%d] %s\nTags: %s\nCreated: %s | Updated: %s\n\n%s",
		note.ID, note.Title, note.Tags,
		note.CreatedAt.Format("2006-01-02 15:04"),
		note.UpdatedAt.Format("2006-01-02 15:04"),
		note.Content,
	), nil
}

func (n *NotesTool) executeEdit(ctx context.Context, a notesArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for edit")
	}
	// Load existing note to preserve fields not provided.
	existing, err := n.db.GetNote(ctx, a.ID, a.ChatID)
	if err != nil {
		return "", fmt.Errorf("get note for edit: %w", err)
	}
	title, content, tags := existing.Title, existing.Content, existing.Tags
	if a.Title != "" {
		title = a.Title
	}
	if a.Content != "" {
		content = a.Content
	}
	if a.Tags != "" {
		tags = a.Tags
	}
	if err := n.db.UpdateNote(ctx, a.ID, a.ChatID, title, content, tags); err != nil {
		return "", fmt.Errorf("edit note: %w", err)
	}
	return fmt.Sprintf("Note %d updated.", a.ID), nil
}

func (n *NotesTool) executeDelete(ctx context.Context, a notesArgs) (string, error) {
	if a.ID == 0 {
		return "", fmt.Errorf("id is required for delete")
	}
	if err := n.db.DeleteNote(ctx, a.ID, a.ChatID); err != nil {
		return "", fmt.Errorf("delete note: %w", err)
	}
	return fmt.Sprintf("Note %d deleted.", a.ID), nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
