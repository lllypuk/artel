# agent-shatrov

Персональный AI-агент с Telegram интерфейсом.

## Стек
- **Go 1.26**
- **LLM**: GLM 4.7 / GLM 4.7 Flash (Zhipu AI, OpenAI-compatible API)
- **Интерфейс**: Telegram Bot
- **Хранилище**: SQLite
- **Безопасность**: whitelist/sandbox для shell-команд

## Структура
```
cmd/agent/           — точка входа
internal/
  config/            — конфигурация (YAML + env)
  agent/             — ядро агента (ReAct loop)
  llm/               — клиент к LLM API (OpenAI-compatible)
  tools/             — инструменты (shell, notes, search, ...)
  telegram/          — Telegram бот
  conversation/      — хранение истории (SQLite)
  storage/           — SQLite абстракция
```

## Запуск
```bash
cp config.example.yaml config.yaml
# заполнить ключи в config.yaml
go run ./cmd/agent/
```

## Возможности
- [x] Telegram бот-интерфейс (long polling)
- [x] Диалог с GLM 4.7 / Flash
- [x] История диалогов (SQLite)
- [ ] Заметки (создание, поиск, теги)
- [ ] Поиск в интернете
- [ ] Агрегация новостей
- [ ] Безопасное выполнение shell-команд
- [ ] Плагинная архитектура
