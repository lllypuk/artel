package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/projects"
)

// PiCoderTool запускает pi (AI coding agent) для работы с проектами.
// Оперирует списком проектов из Registry — один из них может быть кодом самого агента (SelfProject).
type PiCoderTool struct {
	registry       *projects.Registry
	defaultModel   string
	defaultTimeout time.Duration
	restartFunc    func()        // вызывается в goroutine после успешной пересборки (только для SelfProject)
	restartDelay   time.Duration // задержка перед перезапуском (default: 5s)
}

// PiCoderConfig содержит конфигурацию инструмента pi_coder.
type PiCoderConfig struct {
	Registry       *projects.Registry
	DefaultModel   string
	DefaultTimeout time.Duration
	RestartFunc    func()        // callback для перезапуска агента (только для SelfProject)
	RestartDelay   time.Duration // задержка перед перезапуском; если 0 — 5s
}

// NewPiCoderTool создаёт инструмент для запуска pi.
func NewPiCoderTool(cfg PiCoderConfig) *PiCoderTool {
	defaultModel := cfg.DefaultModel
	if defaultModel == "" {
		defaultModel = "glm-4.7"
	}
	defaultTimeout := cfg.DefaultTimeout
	if defaultTimeout == 0 {
		defaultTimeout = 5 * time.Minute
	}
	restartDelay := cfg.RestartDelay
	if restartDelay == 0 {
		restartDelay = 5 * time.Second
	}
	return &PiCoderTool{
		registry:       cfg.Registry,
		defaultModel:   defaultModel,
		defaultTimeout: defaultTimeout,
		restartFunc:    cfg.RestartFunc,
		restartDelay:   restartDelay,
	}
}

type piCoderArgs struct {
	Project string `json:"project"`
	Task    string `json:"task"`
	Rebuild bool   `json:"rebuild"`
	Restart bool   `json:"restart"`
}

func (p *PiCoderTool) Name() string { return "pi_coder" }

func (p *PiCoderTool) Description() string {
	names := p.registry.Names()
	var desc strings.Builder
	desc.WriteString("Запустить pi (AI coding agent) для анализа, разработки или тестирования проекта. " +
		"Параметр project обязателен — выбери один из доступных проектов. " +
		"Доступные проекты: " + joinNames(names) + ". ")

	// Indicate which project is the agent itself (supports rebuild/restart).
	for _, name := range names {
		proj, err := p.registry.Get(name)
		if err != nil {
			continue
		}
		if proj.SelfProject {
			fmt.Fprintf(&desc, "Проект %q — это код самого агента: rebuild/restart доступны. ", name)
			break
		}
	}

	desc.WriteString("ВНИМАНИЕ: изменения применяются к реальному коду — формулируй задачу точно.")
	return desc.String()
}

func joinNames(names []string) string {
	if len(names) == 0 {
		return "(нет проектов)"
	}
	var result strings.Builder
	for i, n := range names {
		if i > 0 {
			result.WriteString(", ")
		}
		result.WriteString(n)
	}
	return result.String()
}

func (p *PiCoderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project": map[string]any{
				"type":        "string",
				"enum":        p.registry.Names(),
				"description": "Имя проекта для работы через pi",
			},
			"task": map[string]any{
				"type":        "string",
				"description": "Задача для pi — анализ, разработка, тестирование, рефакторинг",
			},
			"rebuild": map[string]any{
				"type":        "boolean",
				"description": "Пересобрать проект после изменений (только для self-проекта). По умолчанию false.",
			},
			"restart": map[string]any{
				"type":        "boolean",
				"description": "Перезапустить агента после пересборки (только для self-проекта, автоматически включает rebuild).",
			},
		},
		"required": []string{"project", "task"},
	}
}

func (p *PiCoderTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a piCoderArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Project == "" {
		return "", fmt.Errorf("project is required, available: %v", p.registry.Names())
	}
	if a.Task == "" {
		return "", fmt.Errorf("task is required")
	}

	proj, err := p.registry.Get(a.Project)
	if err != nil {
		return "", err
	}

	// Determine model: per-project override → default.
	model := proj.Model
	if model == "" {
		model = p.defaultModel
	}

	// Determine timeout: per-project override → default.
	timeout := p.defaultTimeout
	if proj.TimeoutSec > 0 {
		timeout = time.Duration(proj.TimeoutSec) * time.Second
	}

	// restart requires rebuild.
	if a.Restart {
		a.Rebuild = true
	}

	// For non-self projects, ignore rebuild/restart with a warning.
	warnings := ""
	if !proj.SelfProject {
		if a.Rebuild {
			warnings += "\n⚠️ rebuild игнорируется: проект не является кодом агента."
			a.Rebuild = false
		}
		if a.Restart {
			warnings += "\n⚠️ restart игнорируется: проект не является кодом агента."
			a.Restart = false
		}
	}

	// Запускаем pi в non-interactive режиме.
	piCtx, piCancel := context.WithTimeout(ctx, timeout)
	defer piCancel()

	//nolint:gosec // task comes from trusted user (whitelist Telegram / Mattermost owner)
	cmd := exec.CommandContext(piCtx, "pi",
		"--print",
		"--model", model,
		"--no-session",
		a.Task,
	)
	cmd.Dir = proj.Path

	output, err := cmd.CombinedOutput()
	result := string(output)

	if piCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("pi timed out after %s", timeout)
	}
	if err != nil {
		// pi может вернуть ненулевой код, но всё равно выполнить задачу
		result = fmt.Sprintf("pi завершился с кодом ошибки (%v)\n\n%s", err, result)
	}

	// Опциональная пересборка (только для self-проекта).
	if a.Rebuild {
		buildOutput, buildOK := p.runBuild(proj.Path)
		result += "\n\n" + buildOutput

		if a.Restart {
			switch {
			case !buildOK:
				result += "\n\n❌ Перезапуск отменён: сборка завершилась с ошибкой."
			case p.restartFunc == nil:
				result += "\n\n⚠️ Перезапуск не настроен (restartFunc отсутствует)."
			default:
				result += fmt.Sprintf("\n\n⚠️ Агент перезапускается через %s...", p.restartDelay.Round(time.Second))
				go p.scheduleRestart()
			}
		}
	}

	// Добавляем предупреждения об игнорировании rebuild/restart.
	result += warnings

	// Обрезаем очень большой вывод.
	const maxLen = 8192
	if len(result) > maxLen {
		result = result[:maxLen] + "\n... (вывод обрезан)"
	}

	return result, nil
}

// scheduleRestart ждёт restartDelay и вызывает restartFunc.
// Запускается в отдельной goroutine, чтобы дать агенту отправить ответ пользователю.
func (p *PiCoderTool) scheduleRestart() {
	time.Sleep(p.restartDelay)
	if p.restartFunc != nil {
		p.restartFunc()
	}
}

// runBuild выполняет make build в директории проекта.
// Возвращает вывод и флаг успеха.
func (p *PiCoderTool) runBuild(projectDir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", "build")
	cmd.Dir = projectDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("--- Сборка завершилась с ошибкой: %v ---\n%s", err, output), false
	}
	return fmt.Sprintf("--- Сборка успешна ---\n%s", output), true
}
