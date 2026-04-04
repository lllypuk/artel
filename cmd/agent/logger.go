package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/lllypuk/agent-shatrov/internal/config"
)

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
