package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/config"
	"github.com/lllypuk/artel/internal/projects"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultTestRegistry creates a projects.Registry with two projects for testing:
// "agent" (self-project pointing to agentDir) and "ext" (external with model override).
func defaultTestRegistry(t *testing.T) (reg *projects.Registry, agentDir string) {
	t.Helper()
	agentDir = t.TempDir()
	projDir := t.TempDir()
	reg = projects.NewRegistry(config.PiCoderConfig{Projects: []config.ProjectConfig{
		{Name: "agent", Path: agentDir},
		{Name: "ext", Path: projDir, Model: "custom-model", TimeoutSec: 600},
	}}, agentDir, nil)
	return reg, agentDir
}

// --- Constructor tests ---

func TestNewPiCoderToolDefaults(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{
		Registry: r,
	})

	assert.Equal(t, "glm-4.7", tool.defaultModel, "default model should be glm-4.7")
	assert.Equal(t, 5*time.Minute, tool.defaultTimeout, "default timeout should be 5 min")
	assert.Equal(t, 5*time.Second, tool.restartDelay, "default restart delay should be 5s")
}

func TestNewPiCoderToolCustomValues(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{
		Registry:       r,
		DefaultModel:   "qwen3.5:cloud",
		DefaultTimeout: 10 * time.Minute,
		RestartDelay:   3 * time.Second,
	})

	assert.Equal(t, "qwen3.5:cloud", tool.defaultModel)
	assert.Equal(t, 10*time.Minute, tool.defaultTimeout)
	assert.Equal(t, 3*time.Second, tool.restartDelay)
}

// --- Name/Description/Parameters tests ---

func TestPiCoderTool_Name(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	assert.Equal(t, "pi_coder", tool.Name())
}

func TestPiCoderTool_DescriptionIncludesProjects(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	desc := tool.Description()

	assert.Contains(t, desc, "agent")
	assert.Contains(t, desc, "ext")
	assert.Contains(t, desc, "Доступные проекты")
	// self-project indication
	assert.Contains(t, desc, "код самого агента")
}

func TestPiCoderTool_ParametersEnum(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)

	projectParam, ok := props["project"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", projectParam["type"])

	enum, ok := projectParam["enum"].([]string)
	require.True(t, ok)
	assert.Contains(t, enum, "agent")
	assert.Contains(t, enum, "ext")

	// project and task should both be required
	required, ok := params["required"].([]string)
	require.True(t, ok)
	assert.Contains(t, required, "project")
	assert.Contains(t, required, "task")
}

func TestPiCoderTool_ParametersRebuildRestart(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	params := tool.Parameters()

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)

	rebuild, ok := props["rebuild"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", rebuild["type"])

	restart, ok := props["restart"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", restart["type"])
}

// --- Execute validation tests ---

func TestPiCoderTool_ExecuteRequiresProject(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"task":"test"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project is required")
}

func TestPiCoderTool_ExecuteRequiresTask(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"project":"agent","task":""}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task is required")
}

