package config_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
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

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	defer func() {
		os.Stderr = oldStderr
	}()

	os.Stderr = w
	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}
	output, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}

	return string(output)
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
	// A missing config file is tolerated; validation will fail because required fields are absent.
	_, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected validation error when no config and no env vars")
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
  base_url: "https://api.example.com"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing token")
	}
	if !strings.Contains(err.Error(), "token") {
		t.Errorf("expected error to mention 'token', got: %v", err)
	}
}

func TestValidateMissingAllowedUsers(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty allowed_users")
	}
	if !strings.Contains(err.Error(), "allowed_users") {
		t.Errorf("expected error to mention 'allowed_users', got: %v", err)
	}
}

func TestValidateMissingAPIKey(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  base_url: "https://api.example.com"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing api_key")
	}
	if !strings.Contains(err.Error(), "api_key") {
		t.Errorf("expected error to mention 'api_key', got: %v", err)
	}
}

func TestValidateDefaultDBPath(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
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
  base_url: "https://api.example.com"
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

func TestEnvOverrideLegacyGLMAPIKey(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "original-token"
  allowed_users: [1]
llm:
  api_key: "original-key"
  base_url: "https://api.example.com"
`)
	t.Setenv("GLM_API_KEY", "legacy-env-key")
	t.Setenv("LLM_API_KEY", "")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLM.APIKey != "legacy-env-key" {
		t.Errorf("expected 'legacy-env-key', got %q", cfg.LLM.APIKey)
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
  base_url: "https://api.example.com"
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
	t.Setenv("LLM_BASE_URL", "https://api.example.com")

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
  base_url: "https://api.example.com"
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
  base_url: "https://api.example.com"
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

func TestEnvOverrideTelegramAllowedUsersInvalidKeepsOriginal(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1, 2]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	t.Setenv("TELEGRAM_ALLOWED_USERS", "123,abc")

	var cfg *config.Config
	stderr := captureStderr(t, func() {
		var err error
		cfg, err = config.Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if len(cfg.Telegram.AllowedUsers) != 2 || cfg.Telegram.AllowedUsers[0] != 1 || cfg.Telegram.AllowedUsers[1] != 2 {
		t.Errorf("expected original allowed_users [1 2], got %v", cfg.Telegram.AllowedUsers)
	}
	if !strings.Contains(stderr, `TELEGRAM_ALLOWED_USERS: invalid value "abc", skipping env override`) {
		t.Fatalf("expected warning about invalid TELEGRAM_ALLOWED_USERS, got %q", stderr)
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
  base_url: "https://api.example.com"
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
  base_url: "https://api.example.com"
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
  base_url: "https://api.example.com"
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
  base_url: "https://api.example.com"
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

func TestLoadMissingFileWithEnvVars(t *testing.T) {
	t.Setenv("TELEGRAM_TOKEN", "env-token")
	t.Setenv("TELEGRAM_ALLOWED_USERS", "123456789")
	t.Setenv("LLM_API_KEY", "env-key")
	t.Setenv("LLM_BASE_URL", "https://api.example.com")

	cfg, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("env-only startup should succeed when file is missing: %v", err)
	}
	if cfg.Telegram.Token != "env-token" {
		t.Errorf("expected 'env-token', got %q", cfg.Telegram.Token)
	}
	if cfg.LLM.BaseURL != "https://api.example.com" {
		t.Errorf("expected base_url from env, got %q", cfg.LLM.BaseURL)
	}
}

func TestValidateMissingBaseURL(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing base_url")
	}
	if !strings.Contains(err.Error(), "base_url") {
		t.Errorf("expected error to mention 'base_url', got: %v", err)
	}
}

func TestMattermostConfig(t *testing.T) {
	path := writeTempConfig(t, `
transport:
  type: mattermost
mattermost:
  server_url: "https://mm.example.com"
  token: "mm-token"
  bot_username: "analyst-bot"
  owner_user_id: "owner-1"
  team_bots: ["dev-bot", "test-bot"]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
database:
  path: "./data/test.db"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Transport.Type != "mattermost" {
		t.Errorf("expected transport 'mattermost', got %q", cfg.Transport.Type)
	}
	if cfg.Mattermost.ServerURL != "https://mm.example.com" {
		t.Errorf("unexpected server_url: %q", cfg.Mattermost.ServerURL)
	}
	if cfg.Mattermost.Token != "mm-token" {
		t.Errorf("unexpected token: %q", cfg.Mattermost.Token)
	}
	if cfg.Mattermost.BotUsername != "analyst-bot" {
		t.Errorf("unexpected bot_username: %q", cfg.Mattermost.BotUsername)
	}
	if cfg.Mattermost.OwnerUserID != "owner-1" {
		t.Errorf("unexpected owner_user_id: %q", cfg.Mattermost.OwnerUserID)
	}
	if len(cfg.Mattermost.TeamBots) != 2 || cfg.Mattermost.TeamBots[0] != "dev-bot" {
		t.Errorf("unexpected team_bots: %v", cfg.Mattermost.TeamBots)
	}
}

func TestMattermostValidationRequiresFields(t *testing.T) {
	path := writeTempConfig(t, `
transport:
  type: mattermost
mattermost:
  server_url: "https://mm.example.com"
  bot_username: "bot"
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing mattermost.token")
	}
	if !strings.Contains(err.Error(), "mattermost.token") {
		t.Errorf("expected error to mention 'mattermost.token', got: %v", err)
	}
}

func TestMattermostValidationRequiresBotUsername(t *testing.T) {
	path := writeTempConfig(t, `
transport:
  type: mattermost
mattermost:
  server_url: "https://mm.example.com"
  token: "tok"
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing mattermost.bot_username")
	}
	if !strings.Contains(err.Error(), "mattermost.bot_username") {
		t.Errorf("expected error to mention 'mattermost.bot_username', got: %v", err)
	}
}

func TestRoleConfigParsed(t *testing.T) {
	path := writeTempConfig(t, `
transport:
  type: mattermost
mattermost:
  server_url: "https://mm.example.com"
  token: "mm-token"
  bot_username: "analyst-bot"
  owner_user_id: "owner-1"
  team_bots: ["dev-bot", "test-bot"]
role:
  name: "analyst"
  system_prompt: "Ты — аналитик команды."
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Role.Name != "analyst" {
		t.Errorf("Role.Name: got %q, want \"analyst\"", cfg.Role.Name)
	}
	if cfg.Role.SystemPrompt != "Ты — аналитик команды." {
		t.Errorf("Role.SystemPrompt: got %q, want custom prompt", cfg.Role.SystemPrompt)
	}
}

func TestRoleConfigDefaults(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Role.Name != "agent" {
		t.Errorf("Role.Name default: got %q, want \"agent\"", cfg.Role.Name)
	}
	if cfg.Role.SystemPrompt != "" {
		t.Errorf("Role.SystemPrompt default: got %q, want empty", cfg.Role.SystemPrompt)
	}
}

func TestLLMAPIKeyWarningInMultiProviderMode(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  providers:
    - name: "prov1"
      base_url: "https://api.example.com"
      api_key: "prov-key"
      priority: 1
`)
	t.Setenv("LLM_API_KEY", "top-level-key")

	var cfg *config.Config
	stderr := captureStderr(t, func() {
		var err error
		cfg, err = config.Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	if cfg == nil {
		t.Fatal("expected config to be loaded")
	}
	if !strings.Contains(stderr, "LLM_API_KEY") {
		t.Errorf("expected warning about LLM_API_KEY in multi-provider mode, got stderr: %q", stderr)
	}
}

func TestPiCoderProjectsParsed(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
  model: "glm-4.7"
  timeout: 300
  projects_dir: "/home/projects"
  exclude_projects:
    - "test-project"
    - "nanoGPT"
  projects:
    - name: "agent-shatrov"
      path: "."
    - name: "my-project"
      path: "/path/to/my-project"
      model: "glm-4.7-flash"
      timeout: 600
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.PiCoder.Enabled {
		t.Error("PiCoder.Enabled: want true")
	}
	if len(cfg.PiCoder.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(cfg.PiCoder.Projects))
	}
	p0 := cfg.PiCoder.Projects[0]
	if p0.Name != "agent-shatrov" {
		t.Errorf("Projects[0].Name: got %q, want 'agent-shatrov'", p0.Name)
	}
	if p0.Path != "." {
		t.Errorf("Projects[0].Path: got %q, want '.'", p0.Path)
	}
	p1 := cfg.PiCoder.Projects[1]
	if p1.Name != "my-project" {
		t.Errorf("Projects[1].Name: got %q, want 'my-project'", p1.Name)
	}
	if p1.Path != "/path/to/my-project" {
		t.Errorf("Projects[1].Path: got %q, want '/path/to/my-project'", p1.Path)
	}
	if p1.Model != "glm-4.7-flash" {
		t.Errorf("Projects[1].Model: got %q, want 'glm-4.7-flash'", p1.Model)
	}
	if p1.TimeoutSec != 600 {
		t.Errorf("Projects[1].TimeoutSec: got %d, want 600", p1.TimeoutSec)
	}
	if cfg.PiCoder.ProjectsDir != "/home/projects" {
		t.Errorf("ProjectsDir: got %q, want '/home/projects'", cfg.PiCoder.ProjectsDir)
	}
	if len(cfg.PiCoder.ExcludeProjects) != 2 || cfg.PiCoder.ExcludeProjects[0] != "test-project" || cfg.PiCoder.ExcludeProjects[1] != "nanoGPT" {
		t.Errorf("ExcludeProjects: got %v, want [test-project nanoGPT]", cfg.PiCoder.ExcludeProjects)
	}
}

func TestPiCoderEnabledWithoutProjectsError(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for pi_coder enabled without projects")
	}
	if !strings.Contains(err.Error(), "pi_coder.projects") {
		t.Errorf("expected error to mention 'pi_coder.projects', got: %v", err)
	}
	if !strings.Contains(err.Error(), "projects_dir") {
		t.Errorf("expected error to mention 'projects_dir', got: %v", err)
	}
}

func TestPiCoderEnabledWithProjectsDirOnlyOK(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
  projects_dir: "/some/path"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PiCoder.ProjectsDir != "/some/path" {
		t.Errorf("ProjectsDir: got %q, want '/some/path'", cfg.PiCoder.ProjectsDir)
	}
}

func TestPiCoderProjectRequiresName(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
  projects:
    - path: "/some/path"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for project without name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("expected error to mention 'name is required', got: %v", err)
	}
}

func TestPiCoderProjectRequiresPath(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
  projects:
    - name: "my-project"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for project without path")
	}
	if !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected error to mention 'path is required', got: %v", err)
	}
}

func TestPiCoderDuplicateProjectNamesError(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: true
  projects:
    - name: "dup"
      path: "/path/a"
    - name: "dup"
      path: "/path/b"
`)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for duplicate project names")
	}
	if !strings.Contains(err.Error(), "not unique") {
		t.Errorf("expected error to mention 'not unique', got: %v", err)
	}
}

func TestPiCoderDisabledWithoutProjectsOK(t *testing.T) {
	path := writeTempConfig(t, `
telegram:
  token: "tok"
  allowed_users: [1]
llm:
  api_key: "key"
  base_url: "https://api.example.com"
pi_coder:
  enabled: false
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PiCoder.Enabled {
		t.Error("PiCoder.Enabled: want false")
	}
	if len(cfg.PiCoder.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(cfg.PiCoder.Projects))
	}
}
