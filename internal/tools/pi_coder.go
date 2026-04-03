package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// PiCoderTool запускает pi (AI coding agent) для модификации исходного кода агента.
type PiCoderTool struct {
	projectDir   string
	model        string
	timeout      time.Duration
	restartFunc  func()        // вызывается в goroutine после успешной пересборки
	restartDelay time.Duration // задержка перед перезапуском (default: 5s)
}

// PiCoderConfig содержит конфигурацию инструмента pi_coder.
type PiCoderConfig struct {
	ProjectDir     string
	Model          string
	TimeoutSeconds int
	RestartFunc    func()        // callback для перезапуска процесса агента
	RestartDelay   time.Duration // задержка перед перезапуском; если 0 — используется 5s
}

// NewPiCoderTool создаёт инструмент для запуска pi.
func NewPiCoderTool(cfg PiCoderConfig) *PiCoderTool {
	model := cfg.Model
	if model == "" {
		model = "glm-4.7"
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	restartDelay := cfg.RestartDelay
	if restartDelay == 0 {
		restartDelay = 5 * time.Second
	}
	return &PiCoderTool{
		projectDir:   cfg.ProjectDir,
		model:        model,
		timeout:      timeout,
		restartFunc:  cfg.RestartFunc,
		restartDelay: restartDelay,
	}
}

type piCoderArgs struct {
	Task    string `json:"task"`
	Rebuild bool   `json:"rebuild"`
	Restart bool   `json:"restart"`
}

func (p *PiCoderTool) Name() string { return "pi_coder" }

func (p *PiCoderTool) Description() string {
	return "Запустить pi (AI coding agent) для доработки исходного кода самого агента. " +
		"Используй этот инструмент когда нужно: добавить новую функцию, исправить баг, " +
		"изменить логику или рефакторить код агента. " +
		"Параметр rebuild=true пересобирает бинарник после изменений (make build). " +
		"Параметр restart=true перезапускает агента после успешной пересборки (автоматически включает rebuild). " +
		"Используй restart=true чтобы применить изменения без ручного make restart. " +
		"ВНИМАНИЕ: изменения применяются к реальному коду — формулируй задачу точно."
}

func (p *PiCoderTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Задача для pi — подробное описание того, что нужно изменить в коде агента",
			},
			"rebuild": map[string]any{
				"type":        "boolean",
				"description": "Пересобрать агента после изменений кода (выполнить make build). По умолчанию false.",
			},
			"restart": map[string]any{
				"type":        "boolean",
				"description": "Перезапустить агента после успешной пересборки (автоматически включает rebuild=true). Агент будет недоступен несколько секунд, затем возобновит работу.",
			},
		},
		"required": []string{"task"},
	}
}

func (p *PiCoderTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a piCoderArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if a.Task == "" {
		return "", fmt.Errorf("task is required")
	}

	// Запускаем pi в non-interactive режиме
	piCtx, piCancel := context.WithTimeout(ctx, p.timeout)
	defer piCancel()

	//nolint:gosec // task приходит от доверенного пользователя (whitelist Telegram)
	cmd := exec.CommandContext(piCtx, "pi",
		"--print",
		"--model", p.model,
		"--no-session",
		a.Task,
	)
	cmd.Dir = p.projectDir

	output, err := cmd.CombinedOutput()
	result := string(output)

	if piCtx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("pi timed out after %s", p.timeout)
	}
	if err != nil {
		// pi может вернуть ненулевой код, но всё равно выполнить задачу
		return fmt.Sprintf("pi завершился с кодом ошибки (%v)\n\n%s", err, result), nil
	}

	// restart требует rebuild
	if a.Restart {
		a.Rebuild = true
	}

	// Опциональная пересборка
	if a.Rebuild {
		buildOutput, buildOK := p.runBuild()
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

	// Обрезаем очень большой вывод
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
func (p *PiCoderTool) runBuild() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "make", "build")
	cmd.Dir = p.projectDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("--- Сборка завершилась с ошибкой: %v ---\n%s", err, output), false
	}
	return fmt.Sprintf("--- Сборка успешна ---\n%s", output), true
}
