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
- [x] Поиск в интернете
- [ ] Агрегация новостей (RSS)
- [ ] Возможность доработки и обновления самого агента с помощью pi
- [ ] Кодинг-ассистент
- [ ] Системное администрирование
