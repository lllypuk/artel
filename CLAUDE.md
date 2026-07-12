# Artel

Персональный AI-агент с Telegram/Mattermost-интерфейсами для личного использования на локальном компьютере.

## Стек

- **Go 1.26**
- **LLM**: любой OpenAI-compatible провайдер (multi-provider с fallback)
- **Интерфейс**: Telegram Bot (long polling) · Mattermost Bot (REST + WebSocket)
- **Хранилище**: SQLite (`data/agent.db`)
- **Прокси**: `HTTPS_PROXY=http://127.0.0.1:2080`

## Структура проекта

```
cmd/agent/
  main.go            — точка входа, wiring всех зависимостей
  dispatcher.go      — Mattermost: диспетчер входящих сообщений + auth/safeguards
  tools.go           — регистрация инструментов (registerOptionalTools)
  news.go            — setup новостей и цикл обновления фидов
  reminders.go       — планировщик напоминаний
  monitor.go         — фоновый цикл мониторинга сайтов с Telegram-алертами
  logger.go          — настройка логгера
internal/
  config/config.go   — загрузка config.yaml + override через env
  agent/agent.go     — ядро: ReAct loop (LLM → tools → LLM → ответ)
  llm/
    provider.go      — интерфейс Provider, APIError, IsRetriable
    client.go        — HTTP-клиент к OpenAI-compatible API (single provider)
    multi.go         — MultiClient: мультипровайдер с fallback, retry, circuit breaker
  tools/
    registry.go        — реестр инструментов, интерфейс Tool, JSON Schema валидация
    validation.go      — валидация аргументов через gojsonschema перед Execute()
    notes.go           — инструмент: заметки (create/search/list/edit/delete) через SQLite
    embedder.go        — интерфейс Embedder + OllamaEmbedder для семантического поиска
    reminders.go       — инструмент: напоминания (create/list/delete/done, повторение)
    shell.go           — инструмент: безопасный shell (whitelist + sandbox)
    web_search.go      — инструмент: поиск в интернете (Brave/Serper/DuckDuckGo)
    web_fetch.go       — инструмент: чтение веб-страниц
    browser.go         — инструмент: автоматизация браузера (agent-browser)
    pi_coder.go        — инструмент: анализ/разработка проектов через pi
    news.go            — инструмент: агрегация новостей (RSS/Atom/JSON)
    weather.go         — инструмент: прогноз погоды (Open-Meteo)
    stocks.go          — инструмент: котировки акций и индексов (Yahoo Finance + MOEX ISS)
    stocks_moex.go     — клиент MOEX ISS API
    stocks_yahoo.go    — клиент Yahoo Finance v8 API
    files.go           — инструмент: управление файлами (list/read/delete)
    code_runner.go     — инструмент: выполнение кода (Python/Go/bash)
    monitor.go         — инструмент: мониторинг доступности сайтов (check/list/history)
    wiki.go            — инструмент: личная вики (create/get/edit/delete/list/tree/rename/backlinks)
    reading_list.go    — инструмент: список для чтения (save/add/list/get/update/delete/digest)
    knowledge_search.go — инструмент: unified поиск по вики + reading list (FTS5 + LIKE fallback)
  storage/
    sqlite.go          — SQLite: схема БД, migrations, CRUD для conversations/messages/notes
    notes_embeddings.go — embeddings: SaveEmbedding, SearchSimilar (cosine), DeleteEmbedding
    news.go            — SQLite: новости, фиды, подписки
    monitor.go         — SQLite: мониторинг сайтов (monitor_checks, monitor_state)
    wiki.go            — SQLite: wiki (wiki_pages + FTS5, wiki_links); cascade rename в транзакции
  projects/
    projects.go        — реестр проектов: конфиг + автообнаружение git-репозиториев
    projects_test.go   — тесты реестра (auto-discovery, exclude, override)
  telemetry/
    telemetry.go       — OpenTelemetry: InitOTLP/InitStdout/Init, tracers
  telegram/
    bot.go           — Telegram bot: long polling, auth по chat_id, обработка команд
    markdown.go      — Markdown → HTML конвертация для Telegram
    transport.go     — реализация интерфейса `Transport` для Telegram
  mattermost/
    client.go        — REST + WebSocket клиент Mattermost
    transport.go     — реализация интерфейса `Transport` для Mattermost
  transport/
    transport.go     — общий интерфейс `Transport`: Send/Messages/Close/Run
    auth.go          — gate авторизации: owner + team_bots + @-mention
  safeguards/
    safeguards.go    — loop-detection и rate-limit для multi-agent диалогов
  news/
    parser.go        — парсер фидов (RSS/Atom/JSON Feed)
    feed.go          — структура фида
    item.go          — структура новости
    subscription.go  — подписка на фид
    fetcher.go       — загрузка фидов по HTTP
    manager.go       — менеджер новостей: фильтрация, поиск
    filter.go        — фильтры новостей
    errors.go        — ошибки пакета news
data/                — БД и логи (в .gitignore)
config.yaml          — конфиг с секретами (в .gitignore)
config.example.yaml  — пример конфига без секретов (transport, role, mattermost, safeguards, projects_dir)
configs/             — примеры конфигов для Docker multi-bot (analyst/developer/tester)
  .gitignore         — игнорировать реальные конфиги без .example
docs/
  multi-instance-guide.md — руководство по запуску нескольких инстансов в Mattermost
  headless-setup.md  — инструкция по установке Docker multi-bot на headless-сервере
  e2e-testing.md     — e2e тестирование: 4 уровня (smoke → pi_coder → multi-bot → Docker)
  roles/             — шаблоны конфигов ролей: analyst, developer, tester
scripts/
  agent-bots.service — systemd user unit для автозапуска Docker multi-bot
Dockerfile           — multi-stage: golang builder + node:22-alpine runtime + Go toolchain + pi
docker-compose.bots.yml — 3 сервиса (analyst, developer, tester) для headless-сервера
.dockerignore        — файлы, исключаемые из Docker-образа
Makefile             — команды для управления сервисом
```

