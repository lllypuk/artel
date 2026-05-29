# Улучшение архитектуры: тесты ReAct, RAG для заметок, observability, валидация инструментов

## Overview

После анализа LangChain / langchaingo принято решение **не внедрять** его в проект. `agent-shatrov`
уже содержит собственные реализации всех ключевых паттернов LangChain (ReAct loop, tools, memory,
multi-provider LLM) — в идиоматичном Go, с полным контролем над потоком управления. Добавление
langchaingo дало бы обратный эффект: лишняя зависимость, скрытая логика через reflection/generics,
меньше контроля и более низкая зрелость библиотеки по сравнению с Python-версией.

Вместо этого план сосредоточен на **четырёх реальных направлениях**, которые улучшат
поддерживаемость, надёжность и расширяемость:

1. **Юнит-тесты для ReAct loop** — покрытие самой сложной и критичной части (`internal/agent/agent.go`)
2. **RAG для заметок** — семантический поиск через embedding + векторный индекс вместо keyword-only FTS5
3. **Observability (OpenTelemetry)** — распределённые трейсы запросов вместо неструктурированных логов
4. **Middleware валидация аргументов инструментов** — JSON Schema проверка *перед* вызовом `Execute()`

**Что решает:** снижение regressions при изменении ядра, качественный поиск по заметкам,
прозрачность работы агента в продакшене, раннее обнаружение невалидных tool_calls.

## Context

- **Основные файлы:**
    - `internal/agent/agent.go` — ReAct loop: загрузка истории, вызов LLM, обработка tool_calls,
      сохранение ответа (цикл до `max_iterations`)
    - `internal/tools/registry.go` — реестр инструментов, интерфейс `Tool`
    - `internal/tools/notes.go` — инструмент заметок (create/search/list/edit/delete), использует
      `storage/` для FTS5
    - `internal/storage/sqlite.go` — схема SQLite, `notes` таблица + FTS5 индекс
    - `internal/llm/` — `provider.go`, `client.go`, `multi.go` (multi-provider с fallback/retry)
    - `internal/telegram/bot.go` — long polling, dispatch сообщений в агент
    - `cmd/agent/main.go` — wiring зависимостей
- **Паттерны:**
    - Инструменты реализуют чистый Go interface: `Name/Description/Parameters/Execute`
    - `Parameters()` возвращает `map[string]any` — JSON Schema для OpenAI tool calling
    - `ChatID` пробрасывается через `context.Value(tools.ChatIDKey)`
    - `agent.go` вызывает `llm.Complete()` с messages + tools, парсит `tool_calls`, выполняет,
      повторяет цикл
- **Критичный gap:** `agent.go` не покрыт юнит-тестами — нет способа убедиться, что изменения не
  ломают reasoning loop
- **Поиск заметок:** использует SQLite FTS5 (`MATCH`) — хорош для exact keyword, но не умеет
  семантический поиск (synonyms, related concepts, paraphrasing)

## Development Approach

- **Testing approach**: Regular (код → тесты)
- Завершать каждую задачу полностью перед переходом к следующей
- **Каждая задача должна включать тесты** для изменённого кода
- Все тесты должны проходить перед переходом к следующей задаче
- Запускать `make lint test` после каждого изменения

## Testing Strategy

- **Unit tests**: моки для `llm.Provider` и `tools.Tool`, табличные тесты для всех веток ReAct loop
- **Storage tests**: миграции SQLite в `:memory:`, проверка CRUD и FTS5 для notes
- **Integration tests** (Task 3): docker-compose с Jaeger/OTel collector, проверка трейсов
- **Middleware tests**: валидные/невалидные JSON payloads для каждого инструмента

## Progress Tracking

- `[x]` — выполнено
- `➕` — добавлено в процессе
- `⚠️` — блокер/проблема

## Solution Overview

### 1. Тесты для ReAct loop

Создать `internal/agent/agent_test.go` с мок-реализациями:
- `mockProvider` — реализует `llm.Provider`, возвращает заданный ответ (text или tool_calls)
- `mockTool` — реализует `tools.Tool`, фиксирует факт вызова и аргументы

Покрыть сценарии:
- LLM отвечает текстом сразу → 1 итерация, сохранение в БД
- LLM возвращает tool_call → выполнение, повторный запрос, финальный текст → 2 итерации
- Достигнут `max_iterations` → graceful stop с уведомлением
- LLM возвращает несуществующий tool → error handling, не паника
- Tool вернул ошибку → retry с тем же tool или fallback к тексту

### 2. RAG для заметок (embedding + векторный поиск)

