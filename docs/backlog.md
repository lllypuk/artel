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

### 5. Мониторинг и `/status`

**Зачем:** Понимать, что происходит с агентом: сколько токенов потрачено, сколько запросов, статус фидов.

**Что сделать:**
- Команда `/status` → аптайм, размер БД, кол-во заметок/диалогов, статус фидов
- Подсчёт использования токенов per-session и per-day
- Таблица `usage_stats(date, prompt_tokens, completion_tokens, requests_count)`
- Опциональный Prometheus-экспортер или простой HTTP `/metrics`

**Сложность:** Лёгкая
**Файлы:** `internal/telegram/bot.go`, `internal/storage/stats.go`, `internal/agent/agent.go` (подсчёт)

---

### 6. Выполнение кода (`code_runner` tool)

**Зачем:** Расширение shell, специализированное для вычислений и прототипов. Агент сможет писать и запускать код для ответа на вопросы.

**Что сделать:**
- Принимает код (Python/Go/bash) и язык
- Выполняет в изолированной среде (temp dir, таймаут, лимит памяти)
- Возвращает stdout/stderr/exit code
- Автоматическая очистка temp-файлов
- Опциональные ограничения: `ulimit`, `timeout`, `nsjail`

**Сложность:** Средняя
**Файлы:** `internal/tools/code_runner.go`

---

## 🟢 Tier 3 — Nice-to-have

### 7. Мультипровайдер LLM с fallback

**Зачем:** Если z.ai недоступен → автоматическое переключение на backup-провайдер. Устойчивость к сбоям.

**Что сделать:**
- Конфиг с несколькими провайдерами (z.ai, Ollama local, OpenRouter, etc.)
- Retry с экспоненциальным backoff
- Автоматический fallback при ошибках 5xx или таймауте
- Разные модели для разных задач (vision, fast, reasoning)
- Health check провайдеров

**Сложность:** Средняя
**Файлы:** `internal/llm/client.go` (рефакторинг), `internal/llm/provider.go` (интерфейс), `internal/llm/fallback.go`

---

### 8. Интеграция с календарём (`calendar` tool)

**Зачем:** Управление расписанием через агента. Хорошо сочетается с reminders.

**Что сделать:**
- CalDAV клиент (Nextcloud, Radicale) или Google Calendar API
- Показать расписание на день/неделю
- Создать/удалить событие через агента
- Уведомления о предстоящих событиях

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
| 5 | `/status` + мониторинг | Лёгкая | Наблюдаемость |
| 6 | Code runner | Средняя | Вычислительные возможности |
| 7 | Мультипровайдер LLM | Средняя | Отказоустойчивость |
| 8 | Календарь | Высокая | Расширение экосистемы |
