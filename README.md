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
  storage/sqlite.go        — SQLite: диалоги, сообщения, заметки
  telegram/bot.go          — Telegram бот
config.example.yaml        — пример конфигурации
Makefile                   — управление сервисом
```

## Возможности

- [x] Telegram бот-интерфейс (long polling, auth по chat_id)
- [x] Диалог с GLM-4.7 / GLM-4.7-Flash
- [x] История диалогов (SQLite, персистентная)
- [x] Заметки — создание, поиск, теги
- [x] Безопасный shell (whitelist команд, blocked patterns, sandbox, таймаут)
- [x] Поиск в интернете (Brave / Serper / DuckDuckGo) + чтение страниц
- [x] Самообновление через `pi_coder` — агент может изменять и пересобирать собственный код
- [ ] Агрегация новостей (RSS)
- [ ] Кодинг-ассистент
- [ ] Системное администрирование

## Инструмент pi_coder

Позволяет агенту модифицировать собственный исходный код через [pi](https://github.com/mariozechner/pi).

Агент принимает задачу в свободной форме, запускает `pi --print --model glm-4.7` в директории проекта и возвращает результат. Опционально может сразу пересобрать бинарник.

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
→ агент вызывает pi_coder, pi редактирует код
→ агент предлагает пересобраться: rebuild=true
→ после сборки — make restart
```
