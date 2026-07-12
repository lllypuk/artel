package tools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lllypuk/artel/internal/tools"
)

func newFilesTool(t *testing.T) (ft *tools.FilesTool, dir string) {
	t.Helper()
	dir = t.TempDir()
	ft = tools.NewFilesTool(dir)
	return ft, dir
}

func filesExec(t *testing.T, ft *tools.FilesTool, args map[string]any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return ft.Execute(context.Background(), raw)
}

// ── Metadata ─────────────────────────────────────────────────────────────────

func TestFilesToolName(t *testing.T) {
	ft, _ := newFilesTool(t)
	assert.Equal(t, "files", ft.Name())
}

func TestFilesToolDescription(t *testing.T) {
	ft, _ := newFilesTool(t)
	assert.NotEmpty(t, ft.Description())
}

func TestFilesToolParameters(t *testing.T) {
	ft, _ := newFilesTool(t)
	params := ft.Parameters()
	assert.Equal(t, "object", params["type"])
	props, _ := params["properties"].(map[string]any)
	assert.Contains(t, props, "action")
	assert.Contains(t, props, "filename")
}

func TestFilesToolStorageDir(t *testing.T) {
	ft, dir := newFilesTool(t)
	assert.Equal(t, dir, ft.StorageDir())
}

// ── List ─────────────────────────────────────────────────────────────────────

func TestFilesListEmpty(t *testing.T) {
	ft, _ := newFilesTool(t)
	result, err := filesExec(t, ft, map[string]any{"action": "list"})
	require.NoError(t, err)
	assert.Contains(t, result, "No files")
}

func TestFilesListAfterSave(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, _, err := ft.SaveFile("hello.txt", []byte("content"))
	require.NoError(t, err)

	result, err := filesExec(t, ft, map[string]any{"action": "list"})
	require.NoError(t, err)
	assert.Contains(t, result, "hello.txt")
	assert.Contains(t, result, "1 file(s)")
}

// ── SaveFile ─────────────────────────────────────────────────────────────────

func TestFilesToolSaveFile(t *testing.T) {
	ft, dir := newFilesTool(t)
	filePath, filename, err := ft.SaveFile("report.txt", []byte("hello world"))
	require.NoError(t, err)
	assert.NotEmpty(t, filename)
	assert.True(t, strings.HasSuffix(filename, "_report.txt"), "filename=%s", filename)
	assert.Equal(t, filepath.Join(dir, filename), filePath)

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

func TestFilesToolSaveSanitizesName(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, filename, err := ft.SaveFile("../../etc/passwd", []byte("x"))
	require.NoError(t, err)
	assert.NotContains(t, filename, "/")
	assert.NotContains(t, filename, "..")
}

// ── Read ─────────────────────────────────────────────────────────────────────

func TestFilesReadText(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, saved, err := ft.SaveFile("note.txt", []byte("hello from file"))
	require.NoError(t, err)

	result, err := filesExec(t, ft, map[string]any{"action": "read", "filename": saved})
	require.NoError(t, err)
	assert.Contains(t, result, "hello from file")
}

func TestFilesReadBinary(t *testing.T) {
	ft, _ := newFilesTool(t)
	// JPEG magic bytes
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	_, saved, err := ft.SaveFile("photo.jpg", jpegData)
	require.NoError(t, err)

	result, err := filesExec(t, ft, map[string]any{"action": "read", "filename": saved})
	require.NoError(t, err)
	assert.Contains(t, result, "Binary file")
}

func TestFilesReadNotFound(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, err := filesExec(t, ft, map[string]any{"action": "read", "filename": "nonexistent.txt"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFilesReadMissingFilename(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, err := filesExec(t, ft, map[string]any{"action": "read"})
	assert.Error(t, err)
}

// ── Delete ───────────────────────────────────────────────────────────────────

func TestFilesDelete(t *testing.T) {
	ft, dir := newFilesTool(t)
	_, saved, err := ft.SaveFile("tmp.txt", []byte("x"))
	require.NoError(t, err)

	result, err := filesExec(t, ft, map[string]any{"action": "delete", "filename": saved})
	require.NoError(t, err)
	assert.Contains(t, result, "deleted")

	_, statErr := os.Stat(filepath.Join(dir, saved))
	assert.True(t, os.IsNotExist(statErr), "file should be removed from disk")
}

func TestFilesDeleteNotFound(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, err := filesExec(t, ft, map[string]any{"action": "delete", "filename": "ghost.txt"})
	assert.Error(t, err)
}

func TestFilesDeleteMissingFilename(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, err := filesExec(t, ft, map[string]any{"action": "delete"})
	assert.Error(t, err)
}

// ── Unknown action ───────────────────────────────────────────────────────────

func TestFilesUnknownAction(t *testing.T) {
	ft, _ := newFilesTool(t)
	_, err := filesExec(t, ft, map[string]any{"action": "upload"})
	assert.Error(t, err)
}

// ── IsTextFile ────────────────────────────────────────────────────────────────

func TestIsTextFile(t *testing.T) {
	cases := map[string]bool{
		"readme.md":   true,
		"data.json":   true,
		"notes.txt":   true,
		"table.csv":   true,
		"image.jpg":   false,
		"photo.png":   false,
		"archive.zip": false,
		"doc.pdf":     false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, tools.IsTextFile(name))
		})
	}
}

// ── Path traversal protection ─────────────────────────────────────────────────

func TestFilesReadPathTraversal(t *testing.T) {
	ft, _ := newFilesTool(t)
	// Attempt path traversal — should fail with "not found", not expose arbitrary files.
	_, err := filesExec(t, ft, map[string]any{"action": "read", "filename": "../../etc/passwd"})
	assert.Error(t, err)
}
