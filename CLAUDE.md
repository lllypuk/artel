# Artel

Персональный AI-агент с Telegram/Mattermost-интерфейсами для личного использования на локальном компьютере.

## Стек

- **Go 1.26**
- **LLM**: любой OpenAI-compatible провайдер (multi-provider с fallback)
- **Интерфейс**: Telegram Bot (long polling) · Mattermost Bot (REST + WebSocket)
- **Хранилище**: SQLite (`data/agent.db`)
- **Прокси**: `HTTPS_PROXY=http://127.0.0.1:2080`

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
make build && go test ./...
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

Секции и значения по умолчанию — в `config.example.yaml`. Ключевые env-переменные:

```bash
export TELEGRAM_TOKEN="..."
export LLM_API_KEY="..."
```

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
// GoalReminderDaysIdle: НЕ трогаем — 0 означает «фича выключена"

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
