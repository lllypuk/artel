package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxCodeRunnerOutputLen = 2048

// allSupportedLangs is the canonical list of supported programming languages.
var allSupportedLangs = []string{"python", "go", "bash"}

// CodeRunnerTool executes code snippets in isolated temporary directories.
type CodeRunnerTool struct {
	timeout      time.Duration
	memLimitMB   int
	enabledLangs map[string]bool
}

// CodeRunnerConfig holds configuration for the code runner tool.
type CodeRunnerConfig struct {
	TimeoutSeconds int
	MemLimitMB     int      // 0 = no limit; applies ulimit -v (virtual memory in MB)
	EnabledLangs   []string // empty = all (python, go, bash)
}

// NewCodeRunnerTool creates a new CodeRunnerTool with the given configuration.
func NewCodeRunnerTool(cfg CodeRunnerConfig) *CodeRunnerTool {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	langs := cfg.EnabledLangs
	if len(langs) == 0 {
		langs = allSupportedLangs
	}

	enabled := make(map[string]bool)
	for _, l := range langs {
		enabled[strings.ToLower(l)] = true
	}
	// canonical aliases
	if enabled["python"] {
		enabled["python3"] = true
	}
	if enabled["go"] {
		enabled["golang"] = true
	}
	if enabled["bash"] {
		enabled["sh"] = true
	}

	return &CodeRunnerTool{
		timeout:      timeout,
		memLimitMB:   cfg.MemLimitMB,
		enabledLangs: enabled,
	}
}

func (c *CodeRunnerTool) Name() string { return "code_runner" }

func (c *CodeRunnerTool) Description() string {
	return "Execute a code snippet and return stdout, stderr, and exit code. " +
		"Supported languages: python (Python 3), go (stdlib only; must have 'package main' with func main()), bash. " +
		"Code runs in an isolated temporary directory that is automatically cleaned up after execution."
}

func (c *CodeRunnerTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{
				"type":        "string",
				"description": "Programming language: python, go, or bash",
				"enum":        []string{"python", "go", "bash"},
			},
			"code": map[string]any{
				"type":        "string",
				"description": "Source code to execute",
			},
			"stdin": map[string]any{
				"type":        "string",
				"description": "Optional stdin input for the program",
			},
		},
		"required": []string{"language", "code"},
	}
}

type codeRunnerArgs struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Stdin    string `json:"stdin,omitempty"`
}

// Execute runs the given code snippet and returns a formatted result string.
func (c *CodeRunnerTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a codeRunnerArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	lang := strings.ToLower(strings.TrimSpace(a.Language))
	if !c.enabledLangs[lang] {
		return "", fmt.Errorf("language %q is not enabled; supported: %s",
			a.Language, strings.Join(allSupportedLangs, ", "))
	}
	if strings.TrimSpace(a.Code) == "" {
		return "", fmt.Errorf("code must not be empty")
	}

	tmpDir, err := os.MkdirTemp("", "code_runner_*")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir) //nolint:errcheck

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var cmd *exec.Cmd
	switch lang {
	case "python", "python3":
		cmd, err = c.buildPython(ctx, tmpDir, a.Code)
	case "go", "golang":
		cmd, err = c.buildGo(ctx, tmpDir, a.Code)
	case "bash", "sh":
		cmd, err = c.buildBash(ctx, tmpDir, a.Code)
	default:
		return "", fmt.Errorf("unsupported language: %q", lang)
	}
	if err != nil {
		return "", err
	}

	// All commands run with tmpDir as the working directory.
	cmd.Dir = tmpDir
	if a.Stdin != "" {
		cmd.Stdin = strings.NewReader(a.Stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	// Check for timeout first — ctx.Err() is set after the deadline is exceeded.
	if ctx.Err() == context.DeadlineExceeded {
		return c.formatResult(-1, stdout.String(), stderr.String(),
			fmt.Sprintf("timed out after %s", c.timeout)), nil
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok { //nolint:errorlint
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("run failed: %w", runErr)
		}
	}

	return c.formatResult(exitCode, stdout.String(), stderr.String(), ""), nil
}

// buildPython writes code to script.py and returns a command to run it.
func (c *CodeRunnerTool) buildPython(ctx context.Context, tmpDir, code string) (*exec.Cmd, error) {
	scriptPath := filepath.Join(tmpDir, "script.py")
	if err := os.WriteFile(scriptPath, []byte(code), 0o600); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}
	return exec.CommandContext(ctx, "bash", "-c", c.wrapWithLimits("python3 "+scriptPath)), nil
}

// buildGo writes main.go + go.mod to tmpDir, compiles the binary and returns
// a command to run it. Using go build instead of go run preserves the actual
// exit code of the program.
func (c *CodeRunnerTool) buildGo(ctx context.Context, tmpDir, code string) (*exec.Cmd, error) {
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte(code), 0o600); err != nil {
		return nil, fmt.Errorf("write main.go: %w", err)
	}
	goMod := "module coderunner\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		return nil, fmt.Errorf("write go.mod: %w", err)
	}
	binPath := filepath.Join(tmpDir, "program")
	// Build without limits (compiler needs more memory), then run the binary
	// with limits. Single-quoted paths are safe since os.MkdirTemp never
	// produces paths with special characters.
	var script string
	if c.memLimitMB > 0 {
		limitKB := c.memLimitMB * 1024
		script = fmt.Sprintf("go build -o '%s' . && (ulimit -v %d; '%s')", binPath, limitKB, binPath)
	} else {
		script = fmt.Sprintf("go build -o '%s' . && '%s'", binPath, binPath)
	}
	return exec.CommandContext(ctx, "bash", "-c", script), nil
}

// buildBash writes code to script.sh and returns a bash command.
func (c *CodeRunnerTool) buildBash(ctx context.Context, tmpDir, code string) (*exec.Cmd, error) {
	scriptPath := filepath.Join(tmpDir, "script.sh")
	if err := os.WriteFile(scriptPath, []byte(code), 0o600); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}
	return exec.CommandContext(ctx, "bash", "-c", c.wrapWithLimits("bash "+scriptPath)), nil
}

// wrapWithLimits prepends a ulimit -v call if a memory limit is configured.
func (c *CodeRunnerTool) wrapWithLimits(cmdStr string) string {
	if c.memLimitMB > 0 {
		limitKB := c.memLimitMB * 1024
		return fmt.Sprintf("ulimit -v %d; %s", limitKB, cmdStr)
	}
	return cmdStr
}

// formatResult formats execution output as a human-readable string.
func (c *CodeRunnerTool) formatResult(exitCode int, stdout, stderr, note string) string {
	if len(stdout) > maxCodeRunnerOutputLen {
		stdout = stdout[:maxCodeRunnerOutputLen] + "\n... (truncated)"
	}
	if len(stderr) > maxCodeRunnerOutputLen {
		stderr = stderr[:maxCodeRunnerOutputLen] + "\n... (truncated)"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Exit code: %d\n", exitCode)
	if note != "" {
		fmt.Fprintf(&sb, "Note: %s\n", note)
	}
	sb.WriteString("\nStdout:\n")
	if stdout == "" {
		sb.WriteString("(empty)\n")
	} else {
		sb.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString("\nStderr:\n")
	if stderr == "" {
		sb.WriteString("(empty)\n")
	} else {
		sb.WriteString(stderr)
		if !strings.HasSuffix(stderr, "\n") {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
