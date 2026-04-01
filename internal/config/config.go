package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	LLM      LLMConfig      `yaml:"llm"`
	Database DatabaseConfig `yaml:"database"`
	Shell    ShellConfig    `yaml:"shell"`
	Web      WebConfig      `yaml:"web"`
	PiCoder  PiCoderConfig  `yaml:"pi_coder"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type TelegramConfig struct {
	Token        string  `yaml:"token"`
	AllowedUsers []int64 `yaml:"allowed_users"`
}

type LLMConfig struct {
	Provider string    `yaml:"provider"`
	Models   LLMModels `yaml:"models"`
	BaseURL  string    `yaml:"base_url"`
	APIKey   string    `yaml:"api_key"`
}

type LLMModels struct {
	Default string `yaml:"default"`
	Fast    string `yaml:"fast"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type ShellConfig struct {
	Enabled         bool     `yaml:"enabled"`
	Timeout         int      `yaml:"timeout"`
	AllowedCommands []string `yaml:"allowed_commands"`
	BlockedPatterns []string `yaml:"blocked_patterns"`
	WorkingDir      string   `yaml:"working_dir"`
}

type WebConfig struct {
	Enabled        bool   `yaml:"enabled"`
	Provider       string `yaml:"provider"` // brave, serper, duckduckgo
	APIKey         string `yaml:"api_key"`
	MaxResults     int    `yaml:"max_results"`
	TimeoutSeconds int    `yaml:"timeout"`
	MaxFetchSizeKB int    `yaml:"max_fetch_size_kb"`
}

type PiCoderConfig struct {
	Enabled        bool   `yaml:"enabled"`
	ProjectDir     string `yaml:"project_dir"`
	Model          string `yaml:"model"`
	TimeoutSeconds int    `yaml:"timeout"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	// Override with env vars if set
	cfg.applyEnvOverrides()

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Telegram.Token == "" {
		return fmt.Errorf("telegram.token is required")
	}
	if len(c.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("telegram.allowed_users must have at least one user")
	}
	if c.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
	}
	if c.Database.Path == "" {
		c.Database.Path = "./data/agent.db"
	}
	return nil
}

func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("TELEGRAM_TOKEN"); v != "" {
		c.Telegram.Token = v
	}
	if v := os.Getenv("GLM_API_KEY"); v != "" {
		c.LLM.APIKey = v
	}
}
