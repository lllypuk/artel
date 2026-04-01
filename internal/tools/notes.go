package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shatrov/agent/internal/storage"
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
	Action  string `json:"action"`  // create, search, list
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

func (n *NotesTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"create", "search", "list"},
				"description": "Action to perform",
			},
			"title": map[string]interface{}{
				"type":        "string",
				"description": "Note title (for create)",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Note content (for create)",
			},
			"tags": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated tags (for create)",
			},
			"query": map[string]interface{}{
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
		id, err := n.db.CreateNote(a.ChatID, a.Title, a.Content, a.Tags)
		if err != nil {
			return "", fmt.Errorf("create note: %w", err)
		}
		return fmt.Sprintf("Note created (id: %d)", id), nil

	case "search":
		notes, err := n.db.SearchNotes(a.ChatID, a.Query)
		if err != nil {
			return "", fmt.Errorf("search notes: %w", err)
		}
		if len(notes) == 0 {
			return "No notes found.", nil
		}
		result := fmt.Sprintf("Found %d notes:\n", len(notes))
		for _, note := range notes {
			result += fmt.Sprintf("- [%d] %s (tags: %s)\n  %s\n", note.ID, note.Title, note.Tags, truncate(note.Content, 100))
		}
		return result, nil

	case "list":
		notes, err := n.db.SearchNotes(a.ChatID, "")
		if err != nil {
			return "", fmt.Errorf("list notes: %w", err)
		}
		if len(notes) == 0 {
			return "No notes yet.", nil
		}
		result := fmt.Sprintf("%d notes:\n", len(notes))
		for _, note := range notes {
			result += fmt.Sprintf("- [%d] %s (tags: %s) %s\n", note.ID, note.Title, note.Tags, note.CreatedAt.Format("2006-01-02"))
		}
		return result, nil

	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
