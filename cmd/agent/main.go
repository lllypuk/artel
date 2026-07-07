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
	"github.com/lllypuk/agent-shatrov/internal/mattermost"
	"github.com/lllypuk/agent-shatrov/internal/safeguards"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/telegram"
	"github.com/lllypuk/agent-shatrov/internal/telemetry"
	"github.com/lllypuk/agent-shatrov/internal/tools"
	"github.com/lllypuk/agent-shatrov/internal/transport"
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

	// Initialize OpenTelemetry tracing.
	var otelShutdown func(context.Context) error
	if cfg.Otel.Endpoint != "" {
		serviceName := cfg.Otel.ServiceName
		if serviceName == "" {
			serviceName = "agent-shatrov"
		}
		shutdown, err := telemetry.InitOTLP(cfg.Otel.Endpoint, serviceName)
		if err != nil {
			logger.Warn("OTel OTLP initialization failed, using noop", "error", err)
			otelShutdown = telemetry.ShutdownNoop()
		} else {
			otelShutdown = shutdown
			logger.Info("OTel tracing enabled", "endpoint", cfg.Otel.Endpoint, "service", serviceName)
		}
	} else {
		telemetry.Init()
		otelShutdown = telemetry.ShutdownNoop()
	}
	defer telemetry.ShutdownWithTimeout(otelShutdown, 5*time.Second)

	startTime := time.Now()

	llmProvider := buildLLMProvider(cfg, logger)

	toolRegistry := tools.NewRegistry(logger)
	registerNotesTool(cfg, toolRegistry, logger, db)
	toolRegistry.Register(tools.NewRemindersTool(db))
	toolRegistry.Register(tools.NewDisciplineTool(db))
	toolRegistry.Register(tools.NewGoalsTool(db))
	toolRegistry.Register(tools.NewWorkoutTool(db))
	toolRegistry.Register(tools.NewWikiTool(db))
	toolRegistry.Register(tools.NewReadingListTool(db, tools.NewDefaultWebFetchTool()))
	toolRegistry.Register(tools.NewKnowledgeSearchTool(db))
	registerOptionalTools(cfg, toolRegistry, logger, db)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ag := agent.New(llmProvider, cfg.LLM.Models.Default, cfg.LLM.MaxIterations, toolRegistry, db, logger)
	setupAgent(ag, cfg)

	guard := safeguards.New(safeguards.Config{
		WarnThreshold:       cfg.Safeguards.LoopWarnBotMessages,
		SuppressThreshold:   cfg.Safeguards.LoopSuppressBotMessages,
		MaxRepliesPerMinute: cfg.Safeguards.MaxRepliesPerMinute,
	})

	tr, err := buildTransport(ctx, cfg, ag, guard, db, toolRegistry, startTime, cancel, logger)
	if err != nil {
		logger.Error("build transport", "error", err)
		os.Exit(1)
	}

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
		"transport", cfg.Transport.Type,
		"role", cfg.Role.Name,
	)

	_ = tr // transport Run loop was started inside buildTransport

	<-ctx.Done()

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

// setupAgent configures model aliases, context window and role/persona on the agent.
func setupAgent(ag *agent.Agent, cfg *config.Config) {
	modelAliases := map[string]string{"default": cfg.LLM.Models.Default}
	if cfg.LLM.Models.Fast != "" {
		modelAliases["fast"] = cfg.LLM.Models.Fast
	}
	ag.SetModels(modelAliases)
	if cfg.LLM.MaxContextMessages > 0 {
		ag.SetMaxContextMessages(cfg.LLM.MaxContextMessages)
	}
	if cfg.Role.SystemPrompt != "" {
		ag.SetSystemPrompt(cfg.Role.SystemPrompt)
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

// buildTransport selects, initializes, and starts the configured message
// transport. Both Telegram and Mattermost follow the same pattern: the
// transport's Run loop and background schedulers are launched here, so the
// caller only needs to wait on ctx.Done().
func buildTransport(ctx context.Context, cfg *config.Config, ag *agent.Agent, guard *safeguards.Guard, db *storage.DB, registry *tools.Registry, startTime time.Time, cancel context.CancelFunc, logger *slog.Logger) (transport.Transport, error) {
	switch cfg.Transport.Type {
	case "telegram", "":
		return buildTelegramTransport(ctx, cfg, ag, guard, db, registry, startTime, cancel, logger)
	case "mattermost":
		return buildMattermostTransport(ctx, cfg, ag, guard, db, cancel, logger)
	default:
		return nil, fmt.Errorf("unknown transport type %q", cfg.Transport.Type)
	}
}

func buildTelegramTransport(ctx context.Context, cfg *config.Config, ag *agent.Agent, guard *safeguards.Guard, db *storage.DB, registry *tools.Registry, startTime time.Time, cancel context.CancelFunc, logger *slog.Logger) (transport.Transport, error) {
	bot, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.AllowedUsers, ag, logger)
	if err != nil {
		return nil, fmt.Errorf("create telegram bot: %w", err)
	}
	bot.SetSafeguards(guard)
	setupBot(bot, cfg, registry)

	bot.SetNotificationHandler()
	bot.SetStatusInfo(startTime, cfg.Database.Path, db)

	notifier := bot.GetNotifier()
	startBackgroundLoops(ctx, cfg, ag, db, notifier, cfg.Telegram.AllowedUsers, logger)

	tr := telegram.NewTransport(bot)
	go func() {
		if err := tr.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("telegram transport stopped with error", "error", err)
			cancel()
		}
	}()
	return tr, nil
}

