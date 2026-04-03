package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lllypuk/agent-shatrov/internal/agent"
	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/llm"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/telegram"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s\n", err)
		os.Exit(1)
	}

	logger, logCloser, err := setupLogger(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting up logger: %s\n", err)
		os.Exit(1)
	}
	if logCloser != nil {
		defer logCloser() //nolint:errcheck
	}

	if err := createDirectories(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating directories: %s\n", err)
		os.Exit(1)
	}

	db, err := storage.Open(cfg.Database.Path)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	startTime := time.Now()

	llmClient := llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey)

	toolRegistry := tools.NewRegistry(logger)
	toolRegistry.Register(tools.NewNotesTool(db))
	toolRegistry.Register(tools.NewRemindersTool(db))
	registerOptionalTools(cfg, toolRegistry, logger, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ag := agent.New(llmClient, cfg.LLM.Models.Default, cfg.LLM.MaxIterations, toolRegistry, db, logger)
	setupAgent(ag, cfg)

	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.AllowedUsers, ag, nil, logger)
	if err != nil {
		logger.Error("create telegram bot", "error", err)
		os.Exit(1)
	}
	setupBot(bot, cfg, toolRegistry)

	bot.SetNotificationHandler()
	bot.SetStatusInfo(startTime, cfg.Database.Path, db)

	go runReminderScheduler(ctx, db, bot.GetNotifier(), logger)

	var newsTool *tools.NewsTool
	if cfg.News.Enabled {
		nt, nm := setupNews(ctx, cfg, db, toolRegistry, logger)
		if nt != nil {
			newsTool = nt
			go fetchNewsLoop(ctx, nm, logger)
		}
	}
	if newsTool != nil {
		bot.SetNewsTool(newsTool)
	}

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

// createDirectories ensures all required data directories exist.
func createDirectories(cfg *config.Config) error {
	if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0o750); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	if cfg.Files.Enabled {
		if err := os.MkdirAll(cfg.Files.StorageDir, 0o750); err != nil {
			return fmt.Errorf("files dir: %w", err)
		}
	}
	return nil
}

// setupAgent configures model aliases and context window on the agent.
func setupAgent(ag *agent.Agent, cfg *config.Config) {
	modelAliases := map[string]string{"default": cfg.LLM.Models.Default}
	if cfg.LLM.Models.Fast != "" {
		modelAliases["fast"] = cfg.LLM.Models.Fast
	}
	ag.SetModels(modelAliases)
	if cfg.LLM.MaxContextMessages > 0 {
		ag.SetMaxContextMessages(cfg.LLM.MaxContextMessages)
	}
}

// setupBot wires optional tools to the bot after creation.
func setupBot(bot *telegram.Bot, cfg *config.Config, registry *tools.Registry) {
	if cfg.Browser.Enabled {
		if tool, ok := registry.Get("browser"); ok {
			if bt, ok := tool.(*tools.BrowserTool); ok {
				bot.SetBrowserTool(bt)
			}
		}
	}
	if cfg.Files.Enabled {
		if tool, ok := registry.Get("files"); ok {
			if ft, ok := tool.(*tools.FilesTool); ok {
				bot.SetFilesTool(ft, cfg.Files)
			}
		}
	}
}