## Команды

```bash
make run      # собрать и запустить сервис (с прокси)
make stop     # остановить сервис
make restart  # перезапустить
make logs     # tail -f data/agent.log
make build    # только сборка бинарника → bin/artel
make clean    # удалить бинарник
make lint     # статический анализ (golangci-lint)
make test     # запуск тестов

# Первый позиционный аргумент — путь к config.yaml (для multi-instance)
./bin/artel config-analyst.yaml
```

## Обязательно после каждой доработки

После **любых** изменений в коде необходимо запустить:

```bash
make lint test
```

Все найденные ошибки и предупреждения должны быть исправлены до завершения задачи. Коммитить код с ошибками lint или
упавшими тестами нельзя.

### E2E тестирование ботов

После доработок, затрагивающих Docker/multi-bot/Mattermost/pi_coder, прогнать e2e тесты
по инструкции: [`docs/e2e-testing.md`](docs/e2e-testing.md). Минимум — уровни 1-2
(smoke-тест одного бота + pi_coder). Полная проверка — уровни 1-4 (включая Docker).

Быстрая проверка (если нет времени на полный прогон):

```bash
# Build + unit tests
make build && go test ./...

# Smoke: бот запускается, WebSocket подключён, LLM отвечает
./bin/artel configs/analyst-bot.yaml &; sleep 3; tail -3 data/analyst-bot.log; pkill -f "bin/artel"
```

## ⚠️ Ключевое правило архитектуры: инструменты, не команды бота

**Новые функции добавляются исключительно через инструменты агента** (`internal/tools/`), а не через slash-команды
Telegram-бота.

Telegram-команды (`/команда`) — только для базовых системных операций, которые нужны **без участия LLM**:

- управление диалогом (`/new`)
- переключение модели/провайдера (`/model`, `/provider`)
- системный статус (`/status`, `/context`)
- справка (`/help`, `/start`)

**Никогда не добавляй новые slash-команды** в `internal/telegram/bot.go` для новых пользовательских функций. Всё новое —
инструмент в `internal/tools/`, зарегистрированный в `cmd/agent/tools.go`.

