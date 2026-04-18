package main

import (
	"context"
	"fmt"
	"log/slog"
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

	llmProvider := buildLLMProvider(cfg, logger)

	toolRegistry := tools.NewRegistry(logger)
	toolRegistry.Register(tools.NewNotesTool(db))
	toolRegistry.Register(tools.NewRemindersTool(db))
	toolRegistry.Register(tools.NewDisciplineTool(db))
	toolRegistry.Register(tools.NewGoalsTool(db))
	toolRegistry.Register(tools.NewWorkoutTool(db))
	registerOptionalTools(cfg, toolRegistry, logger, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ag := agent.New(llmProvider, cfg.LLM.Models.Default, cfg.LLM.MaxIterations, toolRegistry, db, logger)
	setupAgent(ag, cfg)

	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.AllowedUsers, ag, logger)
	if err != nil {
		logger.Error("create telegram bot", "error", err)
		os.Exit(1)
	}
	setupBot(bot, cfg, toolRegistry)

	bot.SetNotificationHandler()
	bot.SetStatusInfo(startTime, cfg.Database.Path, db)

	go runReminderScheduler(ctx, db, bot.GetNotifier(), logger)
	go runProactiveScheduler(ctx, cfg.Proactive, ag, bot.GetNotifier(), cfg.Telegram.AllowedUsers, db, cfg.Weather.Enabled, logger)

	if cfg.News.Enabled {
		if _, nm := setupNews(ctx, cfg, db, toolRegistry, logger); nm != nil {
			go fetchNewsLoop(ctx, nm, logger)
		}
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
	if cfg.Files.Enabled {
		if tool, ok := registry.Get("files"); ok {
			if ft, ok := tool.(*tools.FilesTool); ok {
				bot.SetFilesTool(ft, cfg.Files)
			}
		}
	}
}

// buildLLMProvider creates the appropriate LLM provider:
// - MultiClient with fallback when llm.providers is configured
// - Single Client for backward-compatible llm.base_url + llm.api_key config
func buildLLMProvider(cfg *config.Config, logger *slog.Logger) llm.Provider {
	if len(cfg.LLM.Providers) == 0 {
		logger.Info("using single LLM provider", "base_url", cfg.LLM.BaseURL)
		return llm.NewClient(cfg.LLM.BaseURL, cfg.LLM.APIKey)
	}

	providers := make([]llm.ProviderClientConfig, 0, len(cfg.LLM.Providers))
	for _, p := range cfg.LLM.Providers {
		var opts []llm.ClientOption
		if len(p.ExtraHeaders) > 0 {
			opts = append(opts, llm.WithExtraHeaders(p.ExtraHeaders))
		}
		if p.Proxy != "" {
			opts = append(opts, llm.WithProxy(p.Proxy))
		}
		if p.TimeoutSeconds > 0 {
			opts = append(opts, llm.WithTimeout(time.Duration(p.TimeoutSeconds)*time.Second))
		}

		models := make(map[string]string)
		if p.Models.Default != "" {
			models["default"] = p.Models.Default
		}
		if p.Models.Fast != "" {
			models["fast"] = p.Models.Fast
		}

		providers = append(providers, llm.ProviderClientConfig{
			Name:     p.Name,
			Client:   llm.NewClient(p.BaseURL, p.APIKey, opts...),
			Models:   models,
			Priority: p.Priority,
		})

		logger.Info("LLM provider configured",
			"name", p.Name,
			"base_url", p.BaseURL,
			"priority", p.Priority,
		)
	}

	multiCfg := llm.MultiClientConfig{}
	if cfg.LLM.Retry.MaxRetries > 0 {
		multiCfg.MaxRetries = cfg.LLM.Retry.MaxRetries
	}
	if cfg.LLM.Retry.InitialBackoffMs > 0 {
		multiCfg.InitialBackoff = time.Duration(cfg.LLM.Retry.InitialBackoffMs) * time.Millisecond
	}
	if cfg.LLM.Retry.MaxBackoffMs > 0 {
		multiCfg.MaxBackoff = time.Duration(cfg.LLM.Retry.MaxBackoffMs) * time.Millisecond
	}

	logger.Info("multi-provider LLM enabled", "providers", len(providers))
	return llm.NewMultiClient(providers, multiCfg, logger)
}