Добавить семантический слой к существующему FTS5:
- `storage/notes_embeddings.go` — новая таблица `note_embeddings(id, note_id, embedding BLOB)`
- `tools/notes.go` расширить: при create/update генерировать embedding через локальную модель
  (Ollama: `nomic-embed-text` или `mxbai-embed-large`) или lightweight HTTP API
- Поиск: объединить результаты FTS5 (keyword) и векторного поиска (cosine similarity), ранжировать
- Конфигурация: `notes.embedding.enabled`, `notes.embedding.model`, `notes.embedding.ollama_url`

### 3. Observability (OpenTelemetry)

- Интегрировать `go.opentelemetry.io/otel`:
  - `Tracer` в `agent.go`: trace на весь ReAct loop (спаны: LLM call, each tool execution,
    persistence)
  - `Tracer` в `llm/client.go` и `llm/multi.go`: HTTP spans с retry/fallback
  - `Meter` для метрик: token usage, tool call latency, iteration count
- Экспортеры: OTLP HTTP/gRPC → Jaeger / Grafana Tempo (локально через docker-compose)
- Propagation: trace context через `context.Context` (уже есть)

### 4. JSON Schema валидация перед Execute()

- Создать `internal/tools/middleware.go` — `ValidatingRegistry` или middleware wrapper
- При `Register(tool)` — компилировать `tool.Parameters()` в `gojsonschema.Schema` (или
  `xjsonschema`)
- При `Execute(ctx, name, rawArgs)` — сначала валидация schema, потом `tool.Execute()`
- Если валидация не прошла — вернуть structured error в LLM (а не panic или nil dereference)

## What Goes Where

**Implementation Steps** — всё что делается в этом репо.
**Post-Completion** — ручная проверка и конфигурация.

## Implementation Steps

### Task 1: ReAct Loop Unit Tests

**Files:**

- Create: `internal/agent/agent_test.go`
- Create: `internal/agent/mock_provider.go`
- Create: `internal/agent/mock_tool.go`

- [ ] создать `mockProvider` — `llm.Provider` с настраиваемым ответом (`responses []CompletionResponse`, cursor)
- [ ] создать `mockTool` — `tools.Tool` с полем `calledWith map[string]any`, фиксирует `Execute()`
- [ ] написать тесты:
    - `TestAgent_SingleTurn_TextResponse` — LLM отвечает текстом → `agent.Run()` возвращает текст, 1
      итерация
    - `TestAgent_TwoTurns_ToolThenText` — LLM: tool_call → tool execute → LLM: текст → 2 итерации
    - `TestAgent_MaxIterations` — LLM всегда возвращает tool_call → после `max_iterations`
      возвращается error или partial text
    - `TestAgent_UnknownTool` — LLM просит несуществующий tool → graceful error, не panic
    - `TestAgent_ToolError` — tool.Execute возвращает error → error передаётся в LLM как результат,
      loop продолжается
- [ ] запустить `make lint test` — должно пройти

### Task 2: RAG / Embedding для заметок — инфраструктура

**Files:**

- Create: `internal/storage/notes_embeddings.go`
- Modify: `internal/storage/sqlite.go` — добавить DDL для `note_embeddings`
- Modify: `internal/config/config.go` — добавить `NotesEmbeddingConfig`
- Modify: `config.example.yaml` — пример секции `notes.embedding`

- [ ] добавить в `sqlite.go` DDL: `CREATE TABLE note_embeddings(...)` + `CREATE INDEX` по `note_id`
- [ ] в `notes_embeddings.go` реализовать:
    - `SaveEmbedding(ctx, noteID int64, embedding []float32) error`
    - `SearchSimilar(ctx, queryEmbedding []float32, limit int) ([]int64, error)` — cosine similarity
    - `DeleteEmbedding(ctx, noteID int64) error`
- [ ] добавить `NotesEmbeddingConfig` в `config.go` (Enabled, Model, OllamaURL)
- [ ] написать тесты для `storage/notes_embeddings.go` через `:memory:` SQLite
- [ ] запустить `make lint test` — должно пройти

### Task 3: RAG — генерация embedding и поисковый инструмент

**Files:**

- Create: `internal/tools/embedder.go` — интерфейс `Embedder` + реализация `OllamaEmbedder`
- Modify: `internal/tools/notes.go` — `notesTool` получает `embedder Embedder`
- Modify: `cmd/agent/tools.go` — `registerNotesTool` создаёт `OllamaEmbedder` при `cfg.Notes.Embedding.Enabled`
- Modify: `internal/tools/notes.go` — `searchNotes`: объединить FTS5 + векторный поиск

