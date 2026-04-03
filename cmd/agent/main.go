package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/news"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/telegram"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func main() {
	// Determine config path
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	// Load config
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	// Setup logger
	logger, logCloser, err := setupLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up logger: %s\n", err)
		os.Exit(1)
	}
	if logCloser != nil {
		defer logCloser() //nolint:errcheck
	}

	// Open database
	if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0o750); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating data directory: %s\n", err)
		os.Exit(1)
	}
	db, err := storage.Open(cfg.Database.Path)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	// Create LLM client
	llmClient := llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey)

	// Setup tools
	toolRegistry := tools.NewRegistry(logger)
	toolRegistry.Register(tools.NewNotesTool(db))
	registerOptionalTools(cfg, toolRegistry, logger, db)

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start news fetcher if enabled
	var newsTool *tools.NewsTool
	if cfg.News.Enabled {
		nt, nm := setupNews(ctx, cfg, db, toolRegistry, logger)
		if nt != nil {
			newsTool = nt
			go fetchNewsLoop(ctx, nm, logger)
		}
	}

	// Create agent
	ag := agent.New(llmClient, cfg.LLM.Models.Default, cfg.LLM.MaxIterations, toolRegistry, db, logger)

	// Register named model aliases for /model command
	modelAliases := map[string]string{
		"default": cfg.LLM.Models.Default,
	}
	if cfg.LLM.Models.Fast != "" {
		modelAliases["fast"] = cfg.LLM.Models.Fast
	}
	ag.SetModels(modelAliases)

	// Create and run Telegram bot
	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.AllowedUsers, ag, newsTool, logger)
	if err != nil {
		logger.Error("create telegram bot", "error", err)
		os.Exit(1)
	}

	// Set up browser tool for bot
	if cfg.Browser.Enabled {
		if tool, ok := toolRegistry.Get("browser"); ok {
			if bt, ok := tool.(*tools.BrowserTool); ok {
				bot.SetBrowserTool(bt)
			}
		}
	}

	// Set up notification handler for long-running operations
	bot.SetNotificationHandler()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("agent-shatrov starting",
		"model", cfg.LLM.Models.Default,
		"db", cfg.Database.Path,
	)

	if err := bot.Run(ctx); err != nil && err != context.Canceled {
		logger.Error("bot stopped with error", "error", err)
		os.Exit(1)
	}

	logger.Info("agent-shatrov stopped")
}

// setupLogger создаёт slog.Logger согласно конфигурации.
// Возвращает логгер, функцию закрытия файла (или nil) и ошибку.
func setupLogger(cfg *config.Config) (*slog.Logger, func() error, error) {
	logLevel := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: logLevel}

	if cfg.Logging.File == "" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts)), nil, nil
	}

	if err := os.MkdirAll(filepath.Dir(cfg.Logging.File), 0o750); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}
	return slog.New(slog.NewTextHandler(f, opts)), f.Close, nil
}

// registerOptionalTools регистрирует инструменты, включённые в конфиге.
func registerOptionalTools(cfg *config.Config, registry *tools.Registry, logger *slog.Logger, db *storage.DB) {
	if cfg.Web.Enabled {
		registerWebTools(cfg, registry, logger)
	}

	if cfg.PiCoder.Enabled {
		registerPiCoderTool(cfg, registry, logger)
	}

	if cfg.Shell.Enabled {
		registry.Register(tools.NewShellTool(tools.ShellConfig{
			AllowedCommands: cfg.Shell.AllowedCommands,
			BlockedPatterns: cfg.Shell.BlockedPatterns,
			WorkingDir:      cfg.Shell.WorkingDir,
			TimeoutSeconds:  cfg.Shell.Timeout,
		}))
		logger.Info("Shell tool enabled", "allowed_commands", len(cfg.Shell.AllowedCommands))
	}
}

