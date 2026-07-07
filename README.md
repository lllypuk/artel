# agent-shatrov

Персональный AI-агент с Telegram/Mattermost-интерфейсами для личного использования на локальном компьютере.
Может работать как одиночный Telegram-бот или как набор автономных ролей (аналитик, разработчик,
тестировщик) в общем Mattermost.

## Docker

Требования: установленный Docker и плагин `docker compose`.

Быстрый старт:

```bash
cp config.example.yaml config.yaml
# заполнить telegram.token, telegram.allowed_users, llm.api_key

docker compose up -d
docker compose logs -f
```

- `config.yaml` монтируется в контейнер, поэтому правки применяются после перезапуска `docker compose`.
- Данные агента сохраняются в volume `agent_data`.
- Для переопределения значений в Docker можно использовать env-переменные: `TELEGRAM_TOKEN`,
  `TELEGRAM_ALLOWED_USERS`, `LLM_API_KEY`/`GLM_API_KEY`, `LLM_BASE_URL`, `WEB_API_KEY`, `LOG_LEVEL`.
- В Docker из коробки работают инструменты, не требующие внешних бинарников. `browser` выключен по умолчанию и
  требует отдельной установки `agent-browser`; `shell` выключен по умолчанию; `pi_coder` требует отдельно установленный `pi`.
- Если используешь прокси на хосте, например `http://127.0.0.1:2080`, в `config.yaml` для Docker замени адрес на
  `http://host-gateway:2080`, иначе контейнер не сможет достучаться до loopback-интерфейса хоста.

## Стек

- **Go 1.26**
- **LLM**: GLM-4.7 / GLM-4.7-Flash (z.ai, OpenAI-compatible API)
- **Интерфейс**: Telegram Bot (long polling) · Mattermost Bot (REST + WebSocket)
- **Хранилище**: SQLite
- **Безопасность**: whitelist/sandbox для shell-команд · предохранители от петель для multi-agent
  диалогов

## Быстрый старт

```bash
git clone https://github.com/lllypuk/agent-shatrov.git
cd agent-shatrov

cp config.example.yaml config.yaml
# заполнить telegram.token + telegram.allowed_users (или mattermost + role)
# и llm.api_key

make run
# для второго инстанса в Mattermost см. docs/multi-instance-guide.md
```

## Команды

```bash
make run                    # собрать и запустить с config.yaml (по умолчанию)
make run CONFIG=config-analyst.yaml # запустить с другим конфигом (multi-instance)
./bin/agent config-analyst.yaml  # запустить указанной конфигурацией (multi-instance)
make stop                   # остановить
make restart                # перезапустить
make logs                   # смотреть логи в реальном времени
make build                  # только сборка
make lint                   # статический анализ (golangci-lint)
make test                   # запуск тестов
make clean                  # удалить бинарник
```

> **CLI:** первый позиционный аргумент `bin/agent` — путь к `config.yaml`. По умолчанию используется `config.yaml`. Для multi-instance в Mattermost каждая роль запускается со своим конфигом (см. `docs/multi-instance-guide.md`).

## Конфигурация

Минимальный Telegram-вариант:

```yaml
transport:
  type: telegram
telegram:
  token: "..."
  allowed_users: [123456]
llm:
  api_key: "..."
```

Для Mattermost multi-agent:

```yaml
transport:
  type: mattermost
mattermost:
  server_url: "https://mm.example.com"
  token: "..."
  bot_username: "analyst-bot"
  owner_user_id: "u-..."
  team_bots: ["developer-bot", "tester-bot"]
role:
  name: analyst
  system_prompt: "Ты — аналитик команды..."
safeguards:
  loop_warn_bot_messages: 10
  loop_suppress_bot_messages: 15
  max_replies_per_minute: 5
```

Подробности и описание всех секций — в `config.example.yaml`. Полное руководство по multi-instance: `docs/multi-instance-guide.md`.

## Структура