- [ ] реализовать `OllamaEmbedder` — HTTP POST `/api/embeddings` в Ollama с model + prompt
- [ ] в `notes.go` при `createNote` / `updateNote` — если embedder != nil, сгенерировать embedding и
  сохранить через `storage.SaveEmbedding`
- [ ] в `searchNotes` — если embedder != nil:
    1. Сгенерировать embedding от query
    2. Получить top-N по cosine similarity
    3. Получить top-N по FTS5
    4. Объединить, дедуплицировать, отсортировать по релевантности (гибридный скоринг)
- [ ] написать тесты для `OllamaEmbedder` (httptest mock) и для hybrid search
- [ ] запустить `make lint test` — должно пройти

### Task 4: OpenTelemetry — трассировка ReAct loop

**Files:**

- Modify: `go.mod` / `go.sum` — добавить `go.opentelemetry.io/otel`
- Modify: `cmd/agent/main.go` — инициализация TracerProvider (OTLP или stdout для dev)
- Modify: `internal/agent/agent.go` — спаны: `agent.run`, `llm.complete`, `tool.{name}`
- Modify: `internal/llm/client.go` — span на HTTP запрос с атрибутами: model, provider, tokens
- Modify: `internal/llm/multi.go` — span на fallback/retry

- [ ] добавить зависимости: `otel`, `otel/trace`, `otel/sdk`, `otel/exporters/otlp/otlptrace/otlptracehttp`
- [ ] в `main.go` инициализировать `TracerProvider` при старте, shutdown при SIGINT
- [ ] в `agent.go` — `tracer.Start(ctx, "agent.run")` → child spans для каждой итерации, LLM call, tool execution
- [ ] в `client.go` — `tracer.Start(ctx, "llm.complete")` с атрибутами model, provider, token count
- [ ] в `multi.go` — span на каждый fallback с атрибутом `provider_name`
- [ ] написать тест: проверить что spans создаются (in-memory exporter для тестов)
- [ ] запустить `make lint test` — должно пройти

### Task 5: JSON Schema валидация в Registry

**Files:**

- Modify: `internal/tools/registry.go`
- Create: `internal/tools/validation.go`

- [ ] добавить в `registry.go` поле `validators map[string]*gojsonschema.Schema`
- [ ] при `Register(tool)` — компилировать `tool.Parameters()` в JSON Schema (через
  `gojsonschema.NewGoLoader`)
- [ ] в `Execute(ctx, name, rawArgs)` — перед `tool.Execute()` валидировать `rawArgs` по schema
- [ ] если validation failed — вернуть `fmt.Errorf("validation failed for tool %s: %v", name, errs)`
- [ ] написать тесты:
    - `TestRegistry_ValidPayload` — валидные args проходят
    - `TestRegistry_InvalidPayload` — невалидные args отклоняются до вызова tool
    - `TestRegistry_MissingRequiredField` — отсутствие required поля ловится
- [ ] запустить `make lint test` — должно пройти

### Task 6: Финальная проверка и документация

**Files:**

- Modify: `CLAUDE.md` — обновить разделы Architecture, Implemented Features
- Move: `docs/plans/20260529-architecture-improvements.md` → `docs/plans/completed/`

- [ ] обновить `CLAUDE.md`: добавить embedding/RAG в описание notes, добавить OTel в архитектуру,
  упомянуть middleware валидацию
- [ ] убедиться что `make lint test` проходит полностью
- [ ] переместить план в `docs/plans/completed/`

## Post-Completion

**Ручная проверка:**

- Task 2-3 (RAG): поднять Ollama локально (`ollama run nomic-embed-text`), создать заметку,
  проверить что `note_embeddings` заполнилась, выполнить поиск по синониму (не exact match) —
  убедиться что семантически близкая заметка находится
- Task 4 (OTel): поднять Jaeger (`docker run -p 16686:16686 jaegertracing/all-in-one`), проверить
  трейсы в UI по `service.name=agent-shatrov`
- Task 5 (Validation): вызвать инструмент с невалидным JSON (например, string вместо int) —
  убедиться что агент получает structured error и loop продолжается корректно

**Опционально:**

- Для Task 3 можно добавить `notes.embedding.dimensions` в конфиг (default 768 для nomic)
- Для Task 4 можно добавить метрики (`Meter`) — histogram на latency LLM calls, counter на tool
  invocations
- Рассмотреть batch embedding при массовом импорте заметок (чтобы не генерировать по одному)