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
	t.Setenv("GLM_API_KEY", "env-key")

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
	t.Setenv("GLM_API_KEY", "")

	path := writeTempConfig(t, `
telegram:
  token: "yaml-token"
  allowed_users: [1]
llm:
  api_key: "yaml-key"
`)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "yaml-token" {
		t.Errorf("expected 'yaml-token', got %q", cfg.Telegram.Token)
	}
	if cfg.LLM.APIKey != "yaml-key" {
		t.Errorf("expected 'yaml-key', got %q", cfg.LLM.APIKey)
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