func registerWebTools(cfg *config.Config, registry *tools.Registry, logger *slog.Logger) {
	maxResults := cfg.Web.MaxResults
	if maxResults == 0 {
		maxResults = 5
	}
	timeout := cfg.Web.TimeoutSeconds
	if timeout == 0 {
		timeout = 15
	}
	maxFetchKB := cfg.Web.MaxFetchSizeKB
	if maxFetchKB == 0 {
		maxFetchKB = 100
	}
	registry.Register(tools.NewWebSearchTool(tools.WebSearchConfig{
		Provider:       cfg.Web.Provider,
		APIKey:         cfg.Web.APIKey,
		MaxResults:     maxResults,
		TimeoutSeconds: timeout,
	}))
	registry.Register(tools.NewWebFetchTool(tools.WebFetchConfig{
		TimeoutSeconds: timeout,
		MaxSizeKB:      maxFetchKB,
	}))

	// Register browser tool if enabled
	if cfg.Browser.Enabled {
		browserTimeout := cfg.Browser.TimeoutSeconds
		if browserTimeout == 0 {
			browserTimeout = 30
		}
		registry.Register(tools.NewBrowserTool(tools.BrowserConfig{
			ScreenshotDir:    cfg.Browser.ScreenshotDir,
			AgentBrowserPath: cfg.Browser.AgentBrowserPath,
			TimeoutSeconds:   browserTimeout,
			Proxy:            cfg.Browser.Proxy,
		}))
		logger.Info("Browser tool enabled", "screenshot_dir", cfg.Browser.ScreenshotDir)
	}

	logger.Info("Web tools enabled", "max_results", maxResults)
}

func registerPiCoderTool(cfg *config.Config, registry *tools.Registry, logger *slog.Logger) {
	projectDir := cfg.PiCoder.ProjectDir
	if projectDir == "" {
		projectDir = "."
	}
	model := cfg.PiCoder.Model
	if model == "" {
		model = "glm-4.7"
	}
	registry.Register(tools.NewPiCoderTool(tools.PiCoderConfig{
		ProjectDir:     projectDir,
		Model:          model,
		TimeoutSeconds: cfg.PiCoder.TimeoutSeconds,
		RestartFunc:    makeRestartFunc(logger),
	}))
	logger.Info("PiCoder tool enabled", "project_dir", projectDir, "model", model)
}

// makeRestartFunc создаёт функцию безопасного перезапуска агента.
// Использует syscall.Exec: текущий процесс атомарно заменяется новым бинарником без fork.
// PID остаётся прежним, перенаправления stdin/stdout/stderr сохраняются.
func makeRestartFunc(logger *slog.Logger) func() {
	return func() {
		executable, err := os.Executable()
		if err != nil {
			logger.Error("restart: cannot get executable path", "error", err)
			return
		}
		// Разрешаем символические ссылки, чтобы получить реальный путь к бинарнику
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			logger.Error("restart: eval symlinks failed", "error", err)
			return
		}
		logger.Info("restarting agent via syscall.Exec",
			"executable", executable,
			"args", os.Args,
		)
		// syscall.Exec заменяет текущий процесс — возврат из этой функции невозможен
		// executable получен через os.Executable() + EvalSymlinks — не внешний ввод
		//nolint:gosec // G702: путь зафиксирован из os.Executable() — не является пользовательским вводом
		if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
			logger.Error("restart: syscall.Exec failed", "error", err)
		}
	}
}