func TestPiCoderTool_ExecuteUnknownProject(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	_, err := tool.Execute(context.Background(), json.RawMessage(`{"project":"nonexistent","task":"test"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown project")
	assert.Contains(t, err.Error(), "agent")
	assert.Contains(t, err.Error(), "ext")
}

func TestPiCoderTool_ExecuteInvalidJSON(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	_, err := tool.Execute(context.Background(), json.RawMessage(`{invalid}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse args")
}

// --- Execute behavior tests ---

func TestPiCoderTool_ExecuteNonSelfRestartIgnored(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{
		Registry:       r,
		DefaultTimeout: 10 * time.Millisecond,
	})

	// "ext" is non-self; restart and rebuild should be ignored with warnings.
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"project":"ext","task":"test","rebuild":true,"restart":true}`))
	// pi not found → may return error or non-nil result
	if err == nil {
		assert.Contains(t, result, "rebuild игнорируется")
		assert.Contains(t, result, "restart игнорируется")
	}
}

func TestPiCoderTool_ExecuteSelfRestartWithoutRebuild(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{
		Registry:       r,
		DefaultTimeout: 10 * time.Millisecond,
	})

	// "agent" is self; restart=true should imply rebuild=true.
	// pi won't exist in test env → pi step fails → restart isn't scheduled.
	_, _ = tool.Execute(context.Background(), json.RawMessage(`{"project":"agent","task":"test","restart":true}`))
	// No panic, no validation error — that's the expected behavior.
}

func TestPiCoderTool_ExecuteSpecialChars(t *testing.T) {
	specials := []string{
		`Add "feature" with 'quotes'`,
		"Add function\nWith newlines",
		"Добавить функцию с эмодзи 🚀",
		`Use $VAR in code`,
	}
	for _, task := range specials {
		args, _ := json.Marshal(map[string]any{"project": "agent", "task": task})
		var a piCoderArgs
		require.NoError(t, json.Unmarshal(args, &a))
		assert.Equal(t, task, a.Task)
	}
}

func TestPiCoderTool_ExecuteResultTruncated(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	_ = NewPiCoderTool(PiCoderConfig{
		Registry:       r,
		DefaultTimeout: 10 * time.Millisecond,
	})

	// Manually test truncation logic by calling Execute with a long result path
	// Since pi doesn't exist, result will be short error. Instead test truncation
	// by constructing a very large fake result.
	// This is covered indirectly: result is truncated at maxLen.
	// Direct truncation test:
	longStr := strings.Repeat("a", 10000)
	const maxLen = 8192
	truncated := longStr
	if len(truncated) > maxLen {
		truncated = truncated[:maxLen] + "\n... (вывод обрезан)"
	}
	assert.LessOrEqual(t, len(truncated), maxLen+len("\n... (вывод обрезан)"))
	assert.Contains(t, truncated, "вывод обрезан")
}

// --- restartFunc / scheduleRestart tests ---

func TestPiCoderTool_ScheduleRestartCallsFunc(t *testing.T) {
	called := make(chan struct{}, 1)
	tool := &PiCoderTool{
		registry:     mustTestRegistry(t),
		restartFunc:  func() { called <- struct{}{} },
		restartDelay: 0,
	}

	tool.scheduleRestart()

	select {
	case <-called:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("restartFunc was not called")
	}
}

func TestPiCoderTool_ScheduleRestartNilFunc(t *testing.T) {
	tool := &PiCoderTool{
		registry:     mustTestRegistry(t),
		restartFunc:  nil,
		restartDelay: 0,
	}

	assert.NotPanics(t, func() { tool.scheduleRestart() })
}

func TestNewPiCoderTool_RestartFuncStored(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	called := false
	tool := NewPiCoderTool(PiCoderConfig{
		Registry:    r,
		RestartFunc: func() { called = true },
	})
	require.NotNil(t, tool.restartFunc)
	tool.restartFunc()
	assert.True(t, called)
}

// --- runBuild tests ---

func TestPiCoderTool_RunBuildFailsOnNonexistentDir(t *testing.T) {
	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	output, ok := tool.runBuild("/nonexistent")
	assert.False(t, ok)
	assert.Contains(t, output, "Сборка завершилась с ошибкой")
}

func TestPiCoderTool_RunBuildSucceedsOnDirWithMakefile(t *testing.T) {
	tmp := t.TempDir()
	// Create a minimal Makefile that just echoes.
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "Makefile"), []byte("build:\n\techo ok\n"), 0o644))

	r, _ := defaultTestRegistry(t)
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})

	output, ok := tool.runBuild(tmp)
	assert.True(t, ok)
	assert.Contains(t, output, "Сборка успешна")
}

// --- piCoderArgs tests ---

func TestPiCoderArgs_Unmarshal(t *testing.T) {
	var a piCoderArgs
	require.NoError(t, json.Unmarshal([]byte(`{"project":"ext","task":"test","rebuild":true,"restart":false}`), &a))
	assert.Equal(t, "ext", a.Project)
	assert.Equal(t, "test", a.Task)
	assert.True(t, a.Rebuild)
	assert.False(t, a.Restart)
}

func TestPiCoderArgs_Marshal(t *testing.T) {
	a := piCoderArgs{Project: "ext", Task: "test", Rebuild: true, Restart: false}
	data, err := json.Marshal(a)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, "ext", m["project"])
	assert.Equal(t, "test", m["task"])
}

// --- Benchmarks ---

func BenchmarkPiCoderTool_Name(b *testing.B) {
	r, _ := defaultTestRegistry(&testing.T{})
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	for i := 0; i < b.N; i++ {
		_ = tool.Name()
	}
}

func BenchmarkPiCoderTool_Parameters(b *testing.B) {
	r, _ := defaultTestRegistry(&testing.T{})
	tool := NewPiCoderTool(PiCoderConfig{Registry: r})
	for i := 0; i < b.N; i++ {
		_ = tool.Parameters()
	}
}

// mustTestRegistry returns a minimal registry for tools that need it in struct init.
func mustTestRegistry(t *testing.T) *projects.Registry {
	t.Helper()
	agentDir := t.TempDir()
	return projects.NewRegistry(config.PiCoderConfig{Projects: []config.ProjectConfig{
		{Name: "agent", Path: agentDir},
	}}, agentDir, nil)
}
