package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/llm"
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
	logLevel := slog.LevelInfo
	switch cfg.Logging.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}

	var logHandler slog.Handler
	if cfg.Logging.File != "" {
		os.MkdirAll(filepath.Dir(cfg.Logging.File), 0755)
		f, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening log file: %s\n", err)
			os.Exit(1)
		}
		defer f.Close()
		logHandler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: logLevel})
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	}
	logger := slog.New(logHandler)

	// Open database
	os.MkdirAll(filepath.Dir(cfg.Database.Path), 0755)
	db, err := storage.Open(cfg.Database.Path)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Create LLM client
	llmClient := llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey)

	// Setup tools
	toolRegistry := tools.NewRegistry()
	toolRegistry.Register(tools.NewNotesTool(db))

	if cfg.Web.Enabled {
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
		toolRegistry.Register(tools.NewWebSearchTool(tools.WebSearchConfig{
			Provider:       cfg.Web.Provider,
			APIKey:         cfg.Web.APIKey,
			MaxResults:     maxResults,
			TimeoutSeconds: timeout,
		}))
		toolRegistry.Register(tools.NewWebFetchTool(tools.WebFetchConfig{
			TimeoutSeconds: timeout,
			MaxSizeKB:      maxFetchKB,
		}))
		logger.Info("Web tools enabled", "max_results", maxResults)
	}

	if cfg.Shell.Enabled {
		shellTool := tools.NewShellTool(tools.ShellConfig{
			AllowedCommands: cfg.Shell.AllowedCommands,
			BlockedPatterns: cfg.Shell.BlockedPatterns,
			WorkingDir:      cfg.Shell.WorkingDir,
			TimeoutSeconds:  cfg.Shell.Timeout,
		})
		toolRegistry.Register(shellTool)
		logger.Info("Shell tool enabled", "allowed_commands", len(cfg.Shell.AllowedCommands))
	}

	// Create agent
	ag := agent.New(llmClient, cfg.LLM.Models.Default, toolRegistry, db, logger)

	// Create and run Telegram bot
	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, ag, logger)
	if err != nil {
		logger.Error("create telegram bot", "error", err)
		os.Exit(1)
	}

	// Graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
