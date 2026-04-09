# Бэклог развития agent-shatrov

> Обновлено: 2026-04-02

## Текущее состояние

| Модуль | Статус | Описание |
|--------|--------|----------|
| Telegram бот + auth | ✅ Done | Long polling, whitelist по chat_id, команды |
| ReAct loop (GLM-4.7) | ✅ Done | LLM → tool calls → LLM → ответ, max 10 итераций |
| История диалогов | ✅ Done | SQLite, персистентная, per-chat |
| Заметки | ✅ Done | CRUD + FTS5 полнотекстовый поиск |
| Веб-поиск + чтение страниц | ✅ Done | Brave / Serper / DuckDuckGo + HTML→text |
| Shell (sandbox) | ✅ Done | Whitelist команд, blocked patterns, таймаут |
| pi_coder (самообновление) | ✅ Done | Агент меняет собственный код через pi |
| Новости (RSS) | ✅ Done | Подписки, фиды, поиск, сохранённые, FTS5 |
| Переключение модели (`/model`) | ✅ Done | `/model`, `/model fast`, `/model default` |
| Напоминания (`reminders` tool) | ✅ Done | CRUD, планировщик, recurring, парсинг естественного языка |
| Обработка файлов и изображений | ✅ Done | Документы + фото в чат, Vision (base64), tool `files` (list/read/delete) |
| Мониторинг (`/status`) | ✅ Done | Аптайм, размер БД, счётчики, статистика токенов per-day |
| Выполнение кода (`code_runner`) | ✅ Done | Python / Go / bash, temp dir, таймаут, ulimit, stdout/stderr/exit code |

---

## 🔴 Tier 1 — Высокий приоритет

### ~~1. Напоминания и отложенные задачи (`reminders` tool)~~ ✅ Done

**Зачем:** Сейчас агент только реагирует на сообщения. Reminders сделают его проактивным — он сам напишет в нужное время.

**Реализовано:**
- Таблица `reminders(id, chat_id, text, trigger_at, repeat_interval, status, created_at)` + индексы
- Tool `reminders` с действиями: `create`, `list`, `delete`, `done`
- Горутина-планировщик в `main.go` — раз в минуту проверяет `trigger_at <= NOW() AND status = 'pending'`
- Отправка уведомления в Telegram через `Notifier`
- Recurring reminders: daily, weekly, hourly, custom interval (Nm/Nh/Nd)
- Парсинг естественного языка: «через 2 часа», «завтра в 10:00», «послезавтра в 15:30», «в 09:00», ISO datetime
- Команда `/reminders` в Telegram — список активных напоминаний

**Файлы:** `internal/tools/reminders.go`, `internal/storage/reminders.go`, `internal/storage/sqlite.go`, `cmd/agent/main.go`, `internal/telegram/bot.go`

---

### ~~2. Управление контекстом диалога~~ ✅ Done

**Зачем:** Сейчас вся история загружалась в LLM. При длинных диалогах токены заканчивались, цена росла, context window переполнялась.

**Реализовано:**
- Конфигурируемый `max_context_messages` (по умолчанию 50, 0 = без ограничений) в `llm.max_context_messages`
- Sliding window: при превышении лимита — суммаризация старых сообщений через отдельный LLM-запрос (предпочтительно fast-модель)
- Сохранение summary в `conversations.summary_text` + граница `summarized_until_id` в SQLite
- При загрузке контекста: system prompt + summary (если есть) + последние N сообщений
- Команда `/context` — текущий размер контекста, наличие суммаризации, оценка токенов
- Корректная деградация: при ошибке суммаризации — fallback на усечённую историю без потери запроса

**Файлы:** `internal/agent/context.go` (новый), `internal/agent/agent.go`, `internal/storage/sqlite.go`, `internal/config/config.go`, `internal/telegram/bot.go`, `cmd/agent/main.go`

---

### ~~3. Переключение модели через Telegram (`/model`)~~ ✅ Done

**Зачем:** Быстрые вопросы не требуют glm-4.7, а glm-4.7-flash отвечает быстрее и дешевле.

**Реализовано:**
- Команда `/model` — показать текущую модель и доступные алиасы
- Команда `/model fast` → переключить на `glm-4.7-flash`
- Команда `/model default` → переключить на `glm-4.7`
- Принимает произвольное имя модели напрямую
- `GetModel` / `SetModel` / `SetModels` / `Models` в `agent.Agent` (thread-safe, `sync.RWMutex`)

**Файлы:** `internal/telegram/bot.go`, `internal/agent/agent.go`, `cmd/agent/main.go`

---

## 🟡 Tier 2 — Средний приоритет

### ~~4. Обработка файлов и изображений~~ ✅ Done

**Зачем:** Telegram позволяет отправлять документы и фото. Сейчас бот принимает только текст — это ограничивает его полезность.

**Реализовано:**
- Приём документов (txt, md, json, csv, pdf, jpg, png и др.) → скачивание и сохранение в `./data/files/`
- Текстовые файлы (txt, md, json, csv и др.) автоматически инжектируются в контекст LLM при отправке
- Приём фото → если включён `vision_enabled`, отправляется в LLM как base64 data URL
- Поддержка multi-modal content в `llm.Message`: `ContentParts []ContentPart` с custom JSON marshal/unmarshal
- Tool `files` с действиями: `list`, `read`, `delete`; вспомогательный метод `SaveFile`
- Команды `/files` — список сохранённых файлов; `/getfile <имя>` — скачать файл обратно
- Конфигурация: `files.enabled`, `files.storage_dir`, `files.max_file_size_kb`, `files.allowed_extensions`, `files.vision_enabled`
- Защита от path traversal (`filepath.Base`), лимит размера файла, whitelist расширений
- Рефакторинг `main()`: вынесены `createDirectories`, `setupAgent`, `setupBot` для снижения цикломатической сложности

