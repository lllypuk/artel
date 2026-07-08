package projects

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/config"
)

func TestNewRegistryValidProjects(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	proj1Dir := filepath.Join(tmp, "proj1")
	proj2Dir := filepath.Join(tmp, "proj2")
	os.MkdirAll(proj1Dir, 0o755)
	os.MkdirAll(proj2Dir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "agent-self", Path: agentDir},
		{Name: "proj1", Path: proj1Dir, Model: "custom-model", TimeoutSec: 600},
		{Name: "proj2", Path: proj2Dir},
	}

	r := NewRegistry(cfgProjects, agentDir, nil)

	if len(r.Names()) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(r.Names()))
	}

	// Stable order from config.
	names := r.Names()
	if names[0] != "agent-self" || names[1] != "proj1" || names[2] != "proj2" {
		t.Errorf("unexpected order: %v", names)
	}

	// Self-project detection.
	p, err := r.Get("agent-self")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.SelfProject {
		t.Error("agent-self should be SelfProject")
	}

	// Non-self project.
	p1, err := r.Get("proj1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p1.SelfProject {
		t.Error("proj1 should not be SelfProject")
	}
	if p1.Model != "custom-model" {
		t.Errorf("proj1 Model: got %q, want 'custom-model'", p1.Model)
	}
	if p1.TimeoutSec != 600 {
		t.Errorf("proj1 TimeoutSec: got %d, want 600", p1.TimeoutSec)
	}
}

func TestNewRegistryRelativePath(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projDir := filepath.Join(tmp, "project")
	os.MkdirAll(projDir, 0o755)

	// Relative path "." should resolve to agentDir and be SelfProject.
	cfgProjects := []config.ProjectConfig{
		{Name: "self", Path: "."},
	}

	r := NewRegistry(cfgProjects, agentDir, nil)

	p, err := r.Get("self")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !p.SelfProject {
		t.Error("'.' should be detected as SelfProject")
	}
	if p.Path != agentDir {
		t.Errorf("'.' resolved to %q, want %q", p.Path, agentDir)
	}
}

func TestNewRegistryNonexistentDirExcluded(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	validDir := filepath.Join(tmp, "valid")
	os.MkdirAll(validDir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "valid", Path: validDir},
		{Name: "ghost", Path: filepath.Join(tmp, "does-not-exist")},
	}

	// Capture warnings via logger.
	var logBuf strings.Builder
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	r := NewRegistry(cfgProjects, agentDir, logger)

	if len(r.Names()) != 1 {
		t.Fatalf("expected 1 project (ghost excluded), got %d", len(r.Names()))
	}
	if r.Names()[0] != "valid" {
		t.Errorf("expected 'valid', got %q", r.Names()[0])
	}

	if _, err := r.Get("ghost"); err == nil {
		t.Error("expected error for excluded project 'ghost'")
	}

	if !strings.Contains(logBuf.String(), "not found") {
		t.Errorf("expected warning about missing directory, got: %s", logBuf.String())
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	r := NewRegistry(nil, agentDir, nil)

	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	if !strings.Contains(err.Error(), "available") {
		t.Errorf("error should list available projects, got: %v", err)
	}
}

func TestNewRegistryDuplicateNames(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	dupDir := filepath.Join(tmp, "dup")
	os.MkdirAll(dupDir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "dup", Path: agentDir},
		{Name: "dup", Path: dupDir},
	}

	// Config validation should catch duplicates, but Registry should handle
	// gracefully — last one wins (map semantics).
	r := NewRegistry(cfgProjects, agentDir, nil)

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 entries in order (map dedup), got %d", len(names))
	}
	// Get returns the last stored value.
	p, err := r.Get("dup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Path != dupDir {
		t.Errorf("expected last-wins path %q, got %q", dupDir, p.Path)
	}
}

func TestRegistryList(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projDir := filepath.Join(tmp, "proj")
	os.MkdirAll(projDir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "self", Path: agentDir},
		{Name: "proj", Path: projDir},
	}

	r := NewRegistry(cfgProjects, agentDir, nil)

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}
	if list[0].Name != "self" || list[1].Name != "proj" {
		t.Errorf("unexpected list order: %v", []string{list[0].Name, list[1].Name})
	}
}

func TestRegistryNamesStableOrder(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "c", Path: agentDir},
		{Name: "a", Path: agentDir},
		{Name: "b", Path: agentDir},
	}

	r := NewRegistry(cfgProjects, agentDir, nil)

	// Names() should preserve config order, not alphabetical.
	names := r.Names()
	expected := []string{"c", "a", "b"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("Names[%d]: got %q, want %q", i, names[i], want)
		}
	}
}

func TestRegistryNilLogger(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	cfgProjects := []config.ProjectConfig{
		{Name: "self", Path: agentDir},
		{Name: "missing", Path: filepath.Join(tmp, "nope")},
	}

	// Should not panic with nil logger.
	r := NewRegistry(cfgProjects, agentDir, nil)
	if len(r.Names()) != 1 {
		t.Errorf("expected 1 project, got %d", len(r.Names()))
	}
}