## Конфигурация

Скопировать и заполнить:

```bash
cp config.example.yaml config.yaml
```

Ключевые параметры:

```yaml
telegram:
  token: "<токен от @BotFather>"
  allowed_users: [ <твой chat_id> ]  # whitelist — только ты
  proxy: ""                     # опционально: прокси только для Telegram

llm:
  provider: "openai"            # условное имя провайдера LLM
  base_url: "https://api.openai.com/v1"
  api_key: "<API-ключ провайдера>"
  models:
    default: "gpt-4.1"
    fast: "gpt-4.1-mini"
  max_iterations: 10            # лимит шагов ReAct-цикла
  max_context_messages: 50      # лимит сообщений в окне LLM-контекста (0 = без ограничений)

  # Мультипровайдер (опционально): при наличии providers — base_url/api_key выше игнорируются
  providers: # список провайдеров с приоритетом (priority: меньше = первым)
    - name: "openai"
      base_url: "..."
      api_key: "..."
      priority: 1
      models: { default: "gpt-4.1", fast: "gpt-4.1-mini" }
    - name: "openrouter"
      base_url: "https://openrouter.ai/api/v1"
      api_key: "..."
      priority: 2
      models: { default: "google/gemini-2.5-flash", fast: "google/gemini-2.5-flash" }
      extra_headers: { HTTP-Referer: "...", X-Title: "..." }
      proxy: ""
      timeout_seconds: 120
  retry:
    max_retries: 2
    initial_backoff_ms: 500
    max_backoff_ms: 5000

database:
  path: "./data/agent.db"

shell:
  enabled: false                # включать осторожно
  timeout: 30                   # таймаут выполнения (сек)
  allowed_commands: [ ... ]       # whitelist команд
  blocked_patterns: [ ... ]       # запрещённые паттерны
  working_dir: "./sandbox"      # sandbox директория

web:
  enabled: true
  provider: "brave"             # brave | serper | duckduckgo
  api_key: ""                   # API ключ для brave/serper
  max_results: 5
  timeout: 15
  max_fetch_size_kb: 100

browser:
  enabled: true
  screenshot_dir: "./data/screenshots"
  agent_browser_path: "agent-browser"
  timeout_seconds: 30
  proxy: ""                     # прокси для браузера (опционально)

pi_coder:
  enabled: false
  model: "gpt-4.1"               # модель по умолчанию
  timeout: 300                     # таймаут по умолчанию (сек)
  projects:                        # список проектов (явные)
    - name: "artel"
      path: "."
    # - name: "my-project"
    #   path: "/path/to/my-project"
    #   model: "gpt-4.1-mini"      # переопределение (опц.)
    #   timeout: 600               # переопределение (опц.)
  # projects_dir: "/home/projects"   # автообнаружение git-репозиториев в каталоге
  # exclude_projects: ["test-project", "nanoGPT"]  # исключить по имени

weather:
  enabled: true                 # Open-Meteo, бесплатно, без API-ключа
  timeout_seconds: 15
  proxy: ""

files:
  enabled: false                # включить для обработки файлов/изображений из Telegram
  storage_dir: "./data/files"
  max_file_size_kb: 10240       # 10 МБ
  vision_enabled: false         # передавать изображения в LLM как base64

code_runner:
  enabled: false                # включить для выполнения кода (Python/Go/bash)
  timeout_seconds: 30
  mem_limit_mb: 0               # 0 = без ограничений

news:
  enabled: false                # включить для агрегации новостей
  fetch_interval_minutes: 30
  max_item_age_days: 30
  max_items_per_feed: 1000
  fetch_timeout_seconds: 30
  proxy: ""
  feeds:
    - url: "https://techcrunch.com/feed/"
      title: "TechCrunch"
      category: "tech"
      enabled: false

monitor:
  enabled: false                # включить для мониторинга доступности сайтов
  interval_minutes: 5           # интервал проверок
  timeout_seconds: 10           # таймаут HTTP-запроса
  proxy: ""
  sites:
    - url: "https://example.com"
      name: "Мой сайт"

logging:
  level: "info"                 # debug | info | warn | error
  file: "./data/agent.log"
```