**Файлы:** `internal/tools/files.go`, `internal/tools/files_test.go`, `internal/llm/client.go`, `internal/agent/agent.go`, `internal/telegram/bot.go`, `internal/config/config.go`, `cmd/agent/main.go`, `config.example.yaml`

---

### ~~5. Мониторинг и `/status`~~ ✅ Done

**Зачем:** Понимать, что происходит с агентом: сколько токенов потрачено, сколько запросов, статус фидов.

**Реализовано:**
- Команда `/status` → аптайм, текущая модель, размер БД, кол-во диалогов/сообщений/заметок/напоминаний/фидов
- Таблица `usage_stats(date, prompt_tokens, completion_tokens, requests_count)` с `ON CONFLICT … DO UPDATE` для накопления
- Запись токенов в `agent.go` после каждого LLM-вызова (per-request)
- Статистика токенов за сегодня: кол-во запросов, prompt / completion / total
- Статистика за последние 7 дней (построчно, если >1 дня)
- Helpers: `formatDuration` (аптайм), `formatTokens` (тысячные разделители)

**Файлы:** `internal/storage/stats.go` (новый), `internal/storage/stats_test.go` (новый), `internal/storage/sqlite.go` (миграция), `internal/agent/agent.go` (RecordUsage), `internal/telegram/bot.go` (команда + поля), `cmd/agent/main.go` (startTime + SetStatusInfo)

---

### ~~6. Выполнение кода (`code_runner` tool)~~ ✅ Done

**Зачем:** Расширение shell, специализированное для вычислений и прототипов. Агент сможет писать и запускать код для ответа на вопросы.

**Реализовано:**
- Принимает код (Python / Go / bash) и язык
- Python и bash выполняются из temp-файла; Go — компилируется (`go build`) и затем запускается бинарник (корректный exit code)
- Изолированная среда: временная директория (`os.MkdirTemp`), автоматически удаляется после выполнения
- Таймаут через `context.WithTimeout`; при истечении возвращается результат с пометкой «timed out»
- Опциональный лимит виртуальной памяти (`ulimit -v`, в МБ)
- Возвращает exit code, stdout и stderr в читаемом формате; вывод обрезается до 2 KB каждый
- Языковые алиасы: `python3` → python, `golang` → go, `sh` → bash
- Конфигурация: `code_runner.enabled`, `timeout_seconds`, `mem_limit_mb`, `enabled_langs`
- Уведомление в Telegram при вызове («⚙️ Выполняю код (python)...»)

**Файлы:** `internal/tools/code_runner.go`, `internal/tools/code_runner_test.go`, `internal/config/config.go`, `cmd/agent/tools.go`, `internal/tools/registry.go`, `config.example.yaml`

---

## 🟢 Tier 3 — Nice-to-have

### ~~7. Мультипровайдер LLM с fallback~~ ✅ Done

**Зачем:** Если z.ai недоступен → автоматическое переключение на backup-провайдер. Устойчивость к сбоям.

**Реализовано:**
- Конфиг с несколькими провайдерами (`llm.providers`): z.ai, OpenRouter, Ollama, любой OpenAI-compatible API
- Retry с экспоненциальным backoff и jitter (настраиваемые `max_retries`, `initial_backoff_ms`, `max_backoff_ms`)
- Автоматический fallback при ошибках 5xx, 429 (rate limit) или сетевых ошибках
- Кросс-провайдерный маппинг моделей: алиасы `default`/`fast` автоматически резолвятся в модель конкретного провайдера
- Circuit breaker: после N ошибок провайдер помечается «unhealthy» с cooldown-периодом
- Forced fallback: если все провайдеры unhealthy — пробуем все заново
- Health check провайдеров: `/status` и `/model` отображают состояние каждого провайдера
- Extra headers (OpenRouter: `HTTP-Referer`, `X-Title`) и per-provider proxy/timeout
- Полная обратная совместимость: без `providers` работает как раньше (single client)
- Интерфейс `llm.Provider`: `Client` (single) и `MultiClient` (multi) реализуют одинаковый интерфейс

**Файлы:** `internal/llm/provider.go` (новый), `internal/llm/multi.go` (новый), `internal/llm/multi_test.go` (новый), `internal/llm/client.go` (рефакторинг), `internal/config/config.go`, `internal/agent/agent.go`, `cmd/agent/main.go`, `internal/telegram/bot.go`, `config.example.yaml`

---

### 8. Интеграция с календарём (`calendar` tool)

**Зачем:** Управление расписанием через агента. Хорошо сочетается с reminders.

**Что сделать:**
- Google Calendar API
- Показать расписание на день/неделю
- Создать/удалить событие через агента

**Сложность:** Высокая
**Файлы:** `internal/tools/calendar.go`, `internal/calendar/` (пакет)

---

## 📋 Рекомендуемый порядок реализации

| Очередь | Задача | Сложность | Ожидаемый эффект |
|---------|--------|-----------|------------------|
| ~~1~~ | ~~`/model` переключение~~ | ✅ Done | Быстрая победа, уже в планах |
| ~~2~~ | ~~Reminders~~ | ✅ Done | Агент становится проактивным |
| ~~3~~ | ~~Управление контекстом~~ | ✅ Done | Устранение техдолга, стабильность |
| ~~4~~ | ~~Обработка файлов~~ | ✅ Done | Расширение входных данных |
| ~~5~~ | ~~`/status` + мониторинг~~ | ✅ Done | Наблюдаемость |
| ~~6~~ | ~~Code runner~~ | ✅ Done | Вычислительные возможности |
| 7 | Мультипровайдер LLM | Средняя | Отказоустойчивость |
| 8 | Календарь | Высокая | Расширение экосистемы |
