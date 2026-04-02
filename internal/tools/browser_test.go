package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserTool_Name(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})
	assert.Equal(t, "browser", tool.Name())
}

func TestBrowserTool_Description(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})
	desc := tool.Description()
	assert.Contains(t, desc, "snapshot")
	assert.Contains(t, desc, "screenshot")
	assert.Contains(t, desc, "click")
	assert.Contains(t, desc, "JavaScript")
}

func TestBrowserTool_Parameters(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})
	params := tool.Parameters()

	assert.Equal(t, "object", params["type"])

	props := params["properties"].(map[string]any)
	assert.Contains(t, props, "command")
	assert.Contains(t, props, "url")
	assert.Contains(t, props, "selector")
	assert.Contains(t, props, "text")
	assert.Contains(t, props, "key")
	assert.Contains(t, props, "js")
	assert.Contains(t, props, "path")
	assert.Contains(t, props, "attribute")
	assert.Contains(t, props, "value")
	assert.Contains(t, props, "session")
	assert.Contains(t, props, "direction")
	assert.Contains(t, props, "pixels")
	assert.Contains(t, props, "wait_ms")
	assert.Contains(t, props, "full_page")
	assert.Contains(t, props, "width")
	assert.Contains(t, props, "height")
	assert.Contains(t, props, "interactive")

	required := params["required"].([]string)
	assert.Contains(t, required, "command")
}

func TestNewBrowserTool_Defaults(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	assert.Equal(t, "./data/screenshots", tool.screenshotDir)
	assert.Equal(t, "agent-browser", tool.agentBrowserPath)
	assert.Equal(t, 30*time.Second, tool.timeout)
	assert.Equal(t, "", tool.proxy)
}

func TestNewBrowserTool_CustomConfig(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{
		ScreenshotDir:    "/custom/screenshots",
		AgentBrowserPath: "/usr/bin/agent-browser",
		TimeoutSeconds:   60,
		Proxy:            "http://127.0.0.1:2080",
	})

	assert.Equal(t, "/custom/screenshots", tool.screenshotDir)
	assert.Equal(t, "/usr/bin/agent-browser", tool.agentBrowserPath)
	assert.Equal(t, 60*time.Second, tool.timeout)
	assert.Equal(t, "http://127.0.0.1:2080", tool.proxy)
}

func TestBrowserTool_Execute_MissingCommand(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	args, err := json.Marshal(map[string]any{})
	require.NoError(t, err)

	_, err = tool.Execute(context.Background(), args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestBrowserTool_Execute_InvalidJSON(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, err := tool.Execute(context.Background(), []byte("invalid json"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse args")
}

func TestBrowserTool_Execute_UnknownCommand(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	args, err := json.Marshal(map[string]any{"command": "teleport"})
	require.NoError(t, err)

	_, err = tool.Execute(context.Background(), args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown command")
}

// --- buildArgs tests (unit, no subprocess) ---

func TestBrowserTool_BuildArgs_Open(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	// Missing URL
	_, _, err := tool.buildArgs(browserArgs{Command: "open"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")

	// With URL
	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "open", URL: "https://example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"open", "https://example.com"}, cmdArgs)

	// With viewport
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "open", URL: "https://example.com", Width: 1920, Height: 1080})
	require.NoError(t, err)
	assert.Contains(t, cmdArgs, "--viewport")
	assert.Contains(t, cmdArgs, "1920x1080")
}

func TestBrowserTool_BuildArgs_Snapshot(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "snapshot"})
	require.NoError(t, err)
	assert.Equal(t, []string{"snapshot"}, cmdArgs)

	// Interactive
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "snapshot", Interactive: true})
	require.NoError(t, err)
	assert.Contains(t, cmdArgs, "-i")

	// With selector
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "snapshot", Selector: "#main"})
	require.NoError(t, err)
	assert.Contains(t, cmdArgs, "--selector")
	assert.Contains(t, cmdArgs, "#main")
}

func TestBrowserTool_BuildArgs_Screenshot(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{ScreenshotDir: t.TempDir()})

	// Auto path
	cmdArgs, screenshotPath, err := tool.buildArgs(browserArgs{Command: "screenshot"})
	require.NoError(t, err)
	assert.NotEmpty(t, screenshotPath)
	assert.Contains(t, screenshotPath, "screenshot-")
	assert.Equal(t, "screenshot", cmdArgs[0])
	assert.Equal(t, screenshotPath, cmdArgs[1])

	// Custom path
	cmdArgs, screenshotPath, err = tool.buildArgs(browserArgs{Command: "screenshot", Path: "/tmp/test.png"})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/test.png", screenshotPath)
	assert.Contains(t, cmdArgs, "/tmp/test.png")

	// Full page
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "screenshot", FullPage: true})
	require.NoError(t, err)
	assert.Contains(t, cmdArgs, "--full")
}

