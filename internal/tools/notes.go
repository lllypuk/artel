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
	Action  string `json:"action"` // create, search, list
	Title   string `json:"title"`
	Content string `json:"content"`
	Tags    string `json:"tags"`
	Query   string `json:"query"`
	ChatID  int64  `json:"chat_id"`
}

func (n *NotesTool) Name() string { return "notes" }
func (n *NotesTool) Description() string {
	return "Manage personal notes. Actions: create (create a new note), search (search notes by query), list (list recent notes)."
}

func (n *NotesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "search", "list"},
				"description": "Action to perform",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Note title (for create)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Note content (for create)",
			},
			"tags": map[string]any{
				"type":        "string",
				"description": "Comma-separated tags (for create)",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Search query (for search)",
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
		id, err := n.db.CreateNote(ctx, a.ChatID, a.Title, a.Content, a.Tags)
		if err != nil {
			return "", fmt.Errorf("create note: %w", err)
		}
		return fmt.Sprintf("Note created (id: %d)", id), nil

	case "search":
		notes, err := n.db.SearchNotes(ctx, a.ChatID, a.Query)
		if err != nil {
			return "", fmt.Errorf("search notes: %w", err)
		}
		if len(notes) == 0 {
			return "No notes found.", nil
		}
		var result strings.Builder
		fmt.Fprintf(&result, "Found %d notes:\n", len(notes))
		for _, note := range notes {
			fmt.Fprintf(&result, "- [%d] %s (tags: %s)\n  %s\n", note.ID, note.Title, note.Tags, truncate(note.Content, 100))
		}
		return result.String(), nil

	case "list":
		notes, err := n.db.SearchNotes(ctx, a.ChatID, "")
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		if len(notes) == 0 {
			return "No notes yet.", nil
		}
		var result strings.Builder
		fmt.Fprintf(&result, "%d notes:\n", len(notes))
		for _, note := range notes {
			fmt.Fprintf(&result, "- [%d] %s (tags: %s) %s\n", note.ID, note.Title, note.Tags, note.CreatedAt.Format("2006-01-02"))
		}
		return result.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
