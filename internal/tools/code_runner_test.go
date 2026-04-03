package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helpers

func newCodeRunner(langs ...string) *tools.CodeRunnerTool {
	return tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
		TimeoutSeconds: 30,
		EnabledLangs:   langs,
	})
}

func codeRunnerExec(t *testing.T, cr *tools.CodeRunnerTool, lang, code, stdin string) (string, error) {
	t.Helper()
	a := map[string]any{"language": lang, "code": code}
	if stdin != "" {
		a["stdin"] = stdin
	}
	raw, _ := json.Marshal(a)
	return cr.Execute(context.Background(), raw)
}

// ── Metadata ──────────────────────────────────────────────────────────────────

func TestCodeRunnerName(t *testing.T) {
	cr := newCodeRunner()
	assert.Equal(t, "code_runner", cr.Name())
}

func TestCodeRunnerDescription(t *testing.T) {
	cr := newCodeRunner()
	assert.NotEmpty(t, cr.Description())
}

func TestCodeRunnerParameters(t *testing.T) {
	cr := newCodeRunner()
	params := cr.Parameters()
	assert.Equal(t, "object", params["type"])
	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, props, "language")
	assert.Contains(t, props, "code")
	assert.Contains(t, props, "stdin")
	req, ok := params["required"].([]string)
	require.True(t, ok)
	assert.Contains(t, req, "language")
	assert.Contains(t, req, "code")
}

// ── Python ────────────────────────────────────────────────────────────────────

func TestCodeRunnerPythonHello(t *testing.T) {
	cr := newCodeRunner("python")
	out, err := codeRunnerExec(t, cr, "python", `print("hello from python")`, "")
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 0")
	assert.Contains(t, out, "hello from python")
}

func TestCodeRunnerPythonArithmetic(t *testing.T) {
	cr := newCodeRunner("python")
	out, err := codeRunnerExec(t, cr, "python", "print(2 + 3 * 7)", "")
	require.NoError(t, err)
	assert.Contains(t, out, "23")
}

func TestCodeRunnerPythonStdin(t *testing.T) {
	cr := newCodeRunner("python")
	code := "import sys\nprint('got:', sys.stdin.read().strip())"
	out, err := codeRunnerExec(t, cr, "python", code, "hello stdin")
	require.NoError(t, err)
	assert.Contains(t, out, "got: hello stdin")
}

func TestCodeRunnerPythonNonZeroExit(t *testing.T) {
	cr := newCodeRunner("python")
	out, err := codeRunnerExec(t, cr, "python", "import sys; sys.exit(42)", "")
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 42")
}

func TestCodeRunnerPythonSyntaxError(t *testing.T) {
	cr := newCodeRunner("python")
	out, err := codeRunnerExec(t, cr, "python", "def broken(", "")
	require.NoError(t, err)
	// Syntax error → non-zero exit code
	assert.NotContains(t, out, "Exit code: 0\n")
	assert.Contains(t, out, "Stderr:")
}

func TestCodeRunnerPythonStdErr(t *testing.T) {
	cr := newCodeRunner("python")
	code := "import sys\nprint('out')\nprint('err', file=sys.stderr)"
	out, err := codeRunnerExec(t, cr, "python", code, "")
	require.NoError(t, err)
	assert.Contains(t, out, "out")
	assert.Contains(t, out, "err")
}

// ── Bash ──────────────────────────────────────────────────────────────────────

func TestCodeRunnerBashHello(t *testing.T) {
	cr := newCodeRunner("bash")
	out, err := codeRunnerExec(t, cr, "bash", `echo "hello from bash"`, "")
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 0")
	assert.Contains(t, out, "hello from bash")
}

func TestCodeRunnerBashNonZeroExit(t *testing.T) {
	cr := newCodeRunner("bash")
	out, err := codeRunnerExec(t, cr, "bash", "exit 5", "")
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 5")
}

func TestCodeRunnerBashStdin(t *testing.T) {
	cr := newCodeRunner("bash")
	out, err := codeRunnerExec(t, cr, "bash", "read line; echo got: $line", "world")
	require.NoError(t, err)
	assert.Contains(t, out, "got: world")
}

func TestCodeRunnerBashMultiline(t *testing.T) {
	cr := newCodeRunner("bash")
	code := "for i in 1 2 3; do echo $i; done"
	out, err := codeRunnerExec(t, cr, "bash", code, "")
	require.NoError(t, err)
	assert.Contains(t, out, "1")
	assert.Contains(t, out, "2")
	assert.Contains(t, out, "3")
}

