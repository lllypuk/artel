package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lllypuk/artel/internal/tools"
)

func newTestShell(allowed, blocked []string) *tools.ShellTool {
	return tools.NewShellTool(tools.ShellConfig{
		AllowedCommands: allowed,
		BlockedPatterns: blocked,
		WorkingDir:      "",
		TimeoutSeconds:  5,
	})
}

func shellExec(t *testing.T, s *tools.ShellTool, cmd string) (string, error) {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"command": cmd})
	return s.Execute(context.Background(), args)
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestShellName(t *testing.T) {
	s := newTestShell(nil, nil)
	if s.Name() != "shell" {
		t.Errorf("expected 'shell', got %q", s.Name())
	}
}

func TestShellDescription(t *testing.T) {
	s := newTestShell(nil, nil)
	if s.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestShellParameters(t *testing.T) {
	s := newTestShell(nil, nil)
	params := s.Parameters()
	if params["type"] != "object" {
		t.Errorf("expected type 'object', got %v", params["type"])
	}
}

// ── Whitelist ─────────────────────────────────────────────────────────────────

func TestShellAllowedCommand(t *testing.T) {
	s := newTestShell([]string{"echo"}, nil)
	out, err := shellExec(t, s, "echo hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got %q", out)
	}
}

func TestShellBlockedCommand(t *testing.T) {
	s := newTestShell([]string{"echo"}, nil)
	_, err := shellExec(t, s, "rm -rf /tmp/test")
	if err == nil {
		t.Fatal("expected error for non-whitelisted command 'rm'")
	}
	if !strings.Contains(err.Error(), "whitelist") {
		t.Errorf("expected 'whitelist' in error, got: %v", err)
	}
}

func TestShellEmptyCommandAllowed(t *testing.T) {
	// Empty parts (e.g. trailing semicolons) should not crash the validator.
	s := newTestShell([]string{"echo"}, nil)
	_, _ = shellExec(t, s, "echo hi;")
}

// ── Blocked patterns ──────────────────────────────────────────────────────────

func TestShellBlockedPattern(t *testing.T) {
	s := newTestShell([]string{"echo"}, []string{"rm -rf /"})
	_, err := shellExec(t, s, "echo rm -rf /")
	if err == nil {
		t.Fatal("expected error for blocked pattern")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected 'blocked' in error, got: %v", err)
	}
}

func TestShellBlockedPatternCaseInsensitive(t *testing.T) {
	s := newTestShell([]string{"echo"}, []string{"DANGEROUS"})
	_, err := shellExec(t, s, "echo dangerous word")
	if err == nil {
		t.Fatal("expected error for case-insensitive blocked pattern")
	}
}

// ── sudo ──────────────────────────────────────────────────────────────────────

func TestShellSudoBlocked(t *testing.T) {
	s := newTestShell([]string{"sudo", "ls"}, nil)
	_, err := shellExec(t, s, "sudo ls /")
	if err == nil {
		t.Fatal("expected error for sudo")
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("expected 'sudo' in error, got: %v", err)
	}
}

// ── Redirection ───────────────────────────────────────────────────────────────

func TestShellRedirectionBlocked(t *testing.T) {
	s := newTestShell([]string{"echo"}, nil)
	_, err := shellExec(t, s, "echo hello > /tmp/out.txt")
	if err == nil {
		t.Fatal("expected error for output redirection")
	}
	if !strings.Contains(err.Error(), "redirection") {
		t.Errorf("expected 'redirection' in error, got: %v", err)
	}
}

// ── Pipes ─────────────────────────────────────────────────────────────────────

func TestShellPipeAllowed(t *testing.T) {
	s := newTestShell([]string{"echo", "grep"}, nil)
	out, err := shellExec(t, s, "echo hello world | grep hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got %q", out)
	}
}

func TestShellPipeWithBlockedCommand(t *testing.T) {
	s := newTestShell([]string{"echo"}, nil)
	_, err := shellExec(t, s, "echo hello | rm -f /tmp/x")
	if err == nil {
		t.Fatal("expected error for blocked command in pipe")
	}
}

// ── Edge cases ────────────────────────────────────────────────────────────────

func TestShellInvalidJSON(t *testing.T) {
	s := newTestShell([]string{"echo"}, nil)
	_, err := s.Execute(context.Background(), json.RawMessage(`{invalid}`))
	if err == nil {
		t.Fatal("expected error for invalid JSON args")
	}
}

func TestShellDefaultTimeout(t *testing.T) {
	// TimeoutSeconds=0 should use the 30-second default without panicking.
	s := tools.NewShellTool(tools.ShellConfig{
		AllowedCommands: []string{"echo"},
		TimeoutSeconds:  0,
	})
	out, err := shellExec(t, s, "echo timeout-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "timeout-test") {
		t.Errorf("expected 'timeout-test' in output, got %q", out)
	}
}

func TestShellCommandNotFound(t *testing.T) {
	s := newTestShell([]string{"nonexistentcmd12345"}, nil)
	_, err := shellExec(t, s, "nonexistentcmd12345")
	if err == nil {
		t.Fatal("expected error for command-not-found")
	}
}
