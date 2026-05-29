package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"syscall"

	"github.com/lllypuk/agent-shatrov/internal/config"
	"github.com/lllypuk/agent-shatrov/internal/storage"
	"github.com/lllypuk/agent-shatrov/internal/tools"
)

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

	if cfg.Files.Enabled {
		registry.Register(tools.NewFilesTool(cfg.Files.StorageDir))
		logger.Info("Files tool enabled", "storage_dir", cfg.Files.StorageDir)
	}

	if cfg.CodeRunner.Enabled {
		registry.Register(tools.NewCodeRunnerTool(tools.CodeRunnerConfig{
			TimeoutSeconds: cfg.CodeRunner.TimeoutSeconds,
			MemLimitMB:     cfg.CodeRunner.MemLimitMB,
			EnabledLangs:   cfg.CodeRunner.EnabledLangs,
		}))
		logger.Info("CodeRunner tool enabled",
			"timeout", cfg.CodeRunner.TimeoutSeconds,
			"mem_limit_mb", cfg.CodeRunner.MemLimitMB,
			"langs", cfg.CodeRunner.EnabledLangs,
		)
	}

	registerWeatherTool(cfg, registry, logger)
	registerStocksTool(cfg, registry, logger, db)
	registerMonitorTool(cfg, registry, logger, db)
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

func registerStocksTool(cfg *config.Config, registry *tools.Registry, logger *slog.Logger, db *storage.DB) {
	if !cfg.Stocks.Enabled {
		return
	}
	cacheTTL := cfg.Stocks.CacheTTLMinutes
	if cacheTTL == 0 {
		cacheTTL = 15
	}
	timeout := cfg.Stocks.TimeoutSeconds
	if timeout == 0 {
		timeout = 15
	}
	registry.Register(tools.NewStocksTool(tools.StocksConfig{
		Enabled:         true,
		CacheTTLMinutes: cacheTTL,
		TimeoutSeconds:  timeout,
		Proxy:           cfg.Stocks.Proxy,
	}, db, logger))
	logger.Info("Stocks tool enabled", "cache_ttl_minutes", cacheTTL, "timeout", timeout)
}

// registerNotesTool регистрирует инструмент заметок с опциональным embedding.
func registerNotesTool(cfg *config.Config, registry *tools.Registry, logger *slog.Logger, db *storage.DB) {
	if cfg.Notes.Embedding.Enabled {
		model := cfg.Notes.Embedding.Model
		if model == "" {
			model = "nomic-embed-text"
		}
		ollamaURL := cfg.Notes.Embedding.OllamaURL
		if ollamaURL == "" {
			ollamaURL = "http://localhost:11434"
		}
		embedder := tools.NewOllamaEmbedder(tools.OllamaEmbedderConfig{
			BaseURL: ollamaURL,
			Model:   model,
		})
		registry.Register(tools.NewNotesToolWithEmbedder(db, embedder))
		logger.Info("Notes tool enabled with semantic search",
			"model", model,
			"ollama_url", ollamaURL,
		)
	} else {
		registry.Register(tools.NewNotesTool(db))
	}
}

func registerWeatherTool(cfg *config.Config, registry *tools.Registry, logger *slog.Logger) {
	if !cfg.Weather.Enabled {
		return
	}
	timeout := cfg.Weather.TimeoutSeconds
	if timeout == 0 {
		timeout = 15
	}
	registry.Register(tools.NewWeatherTool(tools.WeatherConfig{
		TimeoutSeconds: timeout,
		Proxy:          cfg.Weather.Proxy,
	}))
	logger.Info("Weather tool enabled", "timeout", timeout)
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

func registerMonitorTool(cfg *config.Config, registry *tools.Registry, logger *slog.Logger, db *storage.DB) {
	if !cfg.Monitor.Enabled || len(cfg.Monitor.Sites) == 0 {
		return
	}
	timeout := cfg.Monitor.TimeoutSeconds
	if timeout == 0 {
		timeout = 10
	}
	registry.Register(tools.NewMonitorTool(tools.MonitorToolConfig{
		Sites:          cfg.Monitor.Sites,
		TimeoutSeconds: timeout,
		Proxy:          cfg.Monitor.Proxy,
	}, db))
	logger.Info("Monitor tool enabled", "sites", len(cfg.Monitor.Sites), "interval_min", cfg.Monitor.IntervalMinutes)
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
