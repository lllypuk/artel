package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram   TelegramConfig   `yaml:"telegram"`
	Proactive  ProactiveConfig  `yaml:"proactive"`
	LLM        LLMConfig        `yaml:"llm"`
	Database   DatabaseConfig   `yaml:"database"`
	Shell      ShellConfig      `yaml:"shell"`
	Web        WebConfig        `yaml:"web"`
	Browser    BrowserConfig    `yaml:"browser"`
	PiCoder    PiCoderConfig    `yaml:"pi_coder"`
	News       NewsConfig       `yaml:"news"`
	Files      FilesConfig      `yaml:"files"`
	CodeRunner CodeRunnerConfig `yaml:"code_runner"`
	Weather    WeatherConfig    `yaml:"weather"`
	Stocks     StocksConfig     `yaml:"stocks"`
	Logging    LoggingConfig    `yaml:"logging"`
}

type TelegramConfig struct {
	Token        string  `yaml:"token"`
	AllowedUsers []int64 `yaml:"allowed_users"`
	Proxy        string  `yaml:"proxy"` // e.g. "http://127.0.0.1:2080"
}

type LLMConfig struct {
	Provider           string              `yaml:"provider"`
	Models             LLMModels           `yaml:"models"`
	BaseURL            string              `yaml:"base_url"`
	APIKey             string              `yaml:"api_key"`
	MaxIterations      int                 `yaml:"max_iterations"`       // ReAct loop limit (default: 10)
	MaxContextMessages int                 `yaml:"max_context_messages"` // context window limit (default: 50, 0=disabled)
	Providers          []LLMProviderConfig `yaml:"providers"`            // multi-provider fallback
	Retry              LLMRetryConfig      `yaml:"retry"`                // retry / backoff settings
}

type LLMModels struct {
	Default string `yaml:"default"`
	Fast    string `yaml:"fast"`
}

// LLMProviderConfig describes a single LLM provider for multi-provider fallback.
type LLMProviderConfig struct {
	Name           string            `yaml:"name"`
	BaseURL        string            `yaml:"base_url"`
	APIKey         string            `yaml:"api_key"`
	Priority       int               `yaml:"priority"` // lower = tried first
	Models         LLMModels         `yaml:"models"`
	ExtraHeaders   map[string]string `yaml:"extra_headers"`   // e.g. HTTP-Referer, X-Title for OpenRouter
	Proxy          string            `yaml:"proxy"`           // HTTP proxy for this provider
	TimeoutSeconds int               `yaml:"timeout_seconds"` // HTTP client timeout (default: 120)
}

// LLMRetryConfig holds retry and backoff settings for multi-provider fallback.
type LLMRetryConfig struct {
	MaxRetries       int `yaml:"max_retries"`        // retries per provider (default: 2)
	InitialBackoffMs int `yaml:"initial_backoff_ms"` // initial backoff in ms (default: 500)
	MaxBackoffMs     int `yaml:"max_backoff_ms"`     // max backoff in ms (default: 5000)
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

// FilesConfig holds settings for the file-upload feature.
type FilesConfig struct {
	Enabled           bool     `yaml:"enabled"`
	StorageDir        string   `yaml:"storage_dir"`        // default: ./data/files
	MaxFileSizeKB     int      `yaml:"max_file_size_kb"`   // default: 10240 (10 MB)
	AllowedExtensions []string `yaml:"allowed_extensions"` // default: see validate()
	VisionEnabled     bool     `yaml:"vision_enabled"`     // send images to LLM as base64
}

// CodeRunnerConfig holds settings for the code execution tool.
type CodeRunnerConfig struct {
	Enabled        bool     `yaml:"enabled"`
	TimeoutSeconds int      `yaml:"timeout_seconds"` // default: 30
	MemLimitMB     int      `yaml:"mem_limit_mb"`    // 0 = no limit
	EnabledLangs   []string `yaml:"enabled_langs"`   // empty = all (python, go, bash)
}

// WeatherConfig holds settings for the weather forecast tool (Open-Meteo).
type WeatherConfig struct {
	Enabled        bool   `yaml:"enabled"`
	TimeoutSeconds int    `yaml:"timeout_seconds"` // default: 15
	Proxy          string `yaml:"proxy"`           // optional proxy override, e.g. "http://127.0.0.1:2080"
}

// StocksConfig holds settings for the stocks market data tool.
type StocksConfig struct {
	Enabled         bool   `yaml:"enabled"`
	CacheTTLMinutes int    `yaml:"cache_ttl_minutes"` // default: 15, 0 = no cache
	TimeoutSeconds  int    `yaml:"timeout_seconds"`   // default: 15
	Proxy           string `yaml:"proxy"`             // optional proxy override, e.g. "http://127.0.0.1:2080"
}

// ProactiveConfig holds settings for the proactive messaging scheduler.
type ProactiveConfig struct {
	Enabled              bool     `yaml:"enabled"`
	MaxMessagesPerDay    int      `yaml:"max_messages_per_day"`    // default: 7
	MorningBriefingTime  string   `yaml:"morning_briefing"`        // "08:00" or "" to disable
	EveningCheckinTime   string   `yaml:"evening_checkin"`         // "21:00" or "" to disable
	WeeklyReportTime     string   `yaml:"weekly_report"`           // "20:00" (Sunday only) or ""
	GoalReminderDaysIdle int      `yaml:"goal_reminder_days_idle"` // default: 3
	Timezone             string   `yaml:"timezone"`                // e.g. "Europe/Moscow", "" = local
	City                 string   `yaml:"city"`                    // for weather in morning briefing
	FinancialTickers     []string `yaml:"financial_tickers"`       // tickers for morning/evening finance digest
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
	// api_key is required only when providers are not configured.
	if len(c.LLM.Providers) == 0 && c.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required (or configure llm.providers)")
	}
	// Validate provider configs.
	for i, p := range c.LLM.Providers {
		if p.BaseURL == "" {
			return fmt.Errorf("llm.providers[%d].base_url is required", i)
		}
		if p.APIKey == "" {
			return fmt.Errorf("llm.providers[%d].api_key is required", i)
		}
	}
	if c.Database.Path == "" {
		c.Database.Path = "./data/agent.db"
	}
	if c.LLM.MaxContextMessages < 0 {
		c.LLM.MaxContextMessages = 0
	}
	if c.Files.StorageDir == "" {
		c.Files.StorageDir = "./data/files"
	}
	if c.Files.MaxFileSizeKB <= 0 {
		c.Files.MaxFileSizeKB = 10240 // 10 MB
	}
	if len(c.Files.AllowedExtensions) == 0 {
		c.Files.AllowedExtensions = []string{
			"txt", "md", "json", "csv", "pdf",
			"jpg", "jpeg", "png", "gif", "webp",
		}
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
