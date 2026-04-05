# agent-shatrov

Персональный AI-агент с Telegram-интерфейсом для личного использования на локальном компьютере.

## Стек

- **Go 1.26**
- **LLM**: GLM-4.7 / GLM-4.7-Flash (Zhipu AI / z.ai) через OpenAI-compatible API
- **Интерфейс**: Telegram Bot (long polling)
- **Хранилище**: SQLite (`data/agent.db`)
- **Прокси**: `HTTPS_PROXY=http://127.0.0.1:2080`

## Структура проекта

```
cmd/agent/main.go          — точка входа, wiring всех зависимостей
internal/
  config/config.go         — загрузка config.yaml + override через env
  agent/agent.go           — ядро: ReAct loop (LLM → tools → LLM → ответ)
  llm/client.go            — HTTP-клиент к OpenAI-compatible API
  tools/
    registry.go            — реестр инструментов, интерфейс Tool
    notes.go               — инструмент: заметки (create/search/list) через SQLite
    shell.go               — инструмент: безопасный shell (whitelist + sandbox)
    web_search.go          — инструмент: поиск в интернете
    web_fetch.go           — инструмент: чтение веб-страниц
    pi_coder.go            — инструмент: доработка агента через pi
    news.go                — инструмент: агрегация новостей (RSS/Atom/JSON)
  storage/
    sqlite.go              — SQLite: схема БД, migrations, CRUD для conversations/messages/notes
    news.go                — SQLite: новости, фиды, подписки
  telegram/
    bot.go                 — Telegram bot: long polling, auth по chat_id, обработка команд
    markdown.go            — Markdown → HTML конвертация для Telegram
  news/
    parser.go              — парсер фидов (RSS/Atom/JSON Feed)
    feed.go                — структура фида
    item.go                — структура новости
    subscription.go        — подписка на фид
    fetcher.go             — загрузка фидов по HTTP
    manager.go             — менеджер новостей: фильтрация, поиск
    filter.go              — фильтры новостей
    errors.go              — ошибки пакета news
data/                      — БД и логи (в .gitignore)
config.yaml                — конфиг с секретами (в .gitignore)
config.example.yaml        — пример конфига без секретов
Makefile                   — команды для управления сервисом
```

## Команды

```bash
make run      # собрать и запустить сервис (с прокси)
make stop     # остановить сервис
make restart  # перезапустить
make logs     # tail -f data/agent.log
make build    # только сборка бинарника → bin/agent
make clean    # удалить бинарник
make lint     # статический анализ (golangci-lint)
make test     # запуск тестов
```

## Обязательно после каждой доработки

После **любых** изменений в коде необходимо запустить:
```bash
make lint test
```
Все найденные ошибки и предупреждения должны быть исправлены до завершения задачи. Коммитить код с ошибками lint или упавшими тестами нельзя.

## Конфигурация

Скопировать и заполнить:
```bash
cp config.example.yaml config.yaml
```

Ключевые параметры:
```yaml
telegram:
  token: "<токен от @BotFather>"
  allowed_users: [<твой chat_id>]  # whitelist — только ты
  proxy: ""                     # опционально: прокси только для Telegram

llm:
  provider: "glm"               # провайдер LLM
  base_url: "https://api.z.ai/api/coding/paas/v4"
  api_key: "<ключ из ~/.pi/agent/auth.json, провайдер zai>"
  models:
    default: "glm-4-plus"
    fast: "glm-4-flash"
  max_iterations: 10            # лимит шагов ReAct-цикла

database:
  path: "./data/agent.db"       # путь к SQLite БД

shell:
  enabled: false                # включать осторожно
  timeout: 30                   # таймаут выполнения (сек)
  allowed_commands: [...]       # whitelist команд
  blocked_patterns: [...]       # запрещённые паттерны
  working_dir: "./sandbox"     # sandbox директория

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
  enabled: true
  project_dir: "/path/to/agent-shatrov"
  model: "glm-4.7"
  timeout: 300

news:
  enabled: false                # включить для агрегации новостей
  fetch_interval_minutes: 30    # интервал автообновления
  max_item_age_days: 30         # макс. возраст новостей
  max_items_per_feed: 1000
  fetch_timeout_seconds: 30
  proxy: ""                     # прокси для фидов
  feeds:
    - url: "https://techcrunch.com/feed/"
      title: "TechCrunch"
      category: "tech"
      enabled: false

logging:
  level: "info"                 # debug | info | warn | error
  file: "./data/agent.log"
```

Либо через переменные окружения:
```bash
export TELEGRAM_TOKEN="..."
export GLM_API_KEY="..."
```

## Архитектура агента

