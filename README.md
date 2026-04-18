# agent-shatrov

Персональный AI-агент с Telegram-интерфейсом для личного использования на локальном компьютере.

## Стек

- **Go 1.26**
- **LLM**: GLM-4.7 / GLM-4.7-Flash (z.ai, OpenAI-compatible API)
- **Интерфейс**: Telegram Bot (long polling)
- **Хранилище**: SQLite
- **Безопасность**: whitelist/sandbox для shell-команд

## Быстрый старт

```bash
git clone https://github.com/lllypuk/agent-shatrov.git
cd agent-shatrov

cp config.example.yaml config.yaml
# заполнить telegram.token, telegram.allowed_users, llm.api_key

make run
```

## Команды

```bash
make run      # собрать и запустить
make stop     # остановить
make restart  # перезапустить
make logs     # смотреть логи в реальном времени
make build    # только сборка
make lint     # статический анализ (golangci-lint)
make test     # запуск тестов
make clean    # удалить бинарник
```

## Структура

```
cmd/agent/
  main.go            — точка входа, wiring
  tools.go           — регистрация инструментов
  news.go            — setup новостей и цикл обновления фидов
  reminders.go       — планировщик напоминаний
  logger.go          — настройка логгера
internal/
  config/config.go   — конфигурация (YAML + env)
  agent/agent.go     — ядро агента (ReAct loop)
  llm/
    client.go        — клиент к LLM API (OpenAI-compatible)
    multi.go         — мультипровайдер с fallback, retry, circuit breaker
  tools/
    registry.go      — реестр инструментов
    notes.go         — заметки (create/search/edit/delete)
    reminders.go     — напоминания (create/list/delete/done)
    shell.go         — безопасный shell (whitelist + sandbox)
    web_search.go    — поиск в интернете
    web_fetch.go     — чтение веб-страниц
    browser.go       — автоматизация браузера (agent-browser)
    pi_coder.go      — доработка агента через pi
    news.go          — агрегация новостей (RSS/Atom/JSON)
    weather.go       — прогноз погоды (Open-Meteo)
    files.go         — управление файлами (list/read/delete)
    code_runner.go   — выполнение кода (Python/Go/bash)
  storage/
    sqlite.go        — SQLite: диалоги, сообщения, заметки, токены
    news.go          — SQLite: новости, фиды, подписки
  telegram/
    bot.go           — Telegram бот
    markdown.go      — Markdown → HTML конвертация
  news/              — парсер/менеджер RSS/Atom/JSON Feed
config.example.yaml  — пример конфигурации
Makefile             — управление сервисом
```

## Возможности

- [x] Telegram бот-интерфейс (long polling, auth по chat_id)
- [x] Диалог с GLM-4.7 / GLM-4.7-Flash
- [x] История диалогов (SQLite, персистентная)
- [x] Окно контекста с лимитом сообщений и суммаризацией
- [x] Заметки — создание, поиск, теги, редактирование, удаление
- [x] Напоминания — create/list/delete/done, повторяющиеся, push-уведомления в Telegram
- [x] Безопасный shell (whitelist команд, blocked patterns, sandbox, таймаут)
- [x] Поиск в интернете (Brave / Serper / DuckDuckGo) + чтение страниц
- [x] Самообновление через `pi_coder` — агент изменяет, пересобирает и **перезапускает себя** без ручного вмешательства (`restart=true` → `syscall.Exec`)
- [x] Агрегация новостей — подписки на RSS/Atom/JSON фиды, поиск, избранное
- [x] Автоматизация браузера (agent-browser) — навигация, клики, формы, скриншоты, JS
- [x] Прогноз погоды (Open-Meteo) — текущие условия, прогноз на 1–7 дней, геокодирование
- [x] Мультипровайдер LLM с fallback — OpenRouter, z.ai, любой OpenAI-compatible API; retry с backoff, circuit breaker
- [x] Файлы — приём файлов/изображений через Telegram, list/read/delete; Vision API
- [x] Выполнение кода — Python, Go, bash в изолированной директории с таймаутом
- [x] Статистика токенов — учёт prompt/completion по дням через `/status`
- [x] Переключение модели и провайдера через Telegram (`/model fast`, `/provider auto`)

## Инструмент pi_coder

Позволяет агенту модифицировать собственный исходный код через [pi](https://github.com/mariozechner/pi) и немедленно применять изменения без ручного вмешательства.

| Параметр | Тип | Описание |
|----------|-----|----------|
| `task` | string | Задача для pi (обязательный) |
| `rebuild` | bool | Пересобрать бинарник (`make build`) |
| `restart` | bool | Перезапустить агента после успешной сборки (включает `rebuild` автоматически) |

**Механизм безопасного перезапуска:**  
При `restart=true` агент использует `syscall.Exec` — текущий процесс атомарно заменяется новым бинарником без форка. PID остаётся прежним, редиректы stdin/stdout/stderr (nohup) сохраняются. Перезапуск происходит через 5 секунд после завершения `Execute`, чтобы Telegram успел получить ответ. Если сборка провалилась — перезапуск отменяется.

```yaml
# config.yaml
pi_coder:
  enabled: true
  project_dir: "/path/to/agent-shatrov"
  model: "glm-4.7"
  timeout: 300  # секунды
```

Пример использования в Telegram:
```
Добавь инструмент для работы с календарём
→ агент вызывает pi_coder {task: "...", restart: true}
→ pi редактирует код, make build пересобирает бинарник
→ агент отвечает «перезапускаюсь через 5 сек...»
→ через 5 сек syscall.Exec заменяет процесс — агент живёт с новым кодом
```

## Telegram команды

Команды обрабатываются без участия LLM — только системные операции. Все пользовательские функции доступны через диалог с агентом (инструменты).

| Команда | Описание |
|---------|----------|
| `/start` | Приветствие |
| `/new` | Начать новый диалог (сбросить контекст) |
| `/model` | Показать текущую модель и доступные алиасы |
| `/model fast` | Переключиться на быструю модель |
| `/model default` | Переключиться на основную модель |
| `/provider` | Показать текущий провайдер LLM и их статус |
| `/provider <name>` | Принудительно выбрать провайдера |
| `/provider auto` | Вернуть автоматический режим (по приоритету с fallback) |
| `/context` | Статистика окна контекста (сообщений, токенов, суммаризация) |
| `/status` | Статус агента: uptime, модель, БД, токены за день, провайдеры |
| `/help` | Справка |

## Новости

Агент поддерживает агрегацию новостей из RSS/Atom/JSON Feed источников. Управление через диалог с агентом (инструмент `news`), а не через slash-команды.

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
      enabled: true
```
