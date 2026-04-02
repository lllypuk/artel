package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// BrowserTool provides universal browser automation via agent-browser CLI.
// Supports navigation, interaction, inspection, screenshots, JavaScript execution,
// and all other agent-browser capabilities in a single LLM-friendly interface.
type BrowserTool struct {
	screenshotDir    string
	agentBrowserPath string
	timeout          time.Duration
	proxy            string
}

// BrowserConfig holds configuration for the browser tool.
type BrowserConfig struct {
	ScreenshotDir    string // Directory for screenshots (default: "./data/screenshots")
	AgentBrowserPath string // Path to agent-browser binary (default: "agent-browser")
	TimeoutSeconds   int    // Timeout for browser operations (default: 30s)
	Proxy            string // Proxy URL (optional, e.g. "http://127.0.0.1:2080")
}

// NewBrowserTool creates a new universal browser automation tool.
func NewBrowserTool(cfg BrowserConfig) *BrowserTool {
	screenshotDir := cfg.ScreenshotDir
	if screenshotDir == "" {
		screenshotDir = "./data/screenshots"
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	agentBrowserPath := cfg.AgentBrowserPath
	if agentBrowserPath == "" {
		agentBrowserPath = "agent-browser"
	}

	return &BrowserTool{
		screenshotDir:    screenshotDir,
		agentBrowserPath: agentBrowserPath,
		timeout:          timeout,
		proxy:            cfg.Proxy,
	}
}

// browserArgs defines the parameters accepted by the browser tool.
type browserArgs struct {
	// Command to execute (required).
	// Supported: open, snapshot, screenshot, click, dblclick, fill, type, press,
	//   hover, check, uncheck, select, scroll, scrollintoview, wait,
	//   get_text, get_html, get_value, get_attr, get_url, get_title,
	//   eval, back, forward, reload, close,
	//   is_visible, is_enabled, is_checked,
	//   errors, console
	Command string `json:"command"`

	// URL to open (for: open)
	URL string `json:"url,omitempty"`

	// CSS selector or @ref from snapshot (for: click, dblclick, fill, type,
	// hover, check, uncheck, select, scrollintoview, wait, get_text, get_html,
	// get_value, get_attr, is_visible, is_enabled, is_checked)
	Selector string `json:"selector,omitempty"`

	// Text to type or fill (for: fill, type)
	Text string `json:"text,omitempty"`

	// Key to press, e.g. "Enter", "Tab", "Control+a" (for: press)
	Key string `json:"key,omitempty"`

	// JavaScript expression to evaluate (for: eval)
	JS string `json:"js,omitempty"`

	// File path for saving screenshot or PDF (for: screenshot, pdf)
	// If omitted for screenshot, auto-generated in screenshotDir
	Path string `json:"path,omitempty"`

	// Attribute name to retrieve (for: get_attr)
	Attribute string `json:"attribute,omitempty"`

	// Option value to select (for: select)
	Value string `json:"value,omitempty"`

	// Browser session name for isolation (optional, default: "default")
	Session string `json:"session,omitempty"`

	// Scroll direction: up, down, left, right (for: scroll)
	Direction string `json:"direction,omitempty"`

	// Pixels to scroll (for: scroll)
	Pixels int `json:"pixels,omitempty"`

	// Milliseconds to wait (for: wait, when no selector given)
	WaitMS int `json:"wait_ms,omitempty"`

	// Capture full page scrollable content (for: screenshot)
	FullPage bool `json:"full_page,omitempty"`

	// Viewport width in pixels (for: open)
	Width int `json:"width,omitempty"`

	// Viewport height in pixels (for: open)
	Height int `json:"height,omitempty"`

	// Interactive elements only in snapshot (for: snapshot)
	Interactive bool `json:"interactive,omitempty"`
}

func (b *BrowserTool) Name() string { return "browser" }

func (b *BrowserTool) Description() string {
	return "Universal browser automation tool. Opens web pages, interacts with elements (click, fill, select, hover), " +
		"reads content (text, HTML, accessibility tree), runs JavaScript, takes screenshots, " +
		"navigates history, and more. Use 'snapshot' to get the accessibility tree with refs (@e1, @e2...) " +
		"before interacting with elements — refs are more reliable than CSS selectors."
}

func (b *BrowserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type": "string",
				"description": "Browser action to perform. One of: " +
					"open (navigate to URL), " +
					"snapshot (get accessibility tree with element refs), " +
					"screenshot (take screenshot), " +
					"click (click element), " +
					"dblclick (double-click element), " +
					"fill (clear and fill input), " +
					"type (type into element), " +
					"press (press keyboard key), " +
					"hover (hover over element), " +
					"check (check checkbox), " +
					"uncheck (uncheck checkbox), " +
					"select (select dropdown option), " +
					"scroll (scroll page or element), " +
					"scrollintoview (scroll element into view), " +
					"wait (wait for element or time), " +
					"get_text (get element text content), " +
					"get_html (get element outer HTML), " +
					"get_value (get input value), " +
					"get_attr (get element attribute), " +
					"get_url (get current page URL), " +
					"get_title (get page title), " +
					"eval (run JavaScript expression), " +
					"back (navigate back), " +
					"forward (navigate forward), " +
					"reload (reload page), " +
					"close (close browser), " +
					"is_visible (check if element is visible), " +
					"is_enabled (check if element is enabled), " +
					"is_checked (check if checkbox is checked), " +
					"errors (get page JS errors), " +
					"console (get browser console logs)",
				"enum": []string{
					"open", "snapshot", "screenshot",
					"click", "dblclick", "fill", "type", "press",
					"hover", "check", "uncheck", "select",
					"scroll", "scrollintoview", "wait",
					"get_text", "get_html", "get_value", "get_attr", "get_url", "get_title",
					"eval", "back", "forward", "reload", "close",
					"is_visible", "is_enabled", "is_checked",
					"errors", "console",
				},
			},
			"url": map[string]any{
				"type":        "string",
				"description": "URL to navigate to (required for: open)",
			},
			"selector": map[string]any{
				"type":        "string",
				"description": "CSS selector or @ref from snapshot (e.g. '#submit', 'button.primary', '@e3'). Required for element interactions.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to enter (required for: fill, type)",
			},
			"key": map[string]any{
				"type":        "string",
				"description": "Key to press, e.g. 'Enter', 'Tab', 'Escape', 'Control+a', 'ArrowDown' (required for: press)",
			},
			"js": map[string]any{
				"type":        "string",
				"description": "JavaScript expression to evaluate in page context (required for: eval). Returns result as string.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File path to save screenshot or PDF. If omitted for screenshot, auto-generated.",
			},
			"attribute": map[string]any{
				"type":        "string",
				"description": "Attribute name to retrieve (required for: get_attr), e.g. 'href', 'src', 'data-id'",
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Value to select in dropdown (required for: select)",
			},
			"session": map[string]any{
				"type":        "string",
				"description": "Browser session name for isolation between tasks (optional, default: 'default')",
			},
			"direction": map[string]any{
				"type":        "string",
				"description": "Scroll direction: up, down, left, right (required for: scroll)",
				"enum":        []string{"up", "down", "left", "right"},
			},
			"pixels": map[string]any{
				"type":        "integer",
				"description": "Pixels to scroll (optional for: scroll, default: 300)",
			},
			"wait_ms": map[string]any{
				"type":        "integer",
				"description": "Milliseconds to wait (for: wait when no selector given)",
			},
			"full_page": map[string]any{
				"type":        "boolean",
				"description": "Capture full scrollable page content (for: screenshot)",
			},
			"width": map[string]any{
				"type":        "integer",
				"description": "Viewport width in pixels (for: open, default: 1280)",
			},
			"height": map[string]any{
				"type":        "integer",
				"description": "Viewport height in pixels (for: open, default: 720)",
			},
			"interactive": map[string]any{
				"type":        "boolean",
				"description": "Show only interactive elements in snapshot (for: snapshot)",
			},
		},
		"required": []string{"command"},
	}
}

