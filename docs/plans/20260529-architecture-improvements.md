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

- **Unit tests**: httptest.Server для LLM (уже используется в agent_test.go), табличные тесты для всех веток ReAct loop
- **Storage tests**: миграции SQLite в `:memory:`, проверка CRUD для note_embeddings
- **OTel tests** (Task 4): in-memory span exporter (`sdktrace.NewSimpleSpanProcessor` + `tracetest.NewInMemoryExporter`) — никакого docker-compose, всё в unit-тестах
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

- Добавить в `registry.go` поле `validators map[string]*gojsonschema.Schema` +
  helper-функции в `validation.go`
- Библиотека: `github.com/xeipuuv/gojsonschema` (JSON Schema Draft-04/07, стабильная)
- При `Register(tool)` — компилировать `tool.Parameters()` в schema через
  `gojsonschema.NewGoLoader`
- При `Execute(ctx, name, rawArgs)` — сначала валидация schema, потом `tool.Execute()`
- Если валидация не прошла — вернуть structured error в LLM (а не panic или nil dereference)

## What Goes Where

**Implementation Steps** — всё что делается в этом репо.
**Post-Completion** — ручная проверка и конфигурация.

## Implementation Steps

### Task 1: ReAct Loop Unit Tests

⚠️ **`internal/agent/agent_test.go` уже существует** с 13 тестами (httptest.Server подход — лучше чем
моки, тестирует всю цепочку). Отдельные `mock_provider.go` / `mock_tool.go` не нужны.

**Уже покрыто:**
- `TestHandleMessageSimpleResponse` — текстовый ответ ✅
- `TestHandleMessageToolCallLoop` — tool call → execute → финальный текст ✅
- `TestHandleMessageToolResultInContext` — tool result в контексте LLM ✅
- `TestHandleMessageNoChoices` — пустой ответ LLM ✅
- `TestHandleMessagePreservesHistory` — история диалога ✅
- `TestHandleMessageWithAttachments*` — вложения/vision ✅
- `TestNewConversation`, `TestGetSetModel`, `TestSetModels` ✅

**Files:**

- Modify: `internal/agent/agent_test.go` — добавить 3 недостающих теста

- [x] `agent_test.go` создан, базовые тесты написаны
- [x] добавить `TestHandleMessageMaxIterations` — LLM всегда возвращает tool_call →
  после `max_iterations` возвращается частичный текст (не паника)
- [x] добавить `TestHandleMessageUnknownTool` — LLM просит несуществующий tool →
  graceful error в ответе, не паника, loop продолжается
- [x] добавить `TestHandleMessageToolError` — tool.Execute возвращает error →
  error передаётся в LLM как результат ("Error: ..."), loop продолжается
- [x] запустить `make lint test` — должно пройти

### Task 2: RAG / Embedding для заметок — инфраструктура

**Files:**

- Create: `internal/storage/notes_embeddings.go`
- Modify: `internal/storage/sqlite.go` — добавить DDL для `note_embeddings`
- Modify: `internal/config/config.go` — добавить `NotesEmbeddingConfig`
- Modify: `config.example.yaml` — пример секции `notes.embedding`

- [x] добавить в `sqlite.go` DDL: `CREATE TABLE note_embeddings(...)` + `CREATE INDEX` по `note_id`
- [x] в `notes_embeddings.go` реализовать:
    - `SaveEmbedding(ctx, noteID int64, embedding []float32) error` — сериализация `[]float32` в
      `[]byte` через `encoding/binary` (little-endian), хранение в BLOB
    - `SearchSimilar(ctx, queryEmbedding []float32, limit int) ([]int64, error)` — загрузить все
      embeddings из БД в память, посчитать cosine similarity в Go (O(n) scan); при ~1000 заметок
      достаточно быстро, нативный vector search в SQLite не поддерживается
    - `DeleteEmbedding(ctx, noteID int64) error`