**ReAct loop** в `internal/agent/agent.go`:
1. Загрузить историю диалога из SQLite
2. Отправить в LLM (messages + system prompt + tool definitions)
3. Если LLM вернул tool_calls → выполнить инструменты → добавить результаты в messages → повторить (max 10 итераций)
4. Если LLM вернул текст → сохранить в БД → вернуть пользователю

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
2. Зарегистрировать в `cmd/agent/main.go` (в `registerOptionalTools` или напрямую):
```go
toolRegistry.Register(tools.NewMyTool(...))
```

## Инструмент pi_coder

Позволяет агенту изменять собственный исходный код через [pi](https://github.com/mariozechner/pi).
Запускает `pi --print --model glm-4.7 --no-session <task>` в директории проекта.

| Параметр | Тип | Описание |
|----------|-----|---------|
| `task` | string | Что нужно изменить в коде (обязательный) |
| `rebuild` | bool | Пересобрать бинарник после изменений (`make build`) |
| `restart` | bool | Безопасно перезапустить агента после успешной сборки; автоматически включает `rebuild` |

**Механизм безопасного перезапуска** (`restart=true`):
1. `Execute()` запускает pi, затем `make build`
2. Если сборка успешна — запускается goroutine с задержкой 5 секунд
3. `Execute()` возвращает ответ агенту → Telegram получает сообщение
4. Через 5 сек: `syscall.Exec(os.Executable(), os.Args, os.Environ())` — замена процесса без fork, тот же PID
5. Если сборка провалилась — перезапуск отменяется, агент продолжает работу со старым кодом

Типичный сценарий:
1. Пользователь просит добавить функцию через Telegram
2. Агент вызывает `pi_coder` с `restart=true`
3. pi редактирует файлы проекта
4. `make build` пересобирает бинарник
5. Агент отвечает «перезапускаюсь через 5 сек...» и заменяет процесс новым бинарником

## Безопасность shell

Shell tool (`internal/tools/shell.go`) имеет многоуровневую защиту:
- **Whitelist команд** — только разрешённые (`ls`, `cat`, `grep`, `find` и др.)
- **Blocked patterns** — запрещённые паттерны (`rm -rf /`, `mkfs`, `shutdown` и др.)
- **Запрет `sudo`** и **перенаправления вывода** (`>`)
- **Таймаут** выполнения (30 сек)
- **Sandbox директория** — `./sandbox/`
- **По умолчанию выключен** (`shell.enabled: false` в конфиге)

## Telegram команды

| Команда | Действие |
|---------|----------|
| `/start` | приветствие |
| `/new` | начать новый диалог (сбросить контекста) |
| `/model` | показать текущую модель и доступные алиасы |
| `/model fast` | переключиться на быструю модель |
| `/model default` | переключиться на основную модель |
| `/help` | справка |
| `/news [запрос]` | последние новости или поиск по запросу |
| `/unread` | непрочитанные новости |
| `/saved` | сохранённые новости |
| `/feeds` | список подписок на фиды |

## Зависимости

| Пакет | Назначение |
|-------|-----------|
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API |
| `github.com/mattn/go-sqlite3` | SQLite (CGo) |
| `gopkg.in/yaml.v3` | парсинг конфига |
| `github.com/stretchr/testify` | assertions в тестах |
| `github.com/mmcdole/gofeed` | RSS/Atom/JSON Feed парсер |

## Реализованные функции

- [x] Telegram бот-интерфейс (long polling, auth по chat_id)
- [x] Диалог с GLM-4.7 / GLM-4.7-Flash
- [x] История диалогов (SQLite, персистентная)
- [x] Заметки — создание, поиск, теги, редактирование, удаление, чтение
- [x] Полнотекстовый поиск заметок (SQLite FTS5, fallback на LIKE)
- [x] Безопасный shell (whitelist, blocked patterns, sandbox, таймаут)
- [x] Поиск в интернете (Brave / Serper / DuckDuckGo) + чтение страниц
- [x] Самообновление через `pi_coder` — агент изменяет, пересобирает и **перезапускает себя** без ручного вмешательства (`restart=true` → `syscall.Exec`)
- [x] Агрегация новостей (RSS/Atom/JSON Feed) — подписки, чтение, поиск, избранное
- [x] Универсальная автоматизация браузера (agent-browser) — навигация, клики, формы, скриншоты, JS, snapshot
- [x] Переключение модели через Telegram (`/model fast`)
- [x] Прогноз погоды (Open-Meteo) — текущие условия, прогноз на 1–7 дней, геокодирование городов

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

**Telegram команды:**
- `/news` — последние новости
- `/news запрос` — поиск по запросу
- `/unread` — непрочитанные
- `/saved` — сохранённые в избранное
- `/feeds` — список подписок