Либо через переменные окружения:

```bash
export TELEGRAM_TOKEN="..."
export LLM_API_KEY="..."
```

## Архитектура агента

**ReAct loop** в `internal/agent/agent.go`:

1. Загрузить историю диалога из SQLite
2. Создать корневой OpenTelemetry span (`agent.run`)
3. Отправить в LLM (messages + system prompt + tool definitions) — child span `llm.complete`
4. Если LLM вернул tool_calls → **валидация аргументов через JSON Schema** → выполнить инструменты (span `tool.execute`) → добавить результаты в messages → повторить (max 10 итераций)
5. Если LLM вернул текст → сохранить в БД → вернуть пользователю

**OpenTelemetry**: каждый шаг ReAct-цикла трассируется через OTLP HTTP (опционально, `otel.endpoint`).
Спаны: `agent.run` → `agent.iteration` → `llm.complete` + `tool.execute`. В продакшене трейсы
отправляются в Jaeger/Grafana Tempo; для разработки доступен stdout-экспортер.

**JSON Schema валидация**: перед вызовом `tool.Execute()` аргументы проверяются через `gojsonschema`
против схемы из `tool.Parameters()`. Невалидные payloads отклоняются до вызова инструмента — нет
nil-pointer паник от неправильных аргументов.

## Интерфейс Transport

Общий транспорт-интерфейс изолирует ядро (`internal/agent`) от конкретного мессенджера.

```go
type Transport interface {
    Run(ctx context.Context) error
    Send(ctx context.Context, m Message) error
    Messages() <-chan InboundMessage
    Close() error
}
```

- `internal/transport/transport.go` — интерфейс и нормализованные типы сообщений.
- `internal/telegram/transport.go` — реализация для Telegram; long-polling остаётся в `bot.go`.
- `internal/mattermost/transport.go` — реализация для Mattermost (REST + WebSocket).
- `cmd/agent/main.go` выбирает транспорт по `config.transport.type` (`telegram` или `mattermost`).

## Mattermost transport

Реализован в `internal/mattermost/`:

- REST-клиент для отправки сообщений и получения информации о каналах/пользователях.
- WebSocket-клиент, слушающий события `posted`, и адаптирующий их в `transport.InboundMessage`.
- Авторизация по токену бота; необходимо задать `mattermost.server_url`, `token`, `bot_username`.
- DM детектируется по `channel_type`; @-mentions парсятся в `Mentions`.

## Multi-agent авторизация

Gate авторизации живёт в `internal/transport/auth.go` и используется транспортным слоем
(или диспетчером) при получении входящих сообщений:

- `owner_user_id` — человек-супервизор, полный доступ.
- `team_bots` — whitelist ботов-коллег; сообщение от них разрешено только при @-mention.
- Боты вне whitelist игнорируются.
- Собственные сообщения (`sender_id == self_bot_id`) пропускаются предохранителем.

## Предохранители петель

Пакет `internal/safeguards` защищает от бесконечных бот-в-бот диалогов:

1. **Self-skip** — не отвечать самому себе.
2. **Лимит итераций на тред** — `warn` после `loop_warn_bot_messages`, `suppress`
   после `loop_suppress_bot_messages` с эскалацией человеку; reset на сообщение человека.
3. **Rate-limit** — `max_replies_per_minute` на тред (скользящее окно 1 минута).

## Роли инстансов

Секция `role:` позволяет одному бинарнику работать под разными «должностями»:

```yaml
role:
  name: "developer"
  system_prompt: |
    Ты — разработчик команды. ...
```

`role.name` используется в логах/`/status`, `role.system_prompt` переопределяет
встроенный системный промпт. Роли используются в основном в multi-agent режиме Mattermost.

## Добавление нового инструмента

1. Создать файл `internal/tools/mytool.go` реализующий интерфейс `Tool`:

