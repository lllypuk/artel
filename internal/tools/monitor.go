package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/storage"
)

// MonitorTool provides site availability monitoring via HTTP ping.
type MonitorTool struct {
	sites  []config.MonitorSite
	db     *storage.DB
	client *http.Client
}

// MonitorToolConfig holds constructor parameters.
type MonitorToolConfig struct {
	Sites          []config.MonitorSite
	TimeoutSeconds int
	Proxy          string
}

// NewMonitorTool creates a new MonitorTool.
func NewMonitorTool(cfg MonitorToolConfig, db *storage.DB) *MonitorTool {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	transport := &http.Transport{}
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}

	return &MonitorTool{
		sites: cfg.Sites,
		db:    db,
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}
}

func (m *MonitorTool) Name() string { return "monitor" }

func (m *MonitorTool) Description() string {
	return "Monitor site availability. " +
		"Actions: 'check' — ping one or all configured sites; " +
		"'list' — show all sites with current status; " +
		"'history' — last N checks for a site."
}

func (m *MonitorTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"check", "list", "history"},
				"description": "Action: check (ping sites), list (current statuses), history (check log)",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Site URL to check or show history for. Omit to target all sites.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Number of history entries to return (default: 20)",
				"minimum":     1,
				"maximum":     100,
			},
		},
		"required": []string{"action"},
	}
}

type monitorArgs struct {
	Action string `json:"action"`
	URL    string `json:"url"`
	Limit  int    `json:"limit"`
}

// Execute dispatches to the appropriate action handler.
func (m *MonitorTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a monitorArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	switch a.Action {
	case "check":
		return m.execCheck(ctx, a.URL)
	case "list":
		return m.execList(ctx)
	case "history":
		return m.execHistory(ctx, a.URL, a.Limit)
	default:
		return "", fmt.Errorf("unknown action %q; use check, list, history", a.Action)
	}
}

// ─── check ────────────────────────────────────────────────────────────────────

func (m *MonitorTool) execCheck(ctx context.Context, targetURL string) (string, error) {
	sites := m.targetSites(targetURL)
	if len(sites) == 0 {
		return "Нет сайтов для проверки. Добавьте сайты в конфиг (monitor.sites).", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "🔍 Проверка %d сайтов...\n\n", len(sites))

	for _, site := range sites {
		result := m.pingSite(ctx, site)
		if err := m.saveCheck(ctx, result); err != nil {
			// non-fatal: log but continue
			_ = err
		}
		writeCheckResult(&sb, result)
	}
	return sb.String(), nil
}