func (b *BrowserTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a browserArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	cmdArgs, screenshotPath, err := b.buildArgs(a)
	if err != nil {
		return "", err
	}

	browserCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	cmd := exec.CommandContext(browserCtx, b.agentBrowserPath, cmdArgs...) //nolint:gosec
	output, err := cmd.CombinedOutput()
	if err != nil {
		if browserCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("browser command timed out after %s", b.timeout)
		}
		return "", fmt.Errorf("browser command failed: %w\nOutput: %s", err, strings.TrimSpace(string(output)))
	}

	result := strings.TrimSpace(string(output))

	// For screenshot commands, return the path so callers can send the file
	if a.Command == "screenshot" && screenshotPath != "" {
		if result != "" {
			return fmt.Sprintf("screenshot_path: %s\n%s", screenshotPath, result), nil
		}
		return fmt.Sprintf("screenshot_path: %s", screenshotPath), nil
	}

	if result == "" {
		return fmt.Sprintf("OK: %s completed", a.Command), nil
	}
	return result, nil
}

// buildArgs constructs the agent-browser CLI arguments from browserArgs.
// Returns the argument slice, the screenshot path (if any), and an error.
func (b *BrowserTool) buildArgs(a browserArgs) (args []string, screenshotPath string, err error) {
	prefix := b.buildPrefix(a)

	switch a.Command {
	case "open":
		return buildOpenArgs(prefix, a)
	case "snapshot":
		return buildSnapshotArgs(prefix, a), "", nil
	case "screenshot":
		return b.buildScreenshotArgs(prefix, a)
	case "click", "dblclick", "hover", "check", "uncheck", "scrollintoview":
		return buildSelectorArgs(prefix, a)
	case "fill", "type":
		return buildTextArgs(prefix, a)
	case "press":
		return buildPressArgs(prefix, a)
	case "select":
		return buildSelectArgs(prefix, a)
	case "scroll":
		return buildScrollArgs(prefix, a), "", nil
	case "wait":
		return buildWaitArgs(prefix, a)
	case "get_text", "get_html", "get_value", "get_attr", "get_url", "get_title":
		return buildGetArgs(prefix, a)
	case "eval":
		return buildEvalArgs(prefix, a)
	case "is_visible", "is_enabled", "is_checked":
		return buildIsArgs(prefix, a)
	case "back", "forward", "reload", "close", "errors", "console":
		return append(prefix, a.Command), "", nil //nolint:gocritic
	default:
		return nil, "", fmt.Errorf("unknown command: %s", a.Command)
	}
}