```
cmd/agent/
  main.go            — точка входа, wiring
  tools.go           — регистрация инструментов
  news.go            — setup новостей и цикл обновления фидов
  reminders.go       — планировщик напоминаний
  monitor.go         — фоновый цикл мониторинга сайтов с Telegram-алертами
  logger.go          — настройка логгера
internal/
  config/config.go   — конфигурация (YAML + env): transport, role, safeguards
  agent/agent.go     — ядро агента (ReAct loop)
  llm/
    client.go        — клиент к LLM API (OpenAI-compatible)
    multi.go         — мультипровайдер с fallback, retry, circuit breaker
  transport/
    transport.go     — общий интерфейс Transport (заменяет прямую зависимость ядра от Telegram)
    auth.go          — gate авторизации: владелец + whitelist ботов-коллег + @-mention
  telegram/
    bot.go           — Telegram бот
    markdown.go      — Markdown → HTML конвертация
    transport.go     — реализация Transport для Telegram
  mattermost/
    client.go        — REST + WebSocket клиент Mattermost
    transport.go     — реализация Transport для Mattermost
  safeguards/
    safeguards.go    — loop-detection и rate-limit для multi-agent диалогов
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
    monitor.go       — мониторинг доступности сайтов (check/list/history)
    wiki.go          — личная вики (create/get/edit/delete/list/tree/rename/backlinks)
    reading_list.go  — список для чтения (save/add/list/get/update/delete/digest)
    knowledge_search.go — unified поиск по вики + reading list (FTS5)
  storage/
    sqlite.go        — SQLite: диалоги, сообщения, заметки, токены
    news.go          — SQLite: новости, фиды, подписки
    monitor.go       — SQLite: мониторинг сайтов (monitor_checks, monitor_state)
    wiki.go          — SQLite: wiki (wiki_pages + FTS5, wiki_links), reading_list
  news/              — парсер/менеджер RSS/Atom/JSON Feed
docs/
  multi-instance-guide.md  — как поднять несколько инстансов в Mattermost
  roles/             — шаблоны конфигов для ролей (аналитик/разработчик/тестировщик)
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
- [x] Самообновление через `pi_coder` — агент изменяет, пересобирает и **перезапускает себя** без ручного
  вмешательства (`restart=true` → `syscall.Exec`)
- [x] Агрегация новостей — подписки на RSS/Atom/JSON фиды, поиск, избранное
- [x] Автоматизация браузера (agent-browser) — навигация, клики, формы, скриншоты, JS
- [x] Прогноз погоды (Open-Meteo) — текущие условия, прогноз на 1–7 дней, геокодирование
- [x] Мультипровайдер LLM с fallback — OpenRouter, z.ai, любой OpenAI-compatible API; retry с backoff, circuit breaker
- [x] Файлы — приём файлов/изображений через Telegram, list/read/delete; Vision API
- [x] Выполнение кода — Python, Go, bash в изолированной директории с таймаутом
- [x] Статистика токенов — учёт prompt/completion по дням через `/status`
- [x] Переключение модели и провайдера через Telegram (`/model fast`, `/provider auto`)
- [x] **Docker** — многоэтапный Dockerfile (alpine, pure Go); Docker Compose с named volume; CI/CD через GitHub Actions
- [x] **Мониторинг сайтов** — фоновые HTTP-проверки, алерты в Telegram при смене состояния (🔴/✅); ручная проверка через агент
- [x] **Личная вики** — иерархия путей (`/dev/golang`), `[[path]]` ссылки, cascade rename, backlinks; create/get/edit/delete/list/tree
- [x] **Список для чтения** — сохранение URL (авто fetch), дайджест непрочитанного в утреннем брифинге
- [x] **Unified поиск** по базе знаний — одновременный поиск по вики и reading list (FTS5 + LIKE fallback)
- [x] **Транспорт-абстракция** — единый интерфейс `Transport`; Telegram и Mattermost работают через него
- [x] **Mattermost-интерфейс** — REST + WebSocket бот, @-mention/DM, для одиночного или multi-agent режима
- [x] **Multi-agent авторизация** — владелец-человек с полным доступом + whitelist ботов-коллег, реагирующих через @-mention
- [x] **Предохранители петель** — self-skip, лимит итераций на тред (warn/suppress), rate-limit ответов
- [x] **Роли инстансов** — разные `role.system_prompt` для разных «должностей» агента

## Инструмент pi_coder

Позволяет агенту модифицировать собственный исходный код через [pi](https://github.com/mariozechner/pi) и немедленно
применять изменения без ручного вмешательства.

| Параметр  | Тип    | Описание                                                                      |
|-----------|--------|-------------------------------------------------------------------------------|
| `task`    | string | Задача для pi (обязательный)                                                  |
| `rebuild` | bool   | Пересобрать бинарник (`make build`)                                           |
| `restart` | bool   | Перезапустить агента после успешной сборки (включает `rebuild` автоматически) |

**Механизм безопасного перезапуска:**  
При `restart=true` агент использует `syscall.Exec` — текущий процесс атомарно заменяется новым бинарником без форка. PID
остаётся прежним, редиректы stdin/stdout/stderr (nohup) сохраняются. Перезапуск происходит через 5 секунд после
завершения `Execute`, чтобы Telegram успел получить ответ. Если сборка провалилась — перезапуск отменяется.

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

Команды обрабатываются без участия LLM — только системные операции. Все пользовательские функции доступны через диалог с
агентом (инструменты).

| Команда            | Описание                                                      |
|--------------------|---------------------------------------------------------------|
| `/start`           | Приветствие                                                   |
| `/new`             | Начать новый диалог (сбросить контекст)                       |
| `/model`           | Показать текущую модель и доступные алиасы                    |
| `/model fast`      | Переключиться на быструю модель                               |
| `/model default`   | Переключиться на основную модель                              |
| `/provider`        | Показать текущий провайдер LLM и их статус                    |
| `/provider <name>` | Принудительно выбрать провайдера                              |
| `/provider auto`   | Вернуть автоматический режим (по приоритету с fallback)       |
| `/context`         | Статистика окна контекста (сообщений, токенов, суммаризация)  |
| `/status`          | Статус агента: uptime, модель, БД, токены за день, провайдеры |
| `/help`            | Справка                                                       |

## Новости

Агент поддерживает агрегацию новостей из RSS/Atom/JSON Feed источников. Управление через диалог с агентом (инструмент
`news`), а не через slash-команды.

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

## Мониторинг сайтов

Агент периодически проверяет доступность HTTP/HTTPS сайтов и присылает алерт в Telegram при смене статуса (🔴 упал / ✅ восстановился). Ручная проверка и просмотр истории — через инструмент `monitor` в диалоге с агентом.

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

## База знаний (wiki + reading_list)

**wiki** — личная вики с иерархией путей (как файловая система):
- Пути: `/dev/golang`, `/health/бег`, `/project/ideas`
- Синтаксис `[[path]]` для ссылок между страницами
- Cascade rename: при переименовании пути все ссылки обновляются атомарно (транзакция)
- Операции: create/get/edit/delete/list/tree/rename/backlinks

**reading_list** — список для чтения и сохранённые статьи:
- `save` — сохранить URL (автоматически fetch контент через web_fetch)
- list/get/update/delete
- `digest` — дайджест непрочитанных статей; включён в утренний брифинг

**knowledge_search** — единый поиск по вики и reading list одновременно (FTS5 + LIKE fallback).

---

## Multi-agent режим в Mattermost

Один бинарник `agent-shatrov` может подниматься одновременно в нескольких ролях
(аналитик, разработчик, тестировщик), общающихся через @-mentions в одном
канале/DM без центрального оркестратора.

- Выбор транспорта: `transport.type: mattermost` в конфиге роли.
- Роли и системные промпты задаются секцией `role:`.
- Безопасность: whitelist ботов-коллег (`mattermost.team_bots`), проверка
  @-mentions, предохранители петель (`safeguards:`).

Полное руководство и примеры конфигов: [`docs/multi-instance-guide.md`](docs/multi-instance-guide.md).
