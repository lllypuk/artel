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
	ag := agent.New(llmClient, cfg.LLM.Models.Default, toolRegistry, db, logger)

	// Create and run Telegram bot
	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, ag, newsTool, logger)
	if err != nil {
		logger.Error("create telegram bot", "error", err)
		os.Exit(1)
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
	}))
	logger.Info("PiCoder tool enabled", "project_dir", projectDir, "model", model)
}

func setupNews(ctx context.Context, cfg *config.Config, db *storage.DB, registry *tools.Registry, logger *slog.Logger) (*tools.NewsTool, *news.NewsManager) {
	newsRepo := storage.NewNewsRepository(db)

	// Initialize default feeds
	if err := initDefaultFeeds(ctx, cfg, newsRepo, logger); err != nil {
		logger.Error("init default feeds", "error", err)
	}

	parser := news.NewGofeedParser()

	timeout := time.Duration(cfg.News.FetchTimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	fetcher := news.NewHTTPFetcher(timeout)

	newsManager := news.NewNewsManager(newsRepo, parser, fetcher, logger)
	nt := tools.NewNewsTool(newsManager)
	registry.Register(nt)

	logger.Info("News tool registered")
	return nt, newsManager
}

func initDefaultFeeds(ctx context.Context, cfg *config.Config, newsRepo storage.NewsRepository, logger *slog.Logger) error {
	for _, feedCfg := range cfg.News.Feeds {
		// Check if feed already exists
		existing, err := newsRepo.GetFeedByURL(ctx, feedCfg.URL)
		if err != nil {
			logger.Warn("check feed exists", "url", feedCfg.URL, "error", err)
			continue
		}
		if existing != nil {
			continue
		}

		// Create feed
		var feedType news.FeedType
		switch {
		case strings.Contains(feedCfg.URL, ".atom"):
			feedType = news.FeedTypeAtom
		case strings.Contains(feedCfg.URL, ".json"):
			feedType = news.FeedTypeJSON
		default:
			feedType = news.FeedTypeRSS
		}

		feed := &news.Feed{
			URL:           feedCfg.URL,
			Title:         feedCfg.Title,
			Description:   feedCfg.Title,
			Type:          feedType,
			Category:      feedCfg.Category,
			Language:      feedCfg.Language,
			Enabled:       feedCfg.Enabled,
			FetchInterval: time.Hour,
		}

		_, err = newsRepo.CreateFeed(ctx, feed)
		if err != nil {
			logger.Warn("create feed", "url", feedCfg.URL, "error", err)
		} else {
			logger.Info("feed created", "title", feedCfg.Title, "url", feedCfg.URL)
		}
	}
	return nil
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
