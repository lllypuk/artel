package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ShellTool executes whitelisted shell commands safely.
type ShellTool struct {
	allowedCommands map[string]bool
	blockedPatterns []string
	workingDir      string
	timeout         time.Duration
}

// ShellConfig holds shell tool configuration.
type ShellConfig struct {
	AllowedCommands []string
	BlockedPatterns []string
	WorkingDir      string
	TimeoutSeconds  int
}

// NewShellTool creates a new shell tool with safety restrictions.
func NewShellTool(cfg ShellConfig) *ShellTool {
	allowed := make(map[string]bool)
	for _, cmd := range cfg.AllowedCommands {
		allowed[cmd] = true
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &ShellTool{
		allowedCommands: allowed,
		blockedPatterns: cfg.BlockedPatterns,
		workingDir:      cfg.WorkingDir,
		timeout:         timeout,
	}
}

type shellArgs struct {
	Command string `json:"command"`
}

func (s *ShellTool) Name() string        { return "shell" }
func (s *ShellTool) Description() string {
	return "Execute a shell command on the local machine. Only whitelisted commands are allowed. Dangerous patterns are blocked."
}

func (s *ShellTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "Shell command to execute",
			},
		},
		"required": []string{"command"},
	}
}

func (s *ShellTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a shellArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	if err := s.validate(a.Command); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", a.Command)
	if s.workingDir != "" {
		cmd.Dir = s.workingDir
	}

	output, err := cmd.CombinedOutput()
	result := string(output)

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("command timed out after %s", s.timeout)
	}
	if err != nil {
		return result, fmt.Errorf("command failed: %w\nOutput: %s", err, result)
	}

	// Truncate large output
	const maxLen = 4096
	if len(result) > maxLen {
		result = result[:maxLen] + "\n... (truncated)"
	}

	return result, nil
}

func (s *ShellTool) validate(command string) error {
	// Check blocked patterns first
	lower := strings.ToLower(command)
	for _, pattern := range s.blockedPatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return fmt.Errorf("blocked: command contains dangerous pattern %q", pattern)
		}
	}

	// Check pipes and command chaining — validate each part
	// Split by pipes and semicolons to check each command
	parts := strings.FieldsFunc(command, func(r rune) bool {
		return r == '|' || r == ';' || r == '&'
	})

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		baseCmd := fields[0]

		// Handle sudo — block it
		if baseCmd == "sudo" {
			return fmt.Errorf("blocked: sudo is not allowed")
		}

		if !s.allowedCommands[baseCmd] {
			return fmt.Errorf("blocked: command %q is not in the whitelist", baseCmd)
		}
	}

	// Block output redirection to prevent file writes
	if strings.ContainsAny(command, ">") {
		return fmt.Errorf("blocked: output redirection is not allowed")
	}

	return nil
}
