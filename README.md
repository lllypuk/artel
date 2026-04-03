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
cmd/agent/main.go          — точка входа
internal/
  config/config.go         — конфигурация (YAML + env)
  agent/agent.go           — ядро агента (ReAct loop)
  llm/client.go            — клиент к LLM API (OpenAI-compatible)
  tools/
    registry.go            — реестр инструментов
    notes.go               — инструмент: заметки
    shell.go               — инструмент: безопасный shell
    web_search.go          — инструмент: поиск в интернете
    web_fetch.go           — инструмент: чтение веб-страниц
    pi_coder.go            — инструмент: доработка агента через pi
    news.go                — инструмент: агрегация новостей
  storage/
    sqlite.go              — SQLite: диалоги, сообщения, заметки
    news.go                — SQLite: новости, фиды, подписки
  telegram/
    bot.go                 — Telegram бот
    markdown.go            — Markdown → HTML конвертация
  news/
    parser.go              — парсер фидов (RSS/Atom/JSON Feed)
    feed.go                — структура фида
    item.go                — структура новости
    subscription.go        — подписка на фид
    fetcher.go             — загрузка фидов по HTTP
    manager.go             — менеджер новостей
    filter.go              — фильтры новостей
    errors.go              — ошибки пакета news
config.example.yaml        — пример конфигурации
Makefile                   — управление сервисом
```

## Возможности

- [x] Telegram бот-интерфейс (long polling, auth по chat_id)
- [x] Диалог с GLM-4.7 / GLM-4.7-Flash
- [x] История диалогов (SQLite, персистентная)
- [x] Заметки — создание, поиск, теги, редактирование, удаление
- [x] Безопасный shell (whitelist команд, blocked patterns, sandbox, таймаут)
- [x] Поиск в интернете (Brave / Serper / DuckDuckGo) + чтение страниц
- [x] Самообновление через `pi_coder` — агент может изменять, пересобирать и **перезапускать себя** без ручного вмешательства (`restart=true` → `syscall.Exec`)
- [x] Агрегация новостей — подписки на RSS/Atom/JSON фиды, поиск, избранное
- [x] Переключение модели через Telegram (`/model fast`)

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

| Команда | Описание |
|---------|----------|
| `/start` | Приветствие |
| `/new` | Начать новый диалог (сбросить контекст) |
| `/model` | Показать текущую модель и доступные алиасы |
| `/model fast` | Переключиться на быструю модель |
| `/model default` | Переключиться на основную модель |
| `/help` | Справка |
| `/news [запрос]` | Последние новости или поиск по запросу |
| `/unread` | Непрочитанные новости |
| `/saved` | Сохранённые новости |
| `/feeds` | Список подписок на фиды |

## Новости

Агент поддерживает агрегацию новостей из RSS/Atom/JSON Feed источников.

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

**Возможности:**
- Подписка на произвольные фиды
- Автоматическое обновление
- Поиск по тексту и фильтрация
- Сохранение в избранное
- Чтение статей через агент
