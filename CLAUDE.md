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
cmd/agent/main.go          — точка входа, сборка всех зависимостей (wiring)
internal/
  config/config.go         — загрузка config.yaml + override через env
  agent/agent.go           — ядро: ReAct loop (LLM → tools → LLM → ответ)
  llm/client.go            — HTTP-клиент к OpenAI-compatible API (chat + streaming)
  tools/
    registry.go            — реестр инструментов, интерфейс Tool
    notes.go               — инструмент: заметки (create/search/list) через SQLite
    shell.go               — инструмент: безопасный shell (whitelist + sandbox)
  storage/sqlite.go        — SQLite: схема БД, migrations, CRUD для conversations/messages/notes
  telegram/bot.go          — Telegram bot: long polling, auth по chat_id, обработка команд
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

llm:
  base_url: "https://api.z.ai/api/coding/paas/v4"  # z.ai Coding endpoint
  api_key: "<ключ из ~/.pi/agent/auth.json, провайдер zai>"
  models:
    default: "glm-4.7"
    fast: "glm-4.7-flash"

shell:
  enabled: false  # включать осторожно
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
2. Зарегистрировать в `cmd/agent/main.go`:
```go
toolRegistry.Register(tools.NewMyTool(...))
```

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
| `/new` | начать новый диалог (сбросить контекст) |
| `/help` | справка |

## Зависимости

| Пакет | Назначение |
|-------|-----------|
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram Bot API |
| `github.com/mattn/go-sqlite3` | SQLite (CGo) |
| `gopkg.in/yaml.v3` | парсинг конфига |

## Планируемые функции

- [x] Web search tool
- [ ] News aggregation (RSS)
- [ ] Полнотекстовый поиск заметок (SQLite FTS5)
- [ ] Редактирование и удаление заметок
- [ ] Переключение модели через Telegram (`/model fast`)
- [ ] Shell tool (после тщательного тестирования)
- [ ] Системное администрирование
- [ ] Кодинг-ассистент