// pingSite performs an HTTP GET and returns the check result.
func (m *MonitorTool) pingSite(ctx context.Context, site config.MonitorSite) storage.MonitorCheck {
	start := time.Now()
	result := storage.MonitorCheck{
		SiteURL:   site.URL,
		SiteName:  site.Name,
		CheckedAt: start,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, site.URL, http.NoBody)
	if err != nil {
		result.IsUp = false
		result.ErrorText = err.Error()
		return result
	}

	resp, err := m.client.Do(req)
	result.ResponseMS = time.Since(start).Milliseconds()

	if err != nil {
		result.IsUp = false
		result.ErrorText = err.Error()
		return result
	}
	defer resp.Body.Close()               //nolint:errcheck
	_, _ = io.Copy(io.Discard, resp.Body) // drain body

	result.StatusCode = resp.StatusCode
	result.IsUp = resp.StatusCode < 500
	if !result.IsUp {
		result.ErrorText = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return result
}

func (m *MonitorTool) saveCheck(ctx context.Context, c storage.MonitorCheck) error {
	return m.db.InsertMonitorCheck(ctx, c)
}

func writeCheckResult(sb *strings.Builder, c storage.MonitorCheck) {
	icon := "✅"
	if !c.IsUp {
		icon = "❌"
	}
	name := c.SiteName
	if name == "" {
		name = c.SiteURL
	}
	fmt.Fprintf(sb, "%s %s (%s)\n", icon, name, c.SiteURL)
	if c.StatusCode > 0 {
		fmt.Fprintf(sb, "   HTTP %d • %d мс\n", c.StatusCode, c.ResponseMS)
	} else {
		fmt.Fprintf(sb, "   %d мс • %s\n", c.ResponseMS, c.ErrorText)
	}
}

// ─── list ─────────────────────────────────────────────────────────────────────

func (m *MonitorTool) execList(ctx context.Context) (string, error) {
	if len(m.sites) == 0 {
		return "Нет настроенных сайтов. Добавьте сайты в конфиг (monitor.sites).", nil
	}

	states, err := m.db.GetAllMonitorStates(ctx)
	if err != nil {
		return "", fmt.Errorf("get monitor states: %w", err)
	}
	stateMap := buildStateMap(states)

	var sb strings.Builder
	fmt.Fprintf(&sb, "📡 Мониторинг сайтов (%d)\n\n", len(m.sites))

	for _, site := range m.sites {
		writeSiteStatus(&sb, site, stateMap[site.URL])
	}
	return sb.String(), nil
}

func buildStateMap(states []storage.MonitorState) map[string]storage.MonitorState {
	m := make(map[string]storage.MonitorState, len(states))
	for _, s := range states {
		m[s.SiteURL] = s
	}
	return m
}

func writeSiteStatus(sb *strings.Builder, site config.MonitorSite, st storage.MonitorState) {
	name := site.Name
	if name == "" {
		name = site.URL
	}
	if st.SiteURL == "" {
		fmt.Fprintf(sb, "⬜ %s — нет данных\n   %s\n", name, site.URL)
		return
	}
	icon := "✅"
	if !st.IsUp {
		icon = "❌"
	}
	ago := time.Since(st.LastCheckedAt).Round(time.Second)
	fmt.Fprintf(sb, "%s %s\n   %s\n", icon, name, site.URL)
	if st.IsUp {
		fmt.Fprintf(sb, "   HTTP %d • %d мс • проверено %s назад\n",
			st.LastStatusCode, st.LastResponseMS, ago)
	} else {
		fmt.Fprintf(sb, "   ❗ %s • проверено %s назад\n", st.LastError, ago)
	}
}

// ─── history ─────────────────────────────────────────────────────────────────

func (m *MonitorTool) execHistory(ctx context.Context, targetURL string, limit int) (string, error) {
	if targetURL == "" && len(m.sites) > 0 {
		targetURL = m.sites[0].URL
	}
	if targetURL == "" {
		return "Укажите url сайта.", nil
	}
	if limit <= 0 {
		limit = 20
	}

	checks, err := m.db.GetMonitorHistory(ctx, targetURL, limit)
	if err != nil {
		return "", fmt.Errorf("get history: %w", err)
	}
	if len(checks) == 0 {
		return fmt.Sprintf("Нет истории проверок для %s.", targetURL), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "📜 История проверок: %s (последние %d)\n\n", targetURL, len(checks))
	for _, c := range checks {
		writeHistoryEntry(&sb, c)
	}
	return sb.String(), nil
}

func writeHistoryEntry(sb *strings.Builder, c storage.MonitorCheck) {
	icon := "✅"
	if !c.IsUp {
		icon = "❌"
	}
	ts := c.CheckedAt.Format("02.01 15:04:05")
	if c.IsUp {
		fmt.Fprintf(sb, "%s %s — HTTP %d • %d мс\n", icon, ts, c.StatusCode, c.ResponseMS)
	} else {
		fmt.Fprintf(sb, "%s %s — %s\n", icon, ts, c.ErrorText)
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// targetSites returns the sites to check: all if targetURL is empty, or just the matching one.
func (m *MonitorTool) targetSites(targetURL string) []config.MonitorSite {
	if targetURL == "" {
		return m.sites
	}
	for _, s := range m.sites {
		if s.URL == targetURL {
			return []config.MonitorSite{s}
		}
	}
	// URL not in config — check it anyway as ad-hoc
	return []config.MonitorSite{{URL: targetURL, Name: targetURL}}
}