// ── Go ────────────────────────────────────────────────────────────────────────

func TestCodeRunnerGoHello(t *testing.T) {
	cr := tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
		TimeoutSeconds: 60,
		EnabledLangs:   []string{"go"},
	})
	code := "package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hello from go\") }"
	raw, _ := json.Marshal(map[string]any{"language": "go", "code": code})
	out, err := cr.Execute(context.Background(), raw)
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 0")
	assert.Contains(t, out, "hello from go")
}

func TestCodeRunnerGoNonZeroExit(t *testing.T) {
	cr := tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
		TimeoutSeconds: 60,
		EnabledLangs:   []string{"go"},
	})
	code := "package main\nimport \"os\"\nfunc main() { os.Exit(7) }"
	raw, _ := json.Marshal(map[string]any{"language": "go", "code": code})
	out, err := cr.Execute(context.Background(), raw)
	require.NoError(t, err)
	assert.Contains(t, out, "Exit code: 7")
}

func TestCodeRunnerGoCompileError(t *testing.T) {
	cr := tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
		TimeoutSeconds: 60,
		EnabledLangs:   []string{"go"},
	})
	code := "package main\nfunc main() { invalid_code_here }"
	raw, _ := json.Marshal(map[string]any{"language": "go", "code": code})
	out, err := cr.Execute(context.Background(), raw)
	require.NoError(t, err)
	assert.NotContains(t, out, "Exit code: 0\n")
	assert.Contains(t, out, "Stderr:")
}

// ── Language aliases ──────────────────────────────────────────────────────────

func TestCodeRunnerPython3Alias(t *testing.T) {
	cr := newCodeRunner("python")
	out, err := codeRunnerExec(t, cr, "python3", "print('alias works')", "")
	require.NoError(t, err)
	assert.Contains(t, out, "alias works")
}

func TestCodeRunnerShAlias(t *testing.T) {
	cr := newCodeRunner("bash")
	out, err := codeRunnerExec(t, cr, "sh", "echo sh-alias", "")
	require.NoError(t, err)
	assert.Contains(t, out, "sh-alias")
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestCodeRunnerUnsupportedLanguage(t *testing.T) {
	cr := newCodeRunner()
	_, err := codeRunnerExec(t, cr, "ruby", "puts 'hello'", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestCodeRunnerDisabledLanguage(t *testing.T) {
	// Only bash enabled — python must be rejected.
	cr := newCodeRunner("bash")
	_, err := codeRunnerExec(t, cr, "python", "print('hi')", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not enabled")
}

func TestCodeRunnerEmptyCode(t *testing.T) {
	cr := newCodeRunner()
	_, err := codeRunnerExec(t, cr, "python", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestCodeRunnerWhitespaceOnlyCode(t *testing.T) {
	cr := newCodeRunner()
	_, err := codeRunnerExec(t, cr, "bash", "   \n\t  ", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestCodeRunnerInvalidJSON(t *testing.T) {
	cr := newCodeRunner()
	_, err := cr.Execute(context.Background(), json.RawMessage(`{invalid}`))
	require.Error(t, err)
}

// ── Timeout ───────────────────────────────────────────────────────────────────

func TestCodeRunnerTimeout(t *testing.T) {
	cr := tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
		TimeoutSeconds: 1,
		EnabledLangs:   []string{"bash"},
	})
	out, err := codeRunnerExec(t, cr, "bash", "sleep 100", "")
	require.NoError(t, err) // timeout is reported in result, not as an error
	assert.Contains(t, strings.ToLower(out), "timed out")
}

// ── Default config (all langs enabled) ───────────────────────────────────────

func TestCodeRunnerDefaultAllLangsEnabled(t *testing.T) {
	cr := tools.NewCodeRunnerTool(tools.CodeRunnerConfig{TimeoutSeconds: 30})
	// python, go, bash should all be accepted (we only test one to keep it fast)
	out, err := codeRunnerExec(t, cr, "bash", "echo default-all", "")
	require.NoError(t, err)
	assert.Contains(t, out, "default-all")
}

// ── Output truncation ─────────────────────────────────────────────────────────

func TestCodeRunnerOutputTruncation(t *testing.T) {
	cr := newCodeRunner("python")
	// Generate ~6 KB of output — should be truncated to 2048 bytes + note.
	code := "print('x' * 6000)"
	out, err := codeRunnerExec(t, cr, "python", code, "")
	require.NoError(t, err)
	assert.Contains(t, out, "truncated")
}
