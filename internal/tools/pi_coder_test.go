package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Примеры использования PiCoderTool:
//
//   // Создание инструмента с конфигом по умолчанию
//   tool := NewPiCoderTool(PiCoderConfig{
//       ProjectDir: "/path/to/project",
//   })
//
//   // Создание инструмента с кастомной моделью и таймаутом
//   tool := NewPiCoderTool(PiCoderConfig{
//       ProjectDir:     "/path/to/project",
//       Model:          "glm-4.7-flash",
//       TimeoutSeconds: 600, // 10 минут
//   })
//
//   // Запуск pi для добавления функции (без пересборки)
//   result, err := tool.Execute(ctx, json.RawMessage(`{
//       "task": "Добавить тесты для notes.go"
//   }`))
//
//   // Запуск pi с пересборкой бинарника
//   result, err := tool.Execute(ctx, json.RawMessage(`{
//       "task": "Добавить веб-поиск",
//       "rebuild": true
//   }`))

// TestNewPiCoderTool проверяет создание инструмента с различными конфигурациями.
func TestNewPiCoderTool(t *testing.T) {
	tests := []struct {
		name        string
		config      PiCoderConfig
		wantDir     string
		wantModel   string
		wantTimeout time.Duration
	}{
		{
			name: "default values",
			config: PiCoderConfig{
				ProjectDir: "/test/project",
			},
			wantDir:     "/test/project",
			wantModel:   "glm-4.7",
			wantTimeout: 5 * time.Minute,
		},
		{
			name: "custom model",
			config: PiCoderConfig{
				ProjectDir: "/test/project",
				Model:      "glm-4.7-flash",
			},
			wantDir:     "/test/project",
			wantModel:   "glm-4.7-flash",
			wantTimeout: 5 * time.Minute,
		},
		{
			name: "custom timeout",
			config: PiCoderConfig{
				ProjectDir:     "/test/project",
				TimeoutSeconds: 120,
			},
			wantDir:     "/test/project",
			wantModel:   "glm-4.7",
			wantTimeout: 2 * time.Minute,
		},
		{
			name: "all custom values",
			config: PiCoderConfig{
				ProjectDir:     "/custom/project",
				Model:          "gpt-4",
				TimeoutSeconds: 300,
			},
			wantDir:     "/custom/project",
			wantModel:   "gpt-4",
			wantTimeout: 5 * time.Minute,
		},
		{
			name: "zero timeout uses default",
			config: PiCoderConfig{
				ProjectDir:     "/test/project",
				TimeoutSeconds: 0,
			},
			wantDir:     "/test/project",
			wantModel:   "glm-4.7",
			wantTimeout: 5 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewPiCoderTool(tt.config)

			assert.Equal(t, tt.wantDir, tool.projectDir)
			assert.Equal(t, tt.wantModel, tool.model)
			assert.Equal(t, tt.wantTimeout, tool.timeout)
		})
	}
}

// TestPiCoderTool_Name проверяет имя инструмента.
func TestPiCoderTool_Name(t *testing.T) {
	tool := NewPiCoderTool(PiCoderConfig{ProjectDir: "/test"})
	assert.Equal(t, "pi_coder", tool.Name())
}

// TestPiCoderTool_Description проверяет описание инструмента.
func TestPiCoderTool_Description(t *testing.T) {
	tool := NewPiCoderTool(PiCoderConfig{ProjectDir: "/test"})
	desc := tool.Description()

	assert.Contains(t, desc, "pi")
	assert.Contains(t, desc, "AI coding agent")
	assert.Contains(t, desc, "rebuild")
	assert.Contains(t, desc, "код агента")
}

