package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

// startMonitorLoop launches the background site-monitoring goroutine.
// It pings all configured sites at the configured interval and sends
// Telegram alerts when a site changes state (up→down or down→up).
func startMonitorLoop(
	ctx context.Context,
	cfg *config.MonitorConfig,
	db *storage.DB,
	notifier tools.Notifier,
	allowedUsers []int64,
	logger *slog.Logger,
) {
	if !cfg.Enabled || len(cfg.Sites) == 0 {
		return
	}

	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval == 0 {
		interval = 5 * time.Minute
	}

	client := buildMonitorHTTPClient(cfg)

	logger.Info("monitor loop started",
		"sites", len(cfg.Sites),
		"interval", interval,
	)

	go runMonitorLoop(ctx, cfg, db, notifier, allowedUsers, client, interval, logger)
}

func runMonitorLoop(
	ctx context.Context,
	cfg *config.MonitorConfig,
	db *storage.DB,
	notifier tools.Notifier,
	allowedUsers []int64,
	client *http.Client,
	interval time.Duration,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Run immediately on start, then on every tick.
	checkAllSites(ctx, cfg, db, notifier, allowedUsers, client, logger)

	for {
		select {
		case <-ctx.Done():
			logger.Info("monitor loop stopped")
			return
		case <-ticker.C:
			checkAllSites(ctx, cfg, db, notifier, allowedUsers, client, logger)
		}
	}
}

func checkAllSites(
	ctx context.Context,
	cfg *config.MonitorConfig,
	db *storage.DB,
	notifier tools.Notifier,
	allowedUsers []int64,
	client *http.Client,
	logger *slog.Logger,
) {
	for _, site := range cfg.Sites {
		checkOneSite(ctx, site, db, notifier, allowedUsers, client, logger)
	}
}

func checkOneSite(
	ctx context.Context,
	site config.MonitorSite,
	db *storage.DB,
	notifier tools.Notifier,
	allowedUsers []int64,
	client *http.Client,
	logger *slog.Logger,
) {
	result := pingMonitorSite(ctx, client, site)

	if err := db.InsertMonitorCheck(ctx, result); err != nil {
		logger.Error("monitor: insert check", "url", site.URL, "error", err)
	}

	prevState, err := db.GetMonitorState(ctx, site.URL)
	if err != nil {
		logger.Error("monitor: get state", "url", site.URL, "error", err)
	}

	newState := storage.MonitorState{
		SiteURL:        site.URL,
		SiteName:       site.Name,
		IsUp:           result.IsUp,
		LastCheckedAt:  result.CheckedAt,
		LastStatusCode: result.StatusCode,
		LastResponseMS: result.ResponseMS,
		LastError:      result.ErrorText,
	}
	if err := db.UpsertMonitorState(ctx, newState); err != nil {
		logger.Error("monitor: upsert state", "url", site.URL, "error", err)
	}

	stateChanged := prevState == nil || prevState.IsUp != result.IsUp
	if stateChanged && notifier != nil {
		msg := buildAlertMessage(site, result, prevState)
		sendMonitorAlert(notifier, allowedUsers, msg)
	}
}

// pingMonitorSite performs a single HTTP GET and returns the result.
func pingMonitorSite(ctx context.Context, client *http.Client, site config.MonitorSite) storage.MonitorCheck {
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

	resp, err := client.Do(req)
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

func buildAlertMessage(site config.MonitorSite, result storage.MonitorCheck, prev *storage.MonitorState) string {
	name := site.Name
	if name == "" {
		name = site.URL
	}
	if result.IsUp {
		msg := fmt.Sprintf("✅ Сайт восстановлен: %s\n%s\nHTTP %d • %d мс",
			name, site.URL, result.StatusCode, result.ResponseMS)
		if prev != nil && !prev.IsUp {
			downSince := time.Since(prev.LastCheckedAt).Round(time.Second)
			msg += fmt.Sprintf("\nНедоступен был: %s", downSince)
		}
		return msg
	}
	errInfo := result.ErrorText
	if result.StatusCode > 0 {
		errInfo = fmt.Sprintf("HTTP %d", result.StatusCode)
	}
	return fmt.Sprintf("🔴 Сайт недоступен: %s\n%s\n%s", name, site.URL, errInfo)
}

func sendMonitorAlert(notifier tools.Notifier, allowedUsers []int64, msg string) {
	for _, chatID := range allowedUsers {
		notifier(chatID, msg)
	}
}

func buildMonitorHTTPClient(cfg *config.MonitorConfig) *http.Client {
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
	return &http.Client{Timeout: timeout, Transport: transport}
}
