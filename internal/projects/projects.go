package projects

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/lllypuk/agent-shatrov/internal/config"
)

// Project represents a single project that pi_coder can work on.
type Project struct {
	Name        string // identifier for the bot, e.g. "unit-control"
	Path        string // absolute, resolved path to project directory
	Model       string // optional: overrides pi_coder default model
	TimeoutSec  int    // optional: overrides pi_coder default timeout (0 = inherit)
	SelfProject bool   // auto-detected: true if path matches agent directory
}

// Registry holds the map of available projects.
type Registry struct {
	projects map[string]*Project
	order    []string // stable order from config
}

// NewRegistry creates a project registry from config, resolving paths and
// auto-detecting the self-project (the agent's own codebase).
// If piCoder.ProjectsDir is set, auto-discovers git repos in that directory.
// Explicit projects from piCoder.Projects override auto-discovered ones with the same name.
// Non-existent directories are logged as warnings and excluded from the registry.
func NewRegistry(piCoder config.PiCoderConfig, agentDir string, logger *slog.Logger) *Registry {
	absAgentDir, _ := filepath.Abs(agentDir)

	r := &Registry{
		projects: make(map[string]*Project),
	}

	// Build a set of excluded project names.
	excludeSet := make(map[string]bool)
	for _, name := range piCoder.ExcludeProjects {
		excludeSet[name] = true
	}

	// Track explicit project names — they override auto-discovered ones.
	explicitNames := make(map[string]bool)
	for _, cp := range piCoder.Projects {
		explicitNames[cp.Name] = true
	}

	// Phase 1: auto-discover git repos in ProjectsDir.
	if piCoder.ProjectsDir != "" {
		scanDir, err := filepath.Abs(piCoder.ProjectsDir)
		if err != nil {
			if logger != nil {
				logger.Warn("projects: failed to resolve projects_dir", "path", piCoder.ProjectsDir, "error", err)
			}
		} else {
			discoverProjects(r, scanDir, absAgentDir, excludeSet, explicitNames, logger)
		}
	}

	// Phase 2: add explicit projects (override auto-discovered with same name).
	for _, cp := range piCoder.Projects {
		// Resolve relative paths against agentDir.
		resolved := cp.Path
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(absAgentDir, resolved)
		}
		absPath, err := filepath.Abs(resolved)
		if err != nil {
			if logger != nil {
				logger.Warn("projects: failed to resolve path", "name", cp.Name, "path", cp.Path, "error", err)
			}
			continue
		}

		// Check directory exists.
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			if logger != nil {
				logger.Warn("projects: directory not found, excluding", "name", cp.Name, "path", absPath)
			}
			continue
		}

		selfProject := absPath == absAgentDir

		p := &Project{
			Name:        cp.Name,
			Path:        absPath,
			Model:       cp.Model,
			TimeoutSec:  cp.TimeoutSec,
			SelfProject: selfProject,
		}

		// Override auto-discovered entry if exists.
		if _, exists := r.projects[cp.Name]; !exists {
			r.order = append(r.order, cp.Name)
		}
		r.projects[cp.Name] = p
	}

	return r
}

// discoverProjects scans a directory for git repositories and adds them to the registry.
// Skips entries in excludeSet and names already claimed by explicit projects.
func discoverProjects(r *Registry, scanDir, absAgentDir string, excludeSet, explicitNames map[string]bool, logger *slog.Logger) {
	entries, err := os.ReadDir(scanDir)
	if err != nil {
		if logger != nil {
			logger.Warn("projects: failed to read projects_dir", "path", scanDir, "error", err)
		}
		return
	}

	for _, entry := range entries {
		// Use os.Stat to resolve symlinks — entry.IsDir() returns false for
		// symlinks pointing to directories.
		info, err := os.Stat(filepath.Join(scanDir, entry.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		name := entry.Name()
		if name == ".git" || strings.HasPrefix(name, ".") {
			continue
		}

		// Skip excluded projects.
		if excludeSet[name] {
			continue
		}

		// Skip if already claimed by explicit project.
		if explicitNames[name] {
			continue
		}

		absPath, err := filepath.Abs(filepath.Join(scanDir, name))
		if err != nil {
			if logger != nil {
				logger.Warn("projects: failed to resolve discovered path", "name", name, "error", err)
			}
			continue
		}

		// Check if it's a git repo (.git presence).
		gitDir := filepath.Join(absPath, ".git")
		if _, err := os.Stat(gitDir); err != nil {
			continue // not a git repo, skip
		}

		selfProject := absPath == absAgentDir

		p := &Project{
			Name:        name,
			Path:        absPath,
			SelfProject: selfProject,
		}

		r.projects[name] = p
		r.order = append(r.order, name)
	}
}

// Get returns the project with the given name, or an error listing available names.
func (r *Registry) Get(name string) (*Project, error) {
	p, ok := r.projects[name]
	if !ok {
		return nil, fmt.Errorf("unknown project %q, available: %v", name, r.order)
	}
	return p, nil
}

// Names returns project names in stable config order.
func (r *Registry) Names() []string {
	result := make([]string, len(r.order))
	copy(result, r.order)
	return result
}

// List returns all projects in stable config order.
func (r *Registry) List() []*Project {
	result := make([]*Project, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.projects[name])
	}
	return result
}
