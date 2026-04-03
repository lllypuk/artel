package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// maxTextReadBytes is the maximum number of bytes returned by the read action.
const maxTextReadBytes = 50 * 1024 // 50 KB

// textExtensions is the set of file extensions treated as plain text.
var textExtensions = map[string]bool{
	"txt":  true,
	"md":   true,
	"json": true,
	"csv":  true,
	"yaml": true,
	"yml":  true,
	"xml":  true,
	"html": true,
	"htm":  true,
	"log":  true,
	"py":   true,
	"go":   true,
	"js":   true,
	"ts":   true,
	"sh":   true,
	"toml": true,
	"ini":  true,
	"conf": true,
	"cfg":  true,
}

// IsTextFile reports whether the file name has a known plain-text extension.
func IsTextFile(filename string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	return textExtensions[ext]
}

// FilesTool provides list / read / delete operations over a local file store.
type FilesTool struct {
	storageDir string
}

// NewFilesTool creates a new FilesTool backed by storageDir.
func NewFilesTool(storageDir string) *FilesTool {
	return &FilesTool{storageDir: storageDir}
}

// StorageDir returns the storage directory used by this tool.
func (f *FilesTool) StorageDir() string { return f.storageDir }

func (f *FilesTool) Name() string { return "files" }

func (f *FilesTool) Description() string {
	return "Manage files uploaded by the user. " +
		"Actions: " +
		"list (list all saved files with name, size, and date); " +
		"read (return the content of a text file, or metadata for binary files); " +
		"delete (permanently delete a file by name)."
}

func (f *FilesTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"list", "read", "delete"},
				"description": "Action to perform",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "File name (required for read and delete)",
			},
		},
		"required": []string{"action"},
	}
}

type filesArgs struct {
	Action   string `json:"action"`
	Filename string `json:"filename"`
}

// Execute dispatches the requested action.
func (f *FilesTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var a filesArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	switch a.Action {
	case "list":
		return f.list()
	case "read":
		return f.readFile(a.Filename)
	case "delete":
		return f.deleteFile(a.Filename)
	default:
		return "", fmt.Errorf("unknown action: %s", a.Action)
	}
}

// SaveFile writes data to the storage directory with a timestamp-prefixed name
// to avoid collisions. It returns the full path and the saved file name.
func (f *FilesTool) SaveFile(originalName string, data []byte) (filePath, filename string, err error) {
	if mkErr := os.MkdirAll(f.storageDir, 0o750); mkErr != nil {
		return "", "", fmt.Errorf("create storage dir: %w", mkErr)
	}
	clean := sanitizeFilename(originalName)
	timestamp := time.Now().Format("20060102_150405")
	filename = timestamp + "_" + clean
	filePath = filepath.Join(f.storageDir, filename)
	if wErr := os.WriteFile(filePath, data, 0o600); wErr != nil {
		return "", "", fmt.Errorf("write file: %w", wErr)
	}
	return filePath, filename, nil
}

// ── private helpers ──────────────────────────────────────────────────────────

func (f *FilesTool) list() (string, error) {
	entries, err := os.ReadDir(f.storageDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "No files saved yet.", nil
		}
		return "", fmt.Errorf("read dir: %w", err)
	}

	type fileEntry struct {
		name    string
		size    int64
		modTime time.Time
	}
	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{e.Name(), info.Size(), info.ModTime()})
	}
	if len(files) == 0 {
		return "No files saved yet.", nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d file(s) in storage:\n", len(files))
	for _, fi := range files {
		fmt.Fprintf(&sb, "- %s (%s, %s)\n", fi.name, formatFileSize(fi.size), fi.modTime.Format("2006-01-02 15:04"))
	}
	return sb.String(), nil
}

func (f *FilesTool) readFile(filename string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required for read")
	}
	clean := filepath.Base(filename)
	path := filepath.Join(f.storageDir, clean)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", clean)
		}
		return "", fmt.Errorf("read file: %w", err)
	}

	// Treat as binary if extension hints at it or content is not valid UTF-8.
	if !utf8.Valid(data) || !IsTextFile(clean) {
		info, _ := os.Stat(path)
		return fmt.Sprintf("Binary file: %s (%s)", clean, formatFileSize(info.Size())), nil
	}

	if len(data) > maxTextReadBytes {
		return fmt.Sprintf("File: %s\n(first %d bytes shown out of %d total)\n\n%s",
			clean, maxTextReadBytes, len(data), string(data[:maxTextReadBytes])), nil
	}
	return fmt.Sprintf("File: %s\n\n%s", clean, string(data)), nil
}

func (f *FilesTool) deleteFile(filename string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required for delete")
	}
	clean := filepath.Base(filename)
	path := filepath.Join(f.storageDir, clean)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", clean)
		}
		return "", fmt.Errorf("delete file: %w", err)
	}
	return fmt.Sprintf("File %s deleted.", clean), nil
}

// sanitizeFilename strips path separators and other dangerous characters.
func sanitizeFilename(name string) string {
	base := filepath.Base(name)
	var sb strings.Builder
	for _, r := range base {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', '\x00':
			sb.WriteRune('_')
		default:
			sb.WriteRune(r)
		}
	}
	s := strings.TrimSpace(sb.String())
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	return s
}

// formatFileSize returns a human-readable representation of a byte count.
func formatFileSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