func TestBrowserTool_BuildArgs_Click(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	// Missing selector
	_, _, err := tool.buildArgs(browserArgs{Command: "click"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "selector is required")

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "click", Selector: "@e3"})
	require.NoError(t, err)
	assert.Equal(t, []string{"click", "@e3"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Fill(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, _, err := tool.buildArgs(browserArgs{Command: "fill"})
	assert.Error(t, err)

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "fill", Selector: "#email", Text: "user@example.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"fill", "#email", "user@example.com"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Type(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "type", Selector: "#search", Text: "query"})
	require.NoError(t, err)
	assert.Equal(t, []string{"type", "#search", "query"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Press(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, _, err := tool.buildArgs(browserArgs{Command: "press"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key is required")

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "press", Key: "Enter"})
	require.NoError(t, err)
	assert.Equal(t, []string{"press", "Enter"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Scroll(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	// Default direction (down)
	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "scroll"})
	require.NoError(t, err)
	assert.Contains(t, cmdArgs, "down")

	// With pixels
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "scroll", Direction: "up", Pixels: 500})
	require.NoError(t, err)
	assert.Equal(t, []string{"scroll", "up", "500"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Wait(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	// No args
	_, _, err := tool.buildArgs(browserArgs{Command: "wait"})
	assert.Error(t, err)

	// By selector
	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "wait", Selector: "#dashboard"})
	require.NoError(t, err)
	assert.Equal(t, []string{"wait", "#dashboard"}, cmdArgs)

	// By ms
	cmdArgs, _, err = tool.buildArgs(browserArgs{Command: "wait", WaitMS: 2000})
	require.NoError(t, err)
	assert.Equal(t, []string{"wait", "2000"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_GetCommands(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	tests := []struct {
		command  string
		selector string
		extra    browserArgs
		expected []string
		errStr   string
	}{
		{command: "get_text", selector: "h1", expected: []string{"get", "text", "h1"}},
		{command: "get_html", selector: "#content", expected: []string{"get", "html", "#content"}},
		{command: "get_value", selector: "input", expected: []string{"get", "value", "input"}},
		{command: "get_url", expected: []string{"get", "url"}},
		{command: "get_title", expected: []string{"get", "title"}},
		{command: "get_text", errStr: "selector is required"},
	}

	for _, tt := range tests {
		a := browserArgs{Command: tt.command, Selector: tt.selector}
		cmdArgs, _, err := tool.buildArgs(a)
		if tt.errStr != "" {
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errStr)
		} else {
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cmdArgs)
		}
	}
}

func TestBrowserTool_BuildArgs_GetAttr(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, _, err := tool.buildArgs(browserArgs{Command: "get_attr", Selector: "a"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "attribute is required")

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "get_attr", Selector: "a.link", Attribute: "href"})
	require.NoError(t, err)
	assert.Equal(t, []string{"get", "attr", "href", "a.link"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Eval(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, _, err := tool.buildArgs(browserArgs{Command: "eval"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "js is required")

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "eval", JS: "document.title"})
	require.NoError(t, err)
	assert.Equal(t, []string{"eval", "document.title"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_IsCommands(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	cases := []struct {
		command  string
		expected []string
	}{
		{"is_visible", []string{"is", "visible", "#btn"}},
		{"is_enabled", []string{"is", "enabled", "#btn"}},
		{"is_checked", []string{"is", "checked", "#chk"}},
	}

	for _, c := range cases {
		selector := "#btn"
		if c.command == "is_checked" {
			selector = "#chk"
		}
		cmdArgs, _, err := tool.buildArgs(browserArgs{Command: c.command, Selector: selector})
		require.NoError(t, err)
		assert.Equal(t, c.expected, cmdArgs, "command: %s", c.command)
	}
}

func TestBrowserTool_BuildArgs_NavigationCommands(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	for _, cmd := range []string{"back", "forward", "reload", "close", "errors", "console"} {
		cmdArgs, _, err := tool.buildArgs(browserArgs{Command: cmd})
		require.NoError(t, err, "command: %s", cmd)
		assert.Equal(t, []string{cmd}, cmdArgs, "command: %s", cmd)
	}
}

func TestBrowserTool_BuildArgs_Session(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "snapshot", Session: "test-session"})
	require.NoError(t, err)
	assert.Equal(t, []string{"--session", "test-session", "snapshot"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Proxy(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{Proxy: "http://127.0.0.1:2080"})

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "get_url"})
	require.NoError(t, err)
	assert.Equal(t, []string{"--proxy", "http://127.0.0.1:2080", "get", "url"}, cmdArgs)
}

func TestBrowserTool_BuildArgs_Select(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{})

	_, _, err := tool.buildArgs(browserArgs{Command: "select", Selector: "#lang"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "value is required")

	cmdArgs, _, err := tool.buildArgs(browserArgs{Command: "select", Selector: "#lang", Value: "ru"})
	require.NoError(t, err)
	assert.Equal(t, []string{"select", "#lang", "ru"}, cmdArgs)
}

func TestBrowserTool_ScreenshotDir(t *testing.T) {
	tool := NewBrowserTool(BrowserConfig{ScreenshotDir: "/my/dir"})
	assert.Equal(t, "/my/dir", tool.ScreenshotDir())
}
