package projects

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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
// Non-existent directories are logged as warnings and excluded from the registry.
func NewRegistry(configProjects []config.ProjectConfig, agentDir string, logger *slog.Logger) *Registry {
	absAgentDir, _ := filepath.Abs(agentDir)

	r := &Registry{
		projects: make(map[string]*Project),
	}

	for _, cp := range configProjects {
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

		r.projects[cp.Name] = p
		r.order = append(r.order, cp.Name)
	}

	return r
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