```go
type Tool interface {
Name()        string
Description() string
Parameters()  map[string]interface{}  // JSON Schema
Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

2. Зарегистрировать в `cmd/agent/tools.go` (в `registerOptionalTools` или `registerWeatherTool`-подобную функцию):

```go
registry.Register(tools.NewMyTool(cfg, logger))
```

3. При необходимости добавить секцию конфига в `internal/config/config.go`.

**Не добавляй slash-команды в `internal/telegram/bot.go`** — только инструменты.

## Инструмент pi_coder

Позволяет агенту анализировать, разрабатывать и тестировать проекты через [pi](https://github.com/mariozechner/pi).
Проекты задаются списком в конфигурации (`pi_coder.projects`) или **автообнаруживаются** из каталога
(`pi_coder.projects_dir` — сканирование на git-репозитории). Исключить проекты по имени можно через
`pi_coder.exclude_projects`. Явные проекты из `pi_coder.projects` переопределяют автообнаруженные с тем же именем.
Один из проектов может быть кодом самого агента (self-project — автоопределение по совпадению пути с директорией агента).

| Параметр   | Тип    | Описание                                                                              |
|------------|--------|---------------------------------------------------------------------------------------|
| `project`  | string | Имя проекта из списка `pi_coder.projects` (обязательный)                              |
| `task`     | string | Задача для pi: анализ, разработка, тестирование, рефакторинг (обязательный)          |
| `rebuild`  | bool   | Пересобрать проект (`make build`) — только для self-project                           |
| `restart`  | bool   | Перезапустить агента после успешной сборки — только для self-project (включает rebuild) |

Для внешних (non-self) проектов `rebuild` и `restart` игнорируются с предупреждением в выводе.

**Механизм безопасного перезапуска** (`restart=true`, только для self-project):

1. `Execute()` запускает pi в директории проекта, затем `make build`
2. Если сборка успешна — запускается goroutine с задержкой 5 секунд
3. `Execute()` возвращает ответ агенту → Telegram получает сообщение
4. Через 5 сек: `syscall.Exec(os.Executable(), os.Args, os.Environ())` — замена процесса без fork, тот же PID
5. Если сборка провалилась — перезапуск отменяется, агент продолжает работу со старым кодом

Типичный сценарий:

1. Пользователь просит проанализировать проект / добавить функцию
2. Агент вызывает `pi_coder` с `project` и `task`
3. pi запускается в директории указанного проекта
4. Для self-project с `restart=true`: `make build` пересобирает, агент перезапускается
5. Для non-self: pi работает, rebuild/restart игнорируются

## Безопасность shell

Shell tool (`internal/tools/shell.go`) имеет многоуровневую защиту:

- **Whitelist команд** — только разрешённые (`ls`, `cat`, `grep`, `find` и др.)
- **Blocked patterns** — запрещённые паттерны (`rm -rf /`, `mkfs`, `shutdown` и др.)
- **Запрет `sudo`** и **перенаправления вывода** (`>`)
- **Таймаут** выполнения (30 сек)
- **Sandbox директория** — `./sandbox/`
- **По умолчанию выключен** (`shell.enabled: false` в конфиге)

## Telegram команды

Команды — только системные операции без участия LLM. Новые функции добавляются через инструменты, а не команды.

| Команда            | Действие                                                      |
|--------------------|---------------------------------------------------------------|
| `/start`           | приветствие                                                   |
| `/new`             | начать новый диалог (сбросить контекст)                       |
| `/model`           | показать текущую модель и доступные алиасы                    |
| `/model fast`      | переключиться на быструю модель                               |
| `/model default`   | переключиться на основную модель                              |
| `/provider`        | показать текущий провайдер LLM и статус провайдеров           |
| `/provider <name>` | принудительно выбрать провайдера (без fallback)               |
| `/provider auto`   | вернуть автоматический режим (по приоритету с fallback)       |
| `/context`         | статистика окна контекста (сообщений, токенов, суммаризация)  |
| `/status`          | статус агента: uptime, модель, БД, токены за день, провайдеры |
| `/help`            | справка                                                       |

## Зависимости

| Пакет                                                | Назначение                |
|------------------------------------------------------|---------------------------|
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API               |
| `github.com/gorilla/websocket`                              | WebSocket клиент Mattermost  |
| `modernc.org/sqlite`                                 | SQLite (pure Go, без CGO)      |
| `gopkg.in/yaml.v3`                                   | парсинг конфига                |
| `github.com/mmcdole/gofeed`                          | RSS/Atom/JSON Feed парсер      |
| `github.com/xeipuuv/gojsonschema`                    | JSON Schema валидация          |
| `go.opentelemetry.io/otel`                           | OpenTelemetry tracing          |
| `go.opentelemetry.io/otel/sdk/trace/tracetest`       | in-memory exporter для тестов  |

## Реализованные функции

- [x] Telegram бот-интерфейс (long polling, auth по chat_id)
- [x] Диалог с LLM через OpenAI-compatible API
- [x] История диалогов (SQLite, персистентная)
- [x] Окно контекста с лимитом сообщений и суммаризацией
- [x] Заметки — создание, поиск, теги, редактирование, удаление, чтение
- [x] Полнотекстовый поиск заметок (SQLite FTS5, fallback на LIKE)
- [x] Напоминания — create/list/delete/done, повторяющиеся (daily/weekly/Nm/Nh/Nd), уведомления в Telegram
- [x] Безопасный shell (whitelist, blocked patterns, sandbox, таймаут)
- [x] Поиск в интернете (Brave / Serper / DuckDuckGo) + чтение страниц
- [x] Мультипроектная разработка через `pi_coder` — список проектов в конфиге, per-project model/timeout, rebuild/restart только для self-project
  вмешательства (`restart=true` → `syscall.Exec`)
- [x] **Автообнаружение проектов** — `pi_coder.projects_dir` сканирует каталог на git-репозитории; `exclude_projects` для исключения; явные `projects` переопределяют автообнаруженные
- [x] Агрегация новостей (RSS/Atom/JSON Feed) — подписки, чтение, поиск, избранное
- [x] Универсальная автоматизация браузера (agent-browser) — навигация, клики, формы, скриншоты, JS, snapshot
- [x] Переключение модели и провайдера через Telegram (`/model fast`, `/provider openrouter`)
- [x] Прогноз погоды (Open-Meteo) — текущие условия, прогноз на 1–7 дней, геокодирование городов
- [x] Котировки акций и индексов — US (через Yahoo Finance) + RU (через MOEX ISS); S&P 500, Nasdaq, DJI, Russell, IMOEX,
  RTSI, любые тикеры; кэш в SQLite
- [x] Мультипровайдер LLM с fallback — любые OpenAI-compatible API; retry с backoff, circuit breaker
- [x] Файлы — приём файлов и изображений через Telegram, list/read/delete; Vision API для изображений
- [x] Выполнение кода — Python, Go, bash в изолированной временной директории с таймаутом и лимитом памяти
- [x] Статистика токенов — учёт prompt/completion токенов по дням, отчёт через `/status`
- [x] Проактивные дайджесты — финансовая сводка (stocks) в утреннем брифинге; вечерний дайджест новостей (news) и
  вечерняя финансовая сводка рынков как отдельные большие сообщения; тикеры конфигурируются через `financial_tickers`
- [x] Docker-образ (alpine, pure Go без CGO) и Docker Compose; CI/CD через GitHub Actions (lint + test + docker publish)
- [x] **Docker multi-bot** — 3 бота (analyst, developer, tester) в Docker с автозапуском через systemd; `docker-compose.bots.yml`, `configs/*-bot.yaml.example`, `scripts/agent-bots.service`
- [x] Мониторинг сайтов — фоновые HTTP-проверки с алертами в Telegram при смене состояния (🔴/✅); ручная проверка через инструмент `monitor`
- [x] Личная вики — иерархия путей (`/dev/golang`), `[[path]]` ссылки с cascade rename, backlinks; create/get/edit/delete/list/tree
- [x] Список для чтения — сохранение URL (fetch + контент), list/get/update/delete, дайджест непрочитанных в утреннем брифинге
- [x] Unified поиск по базе знаний — одновременный поиск по вики и reading list (FTS5 + LIKE fallback)
- [x] Семантический поиск заметок (RAG) — embedding через Ollama + cosine similarity, гибридный поиск (FTS5 + векторный)
- [x] OpenTelemetry трассировка — agent.run, agent.iteration, llm.complete, tool.execute; OTLP HTTP + stdout exporters
- [x] JSON Schema валидация аргументов инструментов — проверка до вызова Execute(), защита от невалидных tool_calls
- [x] Юнит-тесты ReAct loop — max_iterations, unknown tool, tool error, OTel spans
- [x] **Транспорт-абстракция** — общий `Transport` интерфейс; Telegram и Mattermost реализации
- [x] **Mattermost transport** — REST/WebSocket клиент, @-mention/DM, DM vs channel
- [x] **Multi-agent авторизация** — `transport.AuthGate`: owner + team_bots whitelist + @-mention
- [x] **Предохранители петель** — `safeguards.Guard`: self-skip, loop warn/suppress, rate-limit
- [x] **Роли инстансов** — `role.name` + `role.system_prompt` для multi-agent команды

## Инструмент news

Агрегация новостей из RSS/Atom/JSON Feed источников.

**Возможности:**

- Подписка на произвольные RSS/Atom/JSON Feed источники
- Автоматическое обновление фидов (по расписанию)
- Поиск новостей по тексту, тегам, источникам
- Фильтрация по дате, категории, языку
- Сохранение новостей в избранное
- Чтение статей через агент (web_fetch)

**Конфигурация:**

```yaml
news:
  enabled: true
  fetch_interval_minutes: 30
  max_item_age_days: 30
  feeds:
    - url: "https://techcrunch.com/feed/"
      title: "TechCrunch"
      category: "tech"
      language: "en"
      enabled: true
    - url: "https://go.dev/blog/feed.atom"
      title: "Go Blog"
      category: "golang"
      enabled: true
```

Управление новостями — через инструмент `news` в диалоге с агентом (не через slash-команды).

## Инструмент monitor

Мониторинг доступности HTTP/HTTPS сайтов с алертами в Telegram.

**Возможности:**

- Фоновый цикл проверяет сайты с заданным интервалом
- Алерт при смене состояния: сайт упал 🔴 / восстановился ✅
- Инструмент `monitor` для ручных проверок и просмотра истории

**Конфигурация:**

```yaml
monitor:
  enabled: true
  interval_minutes: 5
  timeout_seconds: 10
  sites:
    - url: "https://example.com"
      name: "Мой сайт"
```

Управление — через инструмент `monitor` в диалоге с агентом (не через slash-команды).

## База знаний (wiki + reading_list + knowledge_search)

Личная база знаний с иерархической вики и списком для чтения.

**wiki** — иерархические заметки с путями как в файловой системе:
- Пути: `/dev/golang`, `/health/бег`, `/project/ideas`
- Синтаксис `[[path]]` для ссылок между страницами
- Cascade rename: при переименовании пути все ссылки обновляются атомарно (транзакция)
- Операции: create/get/edit/delete/list/tree/rename/backlinks

**reading_list** — список для чтения и сохранённые статьи:
- `save` — сохранить URL (автоматически fetch контент через web_fetch)
- `add` — добавить запись вручную (без fetch)
- list/get/update/delete
- `digest` — дайджест непрочитанных; включён в утренний брифинг

**knowledge_search** — unified поиск сразу по вики и reading list:
- FTS5 полнотекстовый поиск, fallback на LIKE
- Поиск в одном запросе, результаты с указанием источника (wiki/reading_list)

---

## Паттерны разработки (из опыта)

### A. SQLite UTC timezone gotcha

`CURRENT_TIMESTAMP` в SQLite всегда хранится в **UTC**. `time.Now()` в Go возвращает локальное время.
При передаче Go-времени как параметра SQL-запроса всегда форматировать в UTC:

```go
// ✅ правильно
updatedBefore.UTC().Format("2006-01-02 15:04:05")

// ❌ сломается на машинах с non-UTC таймзоной
updatedBefore.Format("2006-01-02 15:04:05")
```

Касается любых сравнений `WHERE updated_at < ?`, `WHERE created_at >= ?` и т.п.

### B. cyclop лимит = 20: стратегия снижения сложности

golangci-lint блокирует функции с цикломатической сложностью > 20.
Рецепт: выносить логические блоки в отдельные функции **без receiver** (свободные функции):

```go
// Вместо разрастания метода:
func (t *WorkoutTool) execLog(...) (string, error) {
...
insertSets(ctx, id, a.Sets) // free func, не метод
writeLogSets(&sb, a.Sets) // free func
writeNewPRs(&sb, db, ctx, ...) // free func
...
}
```

Free-функции с префиксом `write`/`insert`/`format` — стандартный приём в этом проекте.

### C. Storage: один файл на домен, `sqlite.go` = DDL only

Схема хранилища разделена по доменам:

- `internal/storage/sqlite.go` — **только** DDL-миграции (`CREATE TABLE IF NOT EXISTS`, индексы, additive migrations)
- `internal/storage/discipline.go` — типы + CRUD для discipline/goals
- `internal/storage/workout.go` — типы + CRUD для workouts
- `internal/storage/reminders.go` — типы + CRUD для reminders

Новый домен → новый файл хранилища. Типы (`Workout`, `WorkoutSet`, …) объявляются в файле своего домена.

### D. proactiveScheduler: `0 = disabled`, конструктор не перезаписывает

Для config-флагов с семантикой «`0` = выключено» конструктор **не должен** подставлять дефолт:

```go
// ✅ правильно: только MaxMessagesPerDay требует положительного значения
if cfg.MaxMessagesPerDay <= 0 {
cfg.MaxMessagesPerDay = 7
}
// GoalReminderDaysIdle: НЕ трогаем — 0 означает «фича выключена»

// tick() — единственная точка проверки включённости:
if s.cfg.GoalReminderDaysIdle > 0 && s.db != nil {
s.maybeFireEvent(...)
}
```

Нарушение этого принципа делает тест зелёным по ложной причине (`db == nil` защищает там, где должен защищать `0`).

### E. Docker: known issues и решения

При доработке Docker multi-bot следить за пятью Known issues:

1. **Ollama на 127.0.0.1** — контейнеры не достучатся через `host-gateway`.
   Фикс: `OLLAMA_HOST=0.0.0.0:11434` через systemd override.

2. **pi models.json hardcoded 127.0.0.1** — `~/.pi/agent/models.json` хранит
   `baseUrl: "http://127.0.0.1:11434/v1"`. Env-переменные `OLLAMA_HOST` не переопределяют.
   Фикс: `sed -i 's|127.0.0.1:11434|host-gateway:11434|g' ~/.pi/agent/models.json`.

3. **Версия pi ≠ хост** — расширения из `~/.pi/agent/` несовместимы с другой версией pi.
   Фикс: фиксировать версию в Dockerfile (`@earendil-works/pi-coding-agent@0.80.6`).

4. **~/.pi read-only** — pi не может создавать session-файлы и lock-файлы.
   Фикс: монтировать как `:rw` в `docker-compose.bots.yml`.

5. **Node.js версия** — pi 0.80.6 требует Node ≥22.9.0, alpine:3.20 даёт Node 20.15.1.
   Фикс: использовать `node:22-alpine` как runtime-stage в Dockerfile.

Подробности: `docs/e2e-testing.md` (раздел «Известные проблемы и решения»).

### E. FK-валидация до создания записи

Когда инструмент принимает ссылку на другую сущность (напр. `goal_id` в workout),
валидировать существование и принадлежность чату **до** INSERT:

```go
// ✅ в execLog перед db.CreateWorkout:
if a.GoalID != nil {
if err := t.validateGoalID(ctx, a.ChatID, *a.GoalID); err != nil {
return "", err // нет осиротевших записей
}
}
```

Паттерн `validateXxx(ctx, chatID, id)` использует `GetXxx(ctx, chatID, id)` и проверяет `== nil`.
Работает для любых FK-ссылок между инструментами (workout→goal, note→tag и т.п.).