func setupNews(ctx context.Context, cfg *config.Config, db *storage.DB, registry *tools.Registry, logger *slog.Logger) (*tools.NewsTool, *news.NewsManager) {
	newsRepo := storage.NewNewsRepository(db)

	// Initialize feeds from configuration
	if err := initFeeds(ctx, cfg, newsRepo, logger); err != nil {
		logger.Error("init feeds", "error", err)
	}

	parser := news.NewGofeedParser()

	timeout := time.Duration(cfg.News.FetchTimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	fetcher := news.NewHTTPFetcher(timeout)

	newsManager := news.NewNewsManager(newsRepo, parser, fetcher, cfg.News.Proxy, logger)
	nt := tools.NewNewsTool(newsManager)
	registry.Register(nt)

	logger.Info("News tool registered")
	return nt, newsManager
}

func initFeeds(ctx context.Context, cfg *config.Config, newsRepo storage.NewsRepository, logger *slog.Logger) error {
	// Get all feeds from database
	dbFeeds, err := newsRepo.ListFeeds(ctx, 1000, 0)
	if err != nil {
		return fmt.Errorf("list feeds from database: %w", err)
	}

	// Create a map of URL -> Feed for quick lookup
	dbFeedsMap := make(map[string]*news.Feed)
	for i := range dbFeeds {
		dbFeedsMap[dbFeeds[i].URL] = &dbFeeds[i]
	}

	// Map of feeds in config by URL
	configFeedsMap := make(map[string]*config.FeedConfig)
	for i := range cfg.News.Feeds {
		configFeedsMap[cfg.News.Feeds[i].URL] = &cfg.News.Feeds[i]
	}

	// Track changes for logging
	changes := &feedChanges{}

	// Process feeds from configuration
	for i := range cfg.News.Feeds {
		feedCfg := &cfg.News.Feeds[i]
		existing, existsInDB := dbFeedsMap[feedCfg.URL]

		// Determine feed type
		feedType := determineFeedType(feedCfg.URL)

		if !existsInDB {
			if added := addFeed(ctx, newsRepo, *feedCfg, feedType); added {
				changes.add(feedCfg.Title, feedCfg.URL)
			}
		} else {
			if updated := updateFeedIfNeeded(ctx, newsRepo, existing, *feedCfg); updated {
				changes.update(feedCfg.Title, feedCfg.URL)
			}
		}
	}

	// Delete feeds that are not in configuration
	for _, dbFeed := range dbFeeds {
		if _, existsInConfig := configFeedsMap[dbFeed.URL]; !existsInConfig {
			if deleted := deleteFeed(ctx, newsRepo, dbFeed); deleted {
				changes.del(dbFeed.Title, dbFeed.URL)
			}
		}
	}

	// Log changes
	changes.log(logger, len(dbFeeds))

	return nil
}

type feedChanges struct {
	added   []string
	updated []string
	deleted []string
}

func (c *feedChanges) add(title, url string) {
	c.added = append(c.added, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) update(title, url string) {
	c.updated = append(c.updated, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) del(title, url string) {
	c.deleted = append(c.deleted, fmt.Sprintf("%s (%s)", title, url))
}

func (c *feedChanges) log(logger *slog.Logger, totalFeeds int) {
	if len(c.added) > 0 {
		logger.Info("feeds added from configuration", "count", len(c.added))
		for _, f := range c.added {
			logger.Info("  - added", "feed", f)
		}
	}

	if len(c.updated) > 0 {
		logger.Info("feeds updated from configuration", "count", len(c.updated))
		for _, f := range c.updated {
			logger.Info("  - updated", "feed", f)
		}
	}

	if len(c.deleted) > 0 {
		logger.Info("feeds deleted (not in configuration)", "count", len(c.deleted))
		for _, f := range c.deleted {
			logger.Info("  - deleted", "feed", f)
		}
	}

	if len(c.added) == 0 && len(c.updated) == 0 && len(c.deleted) == 0 {
		logger.Info("feeds configuration is up to date", "total", totalFeeds)
	}
}

func determineFeedType(url string) news.FeedType {
	switch {
	case strings.Contains(url, ".atom"):
		return news.FeedTypeAtom
	case strings.Contains(url, ".json"):
		return news.FeedTypeJSON
	default:
		return news.FeedTypeRSS
	}
}

func addFeed(ctx context.Context, repo storage.NewsRepository, feedCfg config.FeedConfig, feedType news.FeedType) bool {
	feed := &news.Feed{
		URL:           feedCfg.URL,
		Title:         feedCfg.Title,
		Description:   feedCfg.Title,
		Type:          feedType,
		Category:      feedCfg.Category,
		Language:      feedCfg.Language,
		Enabled:       feedCfg.Enabled,
		ProxyURL:      feedCfg.Proxy,
		FetchInterval: time.Hour,
	}

	_, err := repo.CreateFeed(ctx, feed)
	return err == nil
}

func updateFeedIfNeeded(ctx context.Context, repo storage.NewsRepository, existing *news.Feed, feedCfg config.FeedConfig) bool {
	needUpdate := existing.Title != feedCfg.Title ||
		existing.Category != feedCfg.Category ||
		existing.Language != feedCfg.Language ||
		existing.Enabled != feedCfg.Enabled ||
		existing.ProxyURL != feedCfg.Proxy

	if !needUpdate {
		return false
	}

	existing.Title = feedCfg.Title
	existing.Description = feedCfg.Title
	existing.Category = feedCfg.Category
	existing.Language = feedCfg.Language
	existing.Enabled = feedCfg.Enabled
	existing.ProxyURL = feedCfg.Proxy

	err := repo.UpdateFeed(ctx, existing)
	return err == nil
}

func deleteFeed(ctx context.Context, repo storage.NewsRepository, feed news.Feed) bool {
	err := repo.DeleteFeed(ctx, feed.ID)
	return err == nil
}

func fetchNewsLoop(ctx context.Context, manager *news.NewsManager, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	// Initial fetch
	if err := manager.FetchAllFeeds(ctx); err != nil {
		logger.Error("initial fetch all feeds", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			logger.Info("news fetch loop stopped")
			return
		case <-ticker.C:
			if err := manager.FetchAllFeeds(ctx); err != nil {
				logger.Error("fetch all feeds", "error", err)
			}
		}
	}
}
