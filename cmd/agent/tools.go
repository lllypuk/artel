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