- [x] добавить `NotesEmbeddingConfig` в `config.go` (Enabled, Model, OllamaURL)
- [x] написать тесты для `storage/notes_embeddings.go` через `:memory:` SQLite
- [x] запустить `make lint test` — должно пройти

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

- Modify: `go.mod` / `go.sum` — добавить OTel зависимости (см. ниже)
- Modify: `cmd/agent/main.go` — инициализация TracerProvider (OTLP HTTP если задан endpoint,
  иначе stdout для dev; если OTel не сконфигурирован — noop)
- Modify: `internal/agent/agent.go` — спаны: `agent.run`, `llm.complete`, `tool.{name}`
- Modify: `internal/llm/client.go` — span на HTTP запрос с атрибутами: model, provider, tokens
- Modify: `internal/llm/multi.go` — span на fallback/retry

**OTel зависимости** (`go get`):
```
go.opentelemetry.io/otel
go.opentelemetry.io/otel/trace
go.opentelemetry.io/otel/sdk
go.opentelemetry.io/otel/sdk/trace
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp
go.opentelemetry.io/otel/exporters/stdout/stdouttrace
```

- [ ] добавить зависимости через `go get` (список выше)
- [ ] в `main.go` инициализировать `TracerProvider`: если `otel.endpoint` задан в конфиге — OTLP
  HTTP экспортер; иначе noop провайдер (чтобы OTel не был обязательным)
- [ ] добавить `OtelConfig` в `internal/config/config.go` (`Endpoint string`, `ServiceName string`)
- [ ] в `agent.go` — `tracer.Start(ctx, "agent.run")` → child spans для каждой итерации, LLM call,
  tool execution
- [ ] в `client.go` — `tracer.Start(ctx, "llm.complete")` с атрибутами model, provider, token count
- [ ] в `multi.go` — span на каждый fallback с атрибутом `provider_name`
- [ ] написать тест: проверить что spans создаются через `tracetest.NewInMemoryExporter` +
  `sdktrace.NewSimpleSpanProcessor` (не docker-compose, не Jaeger — только unit test)
- [ ] запустить `make lint test` — должно пройти

### Task 5: JSON Schema валидация в Registry

Используем `github.com/xeipuuv/gojsonschema` — стабильная библиотека с поддержкой JSON Schema
Draft-04/07 (именно такой формат возвращает `Parameters()`). Альтернативы не рассматриваем.

**Files:**

- Modify: `internal/tools/registry.go` — добавить `validators map[string]*gojsonschema.Schema`
- Create: `internal/tools/validation.go` — helper-функции компиляции схем и валидации

```
go get github.com/xeipuuv/gojsonschema
```

- [ ] добавить в `registry.go` поле `validators map[string]*gojsonschema.Schema`
- [ ] при `Register(tool)` — компилировать `tool.Parameters()` в схему через
  `gojsonschema.NewGoLoader`; ошибку компиляции логировать как warn (не паниковать)
- [ ] в `ExecuteWithNotify(ctx, name, rawArgs)` — если валидатор есть, проверить `rawArgs` до
  вызова `tool.Execute()`
- [ ] если validation failed — вернуть структурированную ошибку:
  `fmt.Errorf("invalid args for tool %s: %s", name, errs[0].String())`
- [ ] написать тесты в `registry_test.go` (файл уже существует, дополнить):
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
- Task 4 (OTel): поднять Jaeger (`docker run -p 16686:16686 jaegertracing/all-in-one`), задать
  в конфиге `otel.endpoint: "http://localhost:4318"` и `otel.service_name: "agent-shatrov"`,
  проверить трейсы в UI http://localhost:16686
- Task 5 (Validation): вызвать инструмент с невалидным JSON (например, string вместо int) —
  убедиться что агент получает structured error и loop продолжается корректно

**Опционально:**

- Для Task 3 можно добавить `notes.embedding.dimensions` в конфиг (default 768 для nomic)
- Для Task 4 можно добавить метрики (`Meter`) — histogram на latency LLM calls, counter на tool
  invocations
- Рассмотреть batch embedding при массовом импорте заметок (чтобы не генерировать по одному)