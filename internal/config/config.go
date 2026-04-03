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
	Browser  BrowserConfig  `yaml:"browser"`
	PiCoder  PiCoderConfig  `yaml:"pi_coder"`
	News     NewsConfig     `yaml:"news"`
	Logging  LoggingConfig  `yaml:"logging"`
}

type TelegramConfig struct {
	Token        string  `yaml:"token"`
	AllowedUsers []int64 `yaml:"allowed_users"`
	Proxy        string  `yaml:"proxy"` // e.g. "http://127.0.0.1:2080"
}

type LLMConfig struct {
	Provider           string    `yaml:"provider"`
	Models             LLMModels `yaml:"models"`
	BaseURL            string    `yaml:"base_url"`
	APIKey             string    `yaml:"api_key"`
	MaxIterations      int       `yaml:"max_iterations"`       // ReAct loop limit (default: 10)
	MaxContextMessages int       `yaml:"max_context_messages"` // context window limit (default: 50, 0=disabled)
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

// BrowserConfig holds configuration for the universal browser automation tool.
type BrowserConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ScreenshotDir    string `yaml:"screenshot_dir"`     // Directory for screenshots (default: ./data/screenshots)
	AgentBrowserPath string `yaml:"agent_browser_path"` // Path to agent-browser binary (default: agent-browser)
	TimeoutSeconds   int    `yaml:"timeout_seconds"`    // Timeout for browser operations (default: 30s)
	Proxy            string `yaml:"proxy"`              // Proxy URL, e.g. "http://127.0.0.1:2080"
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

type NewsConfig struct {
	Enabled              bool         `yaml:"enabled"`
	FetchIntervalMinutes int          `yaml:"fetch_interval_minutes"`
	MaxItemAgeDays       int          `yaml:"max_item_age_days"`
	MaxItemsPerFeed      int          `yaml:"max_items_per_feed"`
	FetchTimeoutSeconds  int          `yaml:"fetch_timeout_seconds"`
	Proxy                string       `yaml:"proxy"` // default proxy for all feeds, e.g. "http://127.0.0.1:2080"
	Feeds                []FeedConfig `yaml:"feeds"`
}

type FeedConfig struct {
	URL      string `yaml:"url"`
	Title    string `yaml:"title"`
	Category string `yaml:"category"`
	Language string `yaml:"language"`
	Enabled  bool   `yaml:"enabled"`
	Proxy    string `yaml:"proxy"` // per-feed proxy, e.g. "http://127.0.0.1:2080"
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
	if c.LLM.MaxContextMessages < 0 {
		c.LLM.MaxContextMessages = 0
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