// TestPiCoderTool_Parameters проверяет JSON Schema параметров.
func TestPiCoderTool_Parameters(t *testing.T) {
	tool := NewPiCoderTool(PiCoderConfig{ProjectDir: "/test"})
	params := tool.Parameters()

	// Проверка структуры
	assert.Equal(t, "object", params["type"])

	props, ok := params["properties"].(map[string]any)
	require.True(t, ok)

	// Проверка параметра task
	task, ok := props["task"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", task["type"])
	assert.Contains(t, task["description"], "Задача")

	// Проверка параметра rebuild
	rebuild, ok := props["rebuild"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "boolean", rebuild["type"])
	assert.Contains(t, rebuild["description"], "Пересобрать")

	// Проверка обязательных полей
	required, ok := params["required"].([]string)
	require.True(t, ok)
	assert.Len(t, required, 1)
	assert.Equal(t, "task", required[0])

	// Проверяем, что rebuild не в обязательных полях
	assert.NotContains(t, required, "rebuild")
}

// TestPiCoderTool_Execute_Validation проверяет валидацию входных параметров.
func TestPiCoderTool_Execute_Validation(t *testing.T) {
	tests := []struct {
		name        string
		args        json.RawMessage
		wantErr     bool
		errContains string
	}{
		{
			name:    "valid task only",
			args:    json.RawMessage(`{"task": "add tests"}`),
			wantErr: false,
		},
		{
			name:    "valid task with rebuild true",
			args:    json.RawMessage(`{"task": "add tests", "rebuild": true}`),
			wantErr: false,
		},
		{
			name:    "valid task with rebuild false",
			args:    json.RawMessage(`{"task": "add tests", "rebuild": false}`),
			wantErr: false,
		},
		{
			name:        "empty task returns error",
			args:        json.RawMessage(`{"task": ""}`),
			wantErr:     true,
			errContains: "task is required",
		},
		{
			name:        "missing task returns error",
			args:        json.RawMessage(`{"rebuild": true}`),
			wantErr:     true,
			errContains: "task is required",
		},
		{
			name:        "invalid JSON",
			args:        json.RawMessage(`{invalid json}`),
			wantErr:     true,
			errContains: "parse args",
		},
		{
			name:        "empty JSON object",
			args:        json.RawMessage(`{}`),
			wantErr:     true,
			errContains: "task is required",
		},
		{
			name:        "null task",
			args:        json.RawMessage(`{"task": null}`),
			wantErr:     true,
			errContains: "task is required",
		},
		{
			name:    "extra fields are ignored",
			args:    json.RawMessage(`{"task": "add tests", "extra": "field", "number": 123}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Создаём инструмент с минимальным таймаутом для быстрых тестов
			tool := &PiCoderTool{
				projectDir: "/nonexistent",
				model:      "test-model",
				timeout:    10 * time.Millisecond,
			}

			result, err := tool.Execute(context.Background(), tt.args)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				// При ошибке валидации результат должен быть пустым
				assert.Empty(t, result)
			} else if err != nil {
				// Для валидных аргументов может быть ошибка выполнения,
				// но не должна быть ошибка валидации
				// Ошибка выполнения (pi не найден или таймаут) - это нормально для unit тестов
				assert.NotContains(t, err.Error(), "parse args")
				assert.NotContains(t, err.Error(), "task is required")
			}
		})
	}
}

// TestPiCoderTool_Execute_SpecialCharacters проверяет обработку специальных символов.
func TestPiCoderTool_Execute_SpecialCharacters(t *testing.T) {
	tests := []struct {
		name        string
		task        string
		wantErr     bool
		description string
	}{
		{
			name:        "quotes in task",
			task:        `Add "feature" with 'single quotes'`,
			wantErr:     false,
			description: "Task with double and single quotes",
		},
		{
			name:        "newlines in task",
			task:        "Add function\nWith multiple\nlines",
			wantErr:     false,
			description: "Task with newline characters",
		},
		{
			name:        "tabs in task",
			task:        "Add function\twith\ttabs",
			wantErr:     false,
			description: "Task with tab characters",
		},
		{
			name:        "unicode characters",
			task:        "Добавить функцию с эмодзи 🚀",
			wantErr:     false,
			description: "Task with unicode and emoji",
		},
		{
			name:        "backslashes",
			task:        `Add path with \backslash\ separator`,
			wantErr:     false,
			description: "Task with backslash characters",
		},
		{
			name:        "dollar signs",
			task:        `Use $VAR in code`,
			wantErr:     false,
			description: "Task with dollar signs (shell variables)",
		},
		{
			name:        "brackets and braces",
			task:        "Add function with [array] and {object}",
			wantErr:     false,
			description: "Task with brackets and braces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &PiCoderTool{
				projectDir: "/nonexistent",
				model:      "test-model",
				timeout:    10 * time.Millisecond,
			}

			args, err := json.Marshal(map[string]any{"task": tt.task})
			require.NoError(t, err)

			// Проверяем, что аргументы успешно парсятся
			var a piCoderArgs
			err = json.Unmarshal(args, &a)
			assert.NoError(t, err)
			assert.Equal(t, tt.task, a.Task)

			// Проверяем, что Execute не возвращает ошибку валидации
			_, err = tool.Execute(context.Background(), args)
			if err != nil {
				// Ошибка выполнения (pi не найден) - это нормально
				assert.NotContains(t, err.Error(), "parse args")
				assert.NotContains(t, err.Error(), "task is required")
			}
		})
	}
}

// TestPiCoderTool_Execute_TaskLength проверяет обработку задач различной длины.
func TestPiCoderTool_Execute_TaskLength(t *testing.T) {
	tests := []struct {
		name        string
		task        string
		wantErr     bool
		description string
	}{
		{
			name:        "short task",
			task:        "fix bug",
			wantErr:     false,
			description: "Minimal task description",
		},
		{
			name:        "medium task",
			task:        strings.Repeat("add feature ", 10),
			wantErr:     false,
			description: "Task with 100 characters",
		},
		{
			name:        "long task",
			task:        strings.Repeat("word ", 500),
			wantErr:     false,
			description: "Task with 2500 characters",
		},
		{
			name:        "very long task",
			task:        strings.Repeat("a", 10000),
			wantErr:     false,
			description: "Task with 10000 characters",
		},
		{
			name:        "single character",
			task:        "a",
			wantErr:     false,
			description: "Minimal non-empty task",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &PiCoderTool{
				projectDir: "/nonexistent",
				model:      "test-model",
				timeout:    10 * time.Millisecond,
			}

			args, err := json.Marshal(map[string]any{"task": tt.task})
			require.NoError(t, err)

			// Проверяем, что Execute не возвращает ошибку валидации
			result, err := tool.Execute(context.Background(), args)

			// Длинные задачи не должны вызывать ошибку валидации
			if err != nil {
				assert.NotContains(t, err.Error(), "parse args")
				assert.NotContains(t, err.Error(), "task is required")
			}

			// Проверяем, что результат обрезается если слишком длинный
			const maxLen = 8192
			if result != "" && len(result) > maxLen {
				assert.Contains(t, result, "... (вывод обрезан)")
			}
		})
	}
}

// TestPiCoderTool_Execute_Rebuild проверяет поведение параметра rebuild.
func TestPiCoderTool_Execute_Rebuild(t *testing.T) {
	tests := []struct {
		name        string
		rebuild     bool
		wantRebuild bool
	}{
		{
			name:        "rebuild false explicitly",
			rebuild:     false,
			wantRebuild: false,
		},
		{
			name:        "rebuild true",
			rebuild:     true,
			wantRebuild: true,
		},
		{
			name:        "rebuild not specified",
			rebuild:     false, // после парсинга JSON без поля будет false
			wantRebuild: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := &PiCoderTool{
				projectDir: "/nonexistent",
				model:      "test-model",
				timeout:    10 * time.Millisecond,
			}

			args, err := json.Marshal(map[string]any{
				"task":    "test task",
				"rebuild": tt.rebuild,
			})
			require.NoError(t, err)

			// Проверяем парсинг аргументов
			var a piCoderArgs
			err = json.Unmarshal(args, &a)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRebuild, a.Rebuild)

			// Запускаем execute
			_, err = tool.Execute(context.Background(), args)
			// Ошибка выполнения pi ожидаема в тестовой среде
			if err != nil {
				assert.NotContains(t, err.Error(), "parse args")
			}
		})
	}
}

// TestPiCoderTool_Execute_RebuildNotSpecified проверяет, что rebuild=false по умолчанию.
func TestPiCoderTool_Execute_RebuildNotSpecified(t *testing.T) {
	// Отправляем только task, без rebuild
	args := json.RawMessage(`{"task": "test task"}`)

	var a piCoderArgs
	err := json.Unmarshal(args, &a)
	require.NoError(t, err)

	// rebuild должен быть false по умолчанию
	assert.False(t, a.Rebuild)
}

// TestPiCoderTool_Execute_ContextTimeout проверяет обработку таймаута контекста.
func TestPiCoderTool_Execute_ContextTimeout(t *testing.T) {
	tool := &PiCoderTool{
		projectDir: "/nonexistent",
		model:      "test-model",
		timeout:    1 * time.Nanosecond, // Очень короткий таймаут
	}

	args := json.RawMessage(`{"task": "test task"}`)

	_, err := tool.Execute(context.Background(), args)

	// Ожидаем ошибку таймаута или выполнения
	if err != nil {
		// Ошибка может быть таймаутом или ошибкой выполнения (pi не найден)
		// Главное - что это не ошибка валидации
		assert.NotContains(t, err.Error(), "parse args")
		assert.NotContains(t, err.Error(), "task is required")
	}
}

// TestPiCoderArgs_Unmarshal проверяет парсинг JSON в piCoderArgs.
func TestPiCoderArgs_Unmarshal(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    piCoderArgs
		wantErr bool
	}{
		{
			name:    "both fields",
			json:    `{"task": "add tests", "rebuild": true}`,
			want:    piCoderArgs{Task: "add tests", Rebuild: true},
			wantErr: false,
		},
		{
			name:    "only task",
			json:    `{"task": "add tests"}`,
			want:    piCoderArgs{Task: "add tests", Rebuild: false},
			wantErr: false,
		},
		{
			name:    "rebuild false",
			json:    `{"task": "add tests", "rebuild": false}`,
			want:    piCoderArgs{Task: "add tests", Rebuild: false},
			wantErr: false,
		},
		{
			name:    "empty object",
			json:    `{}`,
			want:    piCoderArgs{Task: "", Rebuild: false},
			wantErr: false,
		},
		{
			name:    "task with special chars",
			json:    `{"task": "line1\nline2\ttab"}`,
			want:    piCoderArgs{Task: "line1\nline2\ttab", Rebuild: false},
			wantErr: false,
		},
		{
			name:    "invalid json",
			json:    `{not valid}`,
			want:    piCoderArgs{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got piCoderArgs
			err := json.Unmarshal([]byte(tt.json), &got)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// TestPiCoderArgs_Marshal проверяет сериализацию piCoderArgs в JSON.
func TestPiCoderArgs_Marshal(t *testing.T) {
	tests := []struct {
		name string
		args piCoderArgs
		want string
	}{
		{
			name: "both fields",
			args: piCoderArgs{Task: "add tests", Rebuild: true},
			want: `{"task":"add tests","rebuild":true}`,
		},
		{
			name: "only task",
			args: piCoderArgs{Task: "add tests"},
			want: `{"task":"add tests","rebuild":false}`,
		},
		{
			name: "empty task",
			args: piCoderArgs{Task: ""},
			want: `{"task":"","rebuild":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.args)
			require.NoError(t, err)

			// Сравнение после разбора обратно, так как порядок полей может отличаться
			var gotMap map[string]any
			err = json.Unmarshal(got, &gotMap)
			require.NoError(t, err)

			var wantMap map[string]any
			err = json.Unmarshal([]byte(tt.want), &wantMap)
			require.NoError(t, err)

			assert.Equal(t, wantMap, gotMap)
		})
	}
}

// BenchmarkPiCoderTool_Execute_Name бенчмарк для метода Name.
func BenchmarkPiCoderTool_Execute_Name(b *testing.B) {
	tool := NewPiCoderTool(PiCoderConfig{ProjectDir: "/test"})
	for i := 0; i < b.N; i++ {
		_ = tool.Name()
	}
}

// BenchmarkPiCoderTool_Execute_Parameters бенчмарк для метода Parameters.
func BenchmarkPiCoderTool_Execute_Parameters(b *testing.B) {
	tool := NewPiCoderTool(PiCoderConfig{ProjectDir: "/test"})
	for i := 0; i < b.N; i++ {
		_ = tool.Parameters()
	}
}

// BenchmarkPiCoderTool_Execute_Parse бенчмарк для парсинга аргументов.
func BenchmarkPiCoderTool_Execute_Parse(b *testing.B) {
	args := json.RawMessage(`{"task": "Add comprehensive tests for the pi_coder tool with various scenarios", "rebuild": true}`)
	var a piCoderArgs

	for i := 0; i < b.N; i++ {
		_ = json.Unmarshal(args, &a)
	}
}