// buildPrefix returns the common options slice (session, proxy).
func (b *BrowserTool) buildPrefix(a browserArgs) []string {
	var prefix []string
	if a.Session != "" {
		prefix = append(prefix, "--session", a.Session)
	}
	if b.proxy != "" {
		prefix = append(prefix, "--proxy", b.proxy)
	}
	return prefix
}

func buildOpenArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.URL == "" {
		return nil, "", fmt.Errorf("url is required for open command")
	}
	cmdArgs := append(append([]string(nil), prefix...), "open", a.URL)
	if a.Width > 0 && a.Height > 0 {
		cmdArgs = append(cmdArgs, "--viewport", fmt.Sprintf("%dx%d", a.Width, a.Height))
	}
	return cmdArgs, "", nil
}

func buildSnapshotArgs(prefix []string, a browserArgs) []string {
	cmdArgs := append(append([]string(nil), prefix...), "snapshot")
	if a.Interactive {
		cmdArgs = append(cmdArgs, "-i")
	}
	if a.Selector != "" {
		cmdArgs = append(cmdArgs, "--selector", a.Selector)
	}
	return cmdArgs
}

func (b *BrowserTool) buildScreenshotArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	path := a.Path
	if path == "" {
		if err := os.MkdirAll(b.screenshotDir, 0o750); err != nil {
			return nil, "", fmt.Errorf("create screenshot dir: %w", err)
		}
		path = filepath.Join(b.screenshotDir, fmt.Sprintf("screenshot-%s.png", time.Now().Format("20060102-150405")))
	}
	cmdArgs := append(append([]string(nil), prefix...), "screenshot", path)
	if a.FullPage {
		cmdArgs = append(cmdArgs, "--full")
	}
	return cmdArgs, path, nil
}

func buildSelectorArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.Selector == "" {
		return nil, "", fmt.Errorf("selector is required for %s command", a.Command)
	}
	return append(append([]string(nil), prefix...), a.Command, a.Selector), "", nil
}

func buildTextArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.Selector == "" {
		return nil, "", fmt.Errorf("selector is required for %s command", a.Command)
	}
	return append(append([]string(nil), prefix...), a.Command, a.Selector, a.Text), "", nil
}

func buildPressArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.Key == "" {
		return nil, "", fmt.Errorf("key is required for press command")
	}
	return append(append([]string(nil), prefix...), "press", a.Key), "", nil
}

func buildSelectArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.Selector == "" {
		return nil, "", fmt.Errorf("selector is required for select command")
	}
	if a.Value == "" {
		return nil, "", fmt.Errorf("value is required for select command")
	}
	return append(append([]string(nil), prefix...), "select", a.Selector, a.Value), "", nil
}

func buildScrollArgs(prefix []string, a browserArgs) []string {
	dir := a.Direction
	if dir == "" {
		dir = "down"
	}
	cmdArgs := append(append([]string(nil), prefix...), "scroll", dir)
	if a.Pixels > 0 {
		cmdArgs = append(cmdArgs, fmt.Sprintf("%d", a.Pixels))
	}
	return cmdArgs
}

func buildWaitArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	switch {
	case a.Selector != "":
		return append(append([]string(nil), prefix...), "wait", a.Selector), "", nil
	case a.WaitMS > 0:
		return append(append([]string(nil), prefix...), "wait", fmt.Sprintf("%d", a.WaitMS)), "", nil
	default:
		return nil, "", fmt.Errorf("selector or wait_ms is required for wait command")
	}
}

func buildGetArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	switch a.Command {
	case "get_url":
		return append(append([]string(nil), prefix...), "get", "url"), "", nil
	case "get_title":
		return append(append([]string(nil), prefix...), "get", "title"), "", nil
	}
	if a.Selector == "" {
		return nil, "", fmt.Errorf("selector is required for %s command", a.Command)
	}
	subcmd := strings.TrimPrefix(a.Command, "get_")
	if a.Command == "get_attr" {
		if a.Attribute == "" {
			return nil, "", fmt.Errorf("attribute is required for get_attr command")
		}
		return append(append([]string(nil), prefix...), "get", "attr", a.Attribute, a.Selector), "", nil
	}
	return append(append([]string(nil), prefix...), "get", subcmd, a.Selector), "", nil
}

func buildEvalArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.JS == "" {
		return nil, "", fmt.Errorf("js is required for eval command")
	}
	return append(append([]string(nil), prefix...), "eval", a.JS), "", nil
}

func buildIsArgs(prefix []string, a browserArgs) (args []string, screenshotPath string, err error) {
	if a.Selector == "" {
		return nil, "", fmt.Errorf("selector is required for %s command", a.Command)
	}
	subcmd := strings.TrimPrefix(a.Command, "is_")
	return append(append([]string(nil), prefix...), "is", subcmd, a.Selector), "", nil
}

// ScreenshotDir returns the configured screenshot directory.
func (b *BrowserTool) ScreenshotDir() string {
	return b.screenshotDir
}