func buildMattermostTransport(ctx context.Context, cfg *config.Config, ag *agent.Agent, guard *safeguards.Guard, db *storage.DB, cancel context.CancelFunc, logger *slog.Logger) (transport.Transport, error) {
	mmTr, err := mattermost.NewTransport(cfg.Mattermost.ServerURL, cfg.Mattermost.Token, cfg.Mattermost.Proxy, logger)
	if err != nil {
		return nil, fmt.Errorf("create mattermost transport: %w", err)
	}
	if err := mmTr.Identify(ctx); err != nil {
		return nil, fmt.Errorf("identify mattermost bot: %w", err)
	}
	guard.SetSelfID(mmTr.SelfID())

	if cfg.Mattermost.BotUsername != "" &&
		!strings.EqualFold(cfg.Mattermost.BotUsername, mmTr.SelfUsername()) {
		logger.Warn("mattermost: configured bot_username does not match actual username; using actual",
			"configured", cfg.Mattermost.BotUsername, "actual", mmTr.SelfUsername())
	}

	ownerIDs := make([]string, 0, 1)
	if cfg.Mattermost.OwnerUserID != "" {
		ownerIDs = append(ownerIDs, cfg.Mattermost.OwnerUserID)
	}
	authGate := transport.NewAuthGate(transport.AuthConfig{
		SelfID:             mmTr.SelfID(),
		SelfUsername:       mmTr.SelfUsername(),
		OwnerIDs:           ownerIDs,
		TeamBots:           cfg.Mattermost.TeamBots,
		StrictBotWhitelist: true,
	})

	ag.SetNotifier(mattermostNotifier(ctx, mmTr, db, logger))

	// Proactive schedulers and monitor target Telegram user IDs; in Mattermost
	// mode there are none, so these loops would run without delivering anything.
	// Skip them rather than spin idle goroutines.
	startMattermostBackgroundLoops(ctx, cfg, ag, db, ag.GetNotifier(), logger)

	go func() {
		if err := mmTr.Run(ctx); err != nil && err != context.Canceled {
			logger.Error("mattermost transport stopped with error", "error", err)
			cancel()
		}
	}()
	go runMattermostDispatcher(ctx, mmTr, ag, authGate, guard, db, logger)

	return mmTr, nil
}

// startBackgroundLoops starts reminder, proactive digest and monitor loops
// for Telegram. These loops deliver notifications to a fixed set of human user
// IDs and are not meaningful in Mattermost mode.
func startBackgroundLoops(ctx context.Context, cfg *config.Config, ag *agent.Agent, db *storage.DB, notifier tools.Notifier, allowedUsers []int64, logger *slog.Logger) {
	go runReminderScheduler(ctx, db, notifier, logger)
	go runProactiveScheduler(ctx, cfg.Proactive, ag, notifier, allowedUsers, db, cfg.Weather.Enabled, cfg.News.Enabled, cfg.Stocks.Enabled, logger)
	startMonitorLoop(ctx, &cfg.Monitor, db, notifier, allowedUsers, logger)
}

// startMattermostBackgroundLoops starts only the loops that are transport-
// agnostic. Reminder scheduler delivers to any notifier (including the
// Mattermost notifier), so it runs in both modes. Proactive digests and the
// system monitor target Telegram user IDs and are skipped in Mattermost mode.
func startMattermostBackgroundLoops(ctx context.Context, cfg *config.Config, ag *agent.Agent, db *storage.DB, notifier tools.Notifier, logger *slog.Logger) {
	go runReminderScheduler(ctx, db, notifier, logger)
}
