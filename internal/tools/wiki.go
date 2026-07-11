package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/lllypuk/artel/internal/storage"
)

// WikiTool manages personal wiki pages.
type WikiTool struct {
	db *storage.DB
}

// NewWikiTool creates a new wiki tool.
func NewWikiTool(db *storage.DB) *WikiTool {
	return &WikiTool{db: db}
}

func (w *WikiTool) Name() string { return "wiki" }

func (w *WikiTool) Description() string {
	return "Manage personal wiki pages with a path hierarchy (e.g. /dev/golang, /health/running). " +
		"Actions: " +
		"create (create a page at path with title/content/tags), " +
		"get (get full page content by path), " +
		"edit (update title/content/tags of a page by path), " +
		"delete (delete a page by path), " +
		"list (list pages in a section, e.g. /dev), " +
		"tree (show full wiki structure as a tree), " +
		"rename (rename/move a page, cascades all [[links]]), " +
		"backlinks (show which pages link to a given path). " +
		"Supports [[path/to/page]] link syntax — links are extracted automatically."
}

func (w *WikiTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"create", "get", "edit", "delete", "list", "tree", "rename", "backlinks"},
				"description": "Action to perform",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Page path (e.g. /dev/golang). Required for create, get, edit, delete, list, rename, backlinks.",
			},
			"new_path": map[string]any{
				"type":        "string",
				"description": "New path for rename action.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Page title (for create, edit).",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Page content in markdown (for create, edit). Use [[path/to/page]] for internal links.",
			},
			"tags": map[string]any{
				"type":        "string",
				"description": "Comma-separated tags (for create, edit).",
			},
		},
		"required": []string{"action"},
	}
}

