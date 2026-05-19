package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lllypuk/agent-shatrov/internal/config"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "test-token"
  allowed_users: [123]
llm:
  api_key: "test-key"
  base_url: "https://api.example.com"
  models:
    default: "model-1"
    fast: "model-2"
database:
  path: "./data/test.db"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "test-token" {
		t.Errorf("expected token 'test-token', got %q", cfg.Telegram.Token)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 123 {
		t.Errorf("unexpected allowed_users: %v", cfg.Telegram.AllowedUsers)
	}
	if cfg.LLM.APIKey != "test-key" {
		t.Errorf("expected api_key 'test-key', got %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.BaseURL != "https://api.example.com" {
		t.Errorf("unexpected base_url: %q", cfg.LLM.BaseURL)
	}
	if cfg.Database.Path != "./data/test.db" {
		t.Errorf("unexpected database path: %q", cfg.Database.Path)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	path := writeTempConfig(t, `{invalid yaml: [`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidateMissingToken(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  allowed_users: [123]
llm:
  api_key: "key"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing token")
	}
}

func TestValidateMissingAllowedUsers(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
llm:
  api_key: "key"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty allowed_users")
	}
}

func TestValidateMissingAPIKey(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing api_key")
	}
}

func TestValidateDefaultDBPath(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Database.Path == "" {
		t.Error("expected default database path to be set")
	}
}

func TestEnvOverrides(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "original-token"
  allowed_users: [1]
llm:
  api_key: "original-key"
`)
	t.Setenv("TELEGRAM_TOKEN", "env-token")
	t.Setenv("GLM_API_KEY", "legacy-env-key")
	t.Setenv("LLM_API_KEY", "env-key")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "env-token" {
		t.Errorf("expected 'env-token', got %q", cfg.Telegram.Token)
	}
	if cfg.LLM.APIKey != "env-key" {
		t.Errorf("expected 'env-key', got %q", cfg.LLM.APIKey)
	}
}

func TestEnvOverridesNotAppliedWhenEmpty(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "")
	t.Setenv("TELEGRAM_ALLOWED_USERS", "")
	t.Setenv("GLM_API_KEY", "")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("WEB_API_KEY", "")
	t.Setenv("LOG_LEVEL", "")

	path := writeTempConfig(t, `
telegram:
  token: "yaml-token"
  allowed_users: [1]
llm:
  api_key: "yaml-key"
  base_url: "https://yaml.example.com"
web:
  api_key: "yaml-web-key"
logging:
  level: "warn"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "yaml-token" {
		t.Errorf("expected 'yaml-token', got %q", cfg.Telegram.Token)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 1 {
		t.Errorf("expected allowed_users [1], got %v", cfg.Telegram.AllowedUsers)
	}
	if cfg.LLM.APIKey != "yaml-key" {
		t.Errorf("expected 'yaml-key', got %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.BaseURL != "https://yaml.example.com" {
		t.Errorf("expected YAML base_url, got %q", cfg.LLM.BaseURL)
	}
	if cfg.Web.APIKey != "yaml-web-key" {
		t.Errorf("expected YAML web api_key, got %q", cfg.Web.APIKey)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("expected YAML log level, got %q", cfg.Logging.Level)
	}
}

func TestLoadMultipleAllowedUsers(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [111, 222, 333]
llm:
  api_key: "key"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 3 {
		t.Errorf("expected 3 allowed users, got %d", len(cfg.Telegram.AllowedUsers))
	}
}

func TestEnvOverridesAppliedBeforeValidation(t *testing.T) {
	path := writeTempConfig(t, `
telegram: {}
llm: {}
`)
	t.Setenv("TELEGRAM_TOKEN", "env-token")
	t.Setenv("TELEGRAM_ALLOWED_USERS", "123456789")
	t.Setenv("LLM_API_KEY", "env-key")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "env-token" {
		t.Errorf("expected 'env-token', got %q", cfg.Telegram.Token)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 123456789 {
		t.Errorf("unexpected allowed_users: %v", cfg.Telegram.AllowedUsers)
	}
	if cfg.LLM.APIKey != "env-key" {
		t.Errorf("expected 'env-key', got %q", cfg.LLM.APIKey)
	}
}

func TestEnvOverrideTelegramAllowedUsersCSV(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
`)
	t.Setenv("TELEGRAM_ALLOWED_USERS", "123456789,987654321")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 2 || cfg.Telegram.AllowedUsers[0] != 123456789 || cfg.Telegram.AllowedUsers[1] != 987654321 {
		t.Errorf("unexpected allowed_users: %v", cfg.Telegram.AllowedUsers)
	}
}

func TestEnvOverrideTelegramAllowedUsersSingleValue(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1, 2]
llm:
  api_key: "key"
`)
	t.Setenv("TELEGRAM_ALLOWED_USERS", "123456789")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telegram.AllowedUsers) != 1 || cfg.Telegram.AllowedUsers[0] != 123456789 {
		t.Errorf("unexpected allowed_users: %v", cfg.Telegram.AllowedUsers)
	}
}

func TestEnvOverridesDockerFields(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://yaml.example.com"
web:
  api_key: "yaml-web-key"
logging:
  level: "warn"
`)
	t.Setenv("LLM_BASE_URL", "https://env.example.com")
	t.Setenv("WEB_API_KEY", "env-web-key")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.BaseURL != "https://env.example.com" {
		t.Errorf("expected env base_url, got %q", cfg.LLM.BaseURL)
	}
	if cfg.Web.APIKey != "env-web-key" {
		t.Errorf("expected env web api_key, got %q", cfg.Web.APIKey)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected env log level, got %q", cfg.Logging.Level)
	}
}

func TestProactiveConfigParsed(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
proactive:
  enabled: true
  max_messages_per_day: 5
  morning_briefing: "08:30"
  evening_checkin: "22:00"
  weekly_report: "19:00"
  goal_reminder_days_idle: 4
  timezone: "Europe/Moscow"
  city: "Москва"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := cfg.Proactive
	if !p.Enabled {
		t.Error("Enabled: want true")
	}
	if p.MaxMessagesPerDay != 5 {
		t.Errorf("MaxMessagesPerDay: got %d, want 5", p.MaxMessagesPerDay)
	}
	if p.MorningBriefingTime != "08:30" {
		t.Errorf("MorningBriefingTime: got %q, want \"08:30\"", p.MorningBriefingTime)
	}
	if p.EveningCheckinTime != "22:00" {
		t.Errorf("EveningCheckinTime: got %q, want \"22:00\"", p.EveningCheckinTime)
	}
	if p.WeeklyReportTime != "19:00" {
		t.Errorf("WeeklyReportTime: got %q, want \"19:00\"", p.WeeklyReportTime)
	}
	if p.GoalReminderDaysIdle != 4 {
		t.Errorf("GoalReminderDaysIdle: got %d, want 4", p.GoalReminderDaysIdle)
	}
	if p.Timezone != "Europe/Moscow" {
		t.Errorf("Timezone: got %q, want \"Europe/Moscow\"", p.Timezone)
	}
	if p.City != "Москва" {
		t.Errorf("City: got %q, want \"Москва\"", p.City)
	}
}

func TestProactiveConfigDefaults(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := cfg.Proactive
	if p.Enabled {
		t.Error("Enabled: want false by default")
	}
	if p.MorningBriefingTime != "" {
		t.Errorf("MorningBriefingTime: want empty by default, got %q", p.MorningBriefingTime)
	}
}

func TestProactiveConfigFinancialTickers(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
proactive:
  financial_tickers:
    - "IMOEX"
    - "RTSI"
    - "sp500"
    - "nasdaq"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tickers := cfg.Proactive.FinancialTickers
	if len(tickers) != 4 {
		t.Fatalf("expected 4 tickers, got %d: %v", len(tickers), tickers)
	}
	if tickers[0] != "IMOEX" || tickers[1] != "RTSI" || tickers[2] != "sp500" || tickers[3] != "nasdaq" {
		t.Errorf("unexpected tickers: %v", tickers)
	}
}

func TestStocksConfigProxy(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
stocks:
  enabled: true
  proxy: "http://127.0.0.1:2080"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Stocks.Proxy != "http://127.0.0.1:2080" {
		t.Errorf("Stocks.Proxy: got %q, want \"http://127.0.0.1:2080\"", cfg.Stocks.Proxy)
	}
}
