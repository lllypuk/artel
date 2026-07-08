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

	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)

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

	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)

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

	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, logger)

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

	r := NewRegistry(config.PiCoderConfig{}, agentDir, nil)

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
	// gracefully — last one wins (map semantics), no duplicate in order.
	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)

	names := r.Names()
	if len(names) != 1 {
		t.Fatalf("expected 1 entry in order (dedup), got %d", len(names))
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

	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)

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

	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)

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
	r := NewRegistry(config.PiCoderConfig{Projects: cfgProjects}, agentDir, nil)
	if len(r.Names()) != 1 {
		t.Errorf("expected 1 project, got %d", len(r.Names()))
	}
}

// makeGitRepo creates a directory with a .git subdirectory to simulate a git repo.
func makeGitRepo(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return dir
}

func TestAutoDiscoveryFindsGitRepos(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projectsDir := filepath.Join(tmp, "projects")
	os.MkdirAll(projectsDir, 0o755)
	makeGitRepo(t, projectsDir, "repo1")
	makeGitRepo(t, projectsDir, "repo2")
	makeGitRepo(t, projectsDir, "repo3")

	// Non-git dir should be skipped.
	_nonGit := filepath.Join(projectsDir, "not-a-repo")
	os.MkdirAll(_nonGit, 0o755)

	cfg := config.PiCoderConfig{ProjectsDir: projectsDir}
	r := NewRegistry(cfg, agentDir, nil)

	names := r.Names()
	if len(names) != 3 {
		t.Fatalf("expected 3 auto-discovered repos, got %d: %v", len(names), names)
	}

	for _, name := range names {
		p, err := r.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		if p.Name != name {
			t.Errorf("expected name %q, got %q", name, p.Name)
		}
		expectedPath, _ := filepath.Abs(filepath.Join(projectsDir, name))
		if p.Path != expectedPath {
			t.Errorf("path: got %q, want %q", p.Path, expectedPath)
		}
		if p.SelfProject {
			t.Errorf("%q should not be SelfProject", name)
		}
	}
}

func TestAutoDiscoveryExcludeProjects(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projectsDir := filepath.Join(tmp, "projects")
	os.MkdirAll(projectsDir, 0o755)
	makeGitRepo(t, projectsDir, "repo1")
	makeGitRepo(t, projectsDir, "repo2")
	makeGitRepo(t, projectsDir, "repo3")

	cfg := config.PiCoderConfig{
		ProjectsDir:     projectsDir,
		ExcludeProjects: []string{"repo2"},
	}
	r := NewRegistry(cfg, agentDir, nil)

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 repos after exclude, got %d: %v", len(names), names)
	}

	hasRepo2 := false
	for _, n := range names {
		if n == "repo2" {
			hasRepo2 = true
		}
	}
	if hasRepo2 {
		t.Error("repo2 should be excluded")
	}
}

func TestAutoDiscoveryMergedWithExplicit(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projectsDir := filepath.Join(tmp, "projects")
	os.MkdirAll(projectsDir, 0o755)
	makeGitRepo(t, projectsDir, "repo1")
	makeGitRepo(t, projectsDir, "repo2")
	makeGitRepo(t, projectsDir, "repo3")

	// Explicit project with same name as a discovered one should override it.
	explicitDir := filepath.Join(tmp, "explicit-repo1")
	os.MkdirAll(explicitDir, 0o755)

	cfg := config.PiCoderConfig{
		ProjectsDir: projectsDir,
		Projects: []config.ProjectConfig{
			{Name: "repo1", Path: explicitDir, Model: "custom-model"},
			{Name: "explicit-extra", Path: agentDir},
		},
	}
	r := NewRegistry(cfg, agentDir, nil)

	names := r.Names()
	// Expected: repo2, repo3 (auto), repo1 (explicit override), explicit-extra
	if len(names) != 4 {
		t.Fatalf("expected 4 projects (2 auto + 1 overridden + 1 explicit), got %d: %v", len(names), names)
	}

	// repo1 should use the explicit path and model.
	p, err := r.Get("repo1")
	if err != nil {
		t.Fatalf("Get(repo1): %v", err)
	}
	if p.Path != explicitDir {
		t.Errorf("repo1 path: got %q, want %q (explicit should override)", p.Path, explicitDir)
	}
	if p.Model != "custom-model" {
		t.Errorf("repo1 model: got %q, want 'custom-model'", p.Model)
	}

	// repo2 and repo3 should be auto-discovered.
	for _, name := range []string{"repo2", "repo3"} {
		p, err := r.Get(name)
		if err != nil {
			t.Fatalf("Get(%q): %v", name, err)
		}
		expectedPath, _ := filepath.Abs(filepath.Join(projectsDir, name))
		if p.Path != expectedPath {
			t.Errorf("%s path: got %q, want %q", name, p.Path, expectedPath)
		}
	}

	// explicit-extra should exist.
	p, err = r.Get("explicit-extra")
	if err != nil {
		t.Fatalf("Get(explicit-extra): %v", err)
	}
	if !p.SelfProject {
		t.Error("explicit-extra should be SelfProject (path == agentDir)")
	}
}

func TestAutoDiscoveryNoGitDir(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	projectsDir := filepath.Join(tmp, "projects")
	os.MkdirAll(projectsDir, 0o755)

	// Plain directories without .git should be skipped.
	os.MkdirAll(filepath.Join(projectsDir, "plain-dir"), 0o755)

	cfg := config.PiCoderConfig{ProjectsDir: projectsDir}
	r := NewRegistry(cfg, agentDir, nil)

	if len(r.Names()) != 0 {
		t.Fatalf("expected 0 projects (no git repos), got %d: %v", len(r.Names()), r.Names())
	}
}

func TestAutoDiscoveryNonexistentDir(t *testing.T) {
	tmp := t.TempDir()
	agentDir := filepath.Join(tmp, "agent")
	os.MkdirAll(agentDir, 0o755)

	cfg := config.PiCoderConfig{ProjectsDir: filepath.Join(tmp, "does-not-exist")}
	r := NewRegistry(cfg, agentDir, nil)

	if len(r.Names()) != 0 {
		t.Fatalf("expected 0 projects for nonexistent dir, got %d", len(r.Names()))
	}
}