type wikiArgs struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	NewPath string `json:"new_path"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Tags    string `json:"tags"`
}

// wikiLinkRE matches [[path/to/page]] syntax.
var wikiLinkRE = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

func extractWikiLinks(content string) []string {
	matches := wikiLinkRE.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var links []string
	for _, m := range matches {
		p := strings.TrimSpace(m[1])
		if p != "" && !seen[p] {
			seen[p] = true
			links = append(links, p)
		}
	}
	return links
}

func (w *WikiTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a wikiArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	chatID, _ := ctx.Value(ChatIDKey).(int64)

	switch a.Action {
	case "create":
		return w.execCreate(ctx, chatID, a)
	case "get":
		return w.execGet(ctx, chatID, a)
	case "edit":
		return w.execEdit(ctx, chatID, a)
	case "delete":
		return w.execDelete(ctx, chatID, a)
	case "list":
		return w.execList(ctx, chatID, a)
	case "tree":
		return w.execTree(ctx, chatID)
	case "rename":
		return w.execRename(ctx, chatID, a)
	case "backlinks":
		return w.execBacklinks(ctx, chatID, a)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

func (w *WikiTool) execCreate(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for create")
	}
	if a.Title == "" {
		return "", fmt.Errorf("title is required for create")
	}
	id, err := w.db.CreateWikiPage(ctx, chatID, a.Path, a.Title, a.Content, a.Tags)
	if err != nil {
		return "", fmt.Errorf("create wiki page: %w", err)
	}
	if err := w.db.UpsertWikiLinks(ctx, chatID, a.Path, extractWikiLinks(a.Content)); err != nil {
		return "", fmt.Errorf("update wiki links: %w", err)
	}
	return fmt.Sprintf("Wiki page created: %s (id: %d)", a.Path, id), nil
}

func (w *WikiTool) execGet(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for get")
	}
	page, err := w.db.GetWikiPage(ctx, chatID, a.Path)
	if err != nil {
		return "", fmt.Errorf("get wiki page: %w", err)
	}
	if page == nil {
		return fmt.Sprintf("Page not found: %s", a.Path), nil
	}
	return formatWikiPage(page), nil
}

func formatWikiPage(p *storage.WikiPage) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", p.Title)
	fmt.Fprintf(&sb, "Path: %s | Tags: %s\n", p.Path, p.Tags)
	fmt.Fprintf(&sb, "Created: %s | Updated: %s\n\n",
		p.CreatedAt.Format("2006-01-02 15:04"),
		p.UpdatedAt.Format("2006-01-02 15:04"))
	sb.WriteString(p.Content)
	return sb.String()
}

func (w *WikiTool) execEdit(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for edit")
	}
	existing, err := w.db.GetWikiPage(ctx, chatID, a.Path)
	if err != nil {
		return "", fmt.Errorf("get wiki page: %w", err)
	}
	if existing == nil {
		return fmt.Sprintf("Page not found: %s", a.Path), nil
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
	if err := w.db.UpdateWikiPage(ctx, chatID, a.Path, title, content, tags); err != nil {
		return "", fmt.Errorf("update wiki page: %w", err)
	}
	if err := w.db.UpsertWikiLinks(ctx, chatID, a.Path, extractWikiLinks(content)); err != nil {
		return "", fmt.Errorf("update wiki links: %w", err)
	}
	return fmt.Sprintf("Wiki page updated: %s", a.Path), nil
}

func (w *WikiTool) execDelete(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for delete")
	}
	if err := w.db.DeleteWikiPage(ctx, chatID, a.Path); err != nil {
		return "", fmt.Errorf("delete wiki page: %w", err)
	}
	return fmt.Sprintf("Wiki page deleted: %s", a.Path), nil
}

func (w *WikiTool) execList(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	pages, err := w.db.ListWikiPages(ctx, chatID, a.Path)
	if err != nil {
		return "", fmt.Errorf("list wiki pages: %w", err)
	}
	if len(pages) == 0 {
		if a.Path != "" {
			return fmt.Sprintf("No pages found in section: %s", a.Path), nil
		}
		return "Wiki is empty.", nil
	}
	var sb strings.Builder
	section := a.Path
	if section == "" {
		section = "/"
	}
	fmt.Fprintf(&sb, "Pages in %s (%d):\n", section, len(pages))
	for _, p := range pages {
		fmt.Fprintf(&sb, "  %s — %s", p.Path, p.Title)
		if p.Tags != "" {
			fmt.Fprintf(&sb, " [%s]", p.Tags)
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func (w *WikiTool) execTree(ctx context.Context, chatID int64) (string, error) {
	pages, err := w.db.AllWikiPages(ctx, chatID)
	if err != nil {
		return "", fmt.Errorf("get all wiki pages: %w", err)
	}
	if len(pages) == 0 {
		return "Wiki is empty.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Wiki tree (%d pages):\n", len(pages))
	for _, p := range pages {
		depth := max(strings.Count(p.Path, "/")-1, 0)
		indent := strings.Repeat("  ", depth)
		name := p.Path[strings.LastIndex(p.Path, "/")+1:]
		fmt.Fprintf(&sb, "%s- %s — %s\n", indent, name, p.Title)
	}
	return sb.String(), nil
}

func (w *WikiTool) execRename(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for rename")
	}
	if a.NewPath == "" {
		return "", fmt.Errorf("new_path is required for rename")
	}
	if err := w.db.RenameWikiPage(ctx, chatID, a.Path, a.NewPath); err != nil {
		return "", fmt.Errorf("rename wiki page: %w", err)
	}
	return fmt.Sprintf("Wiki page renamed: %s → %s (all links updated)", a.Path, a.NewPath), nil
}

func (w *WikiTool) execBacklinks(ctx context.Context, chatID int64, a wikiArgs) (string, error) {
	if a.Path == "" {
		return "", fmt.Errorf("path is required for backlinks")
	}
	pages, err := w.db.GetBacklinks(ctx, chatID, a.Path)
	if err != nil {
		return "", fmt.Errorf("get backlinks: %w", err)
	}
	if len(pages) == 0 {
		return fmt.Sprintf("No pages link to: %s", a.Path), nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Pages linking to %s (%d):\n", a.Path, len(pages))
	for _, p := range pages {
		fmt.Fprintf(&sb, "  %s — %s\n", p.Path, p.Title)
	}
	return sb.String(), nil
}
