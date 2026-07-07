# E2E тестирование Mattermost multi-agent

Каталог с материалами для ручного end-to-end тестирования автономной команды
агентов `agent-shatrov` в локальном Mattermost.

## Что здесь

| Файл | Назначение |
|---|---|
| `README.md` | этот файл |
| `REPORT-2026-07-07.md` | отчёт о первом E2E-прогоне |
| `config-analyst.yaml.example` | пример конфига роли `analyst` |
| `config-developer.yaml.example` | пример конфига роли `developer` |
| `config-tester.yaml.example` | пример конфига роли `tester` |
| `e2e_test.sh` | скрипт для постинга сообщений/чтения канала |
| `e2e_loop_test.sh` | скрипт для проверки защиты от петель |

## Подготовка

1. Mattermost запущен локально (например, `~/Project/devbots/mattermost/docker-compose.yml`).
2. Созданы три бота-интеграции и получены их токены.
3. Боты добавлены в одну команду и в тестовый канал.
4. Узнан `user_id` владельца (человека-supervisor).
5. Установлен Ollama с моделью, поддерживающей tools (например, `qwen3.5:cloud`),
   или есть рабочий OpenAI-compatible LLM.

## Запуск

1. Скопируйте example-конфиги и впишите реальные токены/URL:

   ```bash
   cd tests/e2e
   cp config-analyst.yaml.example config-analyst.yaml
   cp config-developer.yaml.example config-developer.yaml
   cp config-tester.yaml.example config-tester.yaml
   # отредактируйте YAML: токены, owner_user_id, LLM, пути к БД
   ```

2. Соберите бинарник в корне проекта:

   ```bash
   cd /home/sasha/Project/agent-shatrov
   make build
   ```

3. Запустите три инстанса:

   ```bash
   ./bin/agent tests/e2e/config-analyst.yaml
   # в другом терминале
   ./bin/agent tests/e2e/config-developer.yaml
   # в третьем терминале
   ./bin/agent tests/e2e/config-tester.yaml
   ```

4. Запустите сценарии:

   ```bash
   cd tests/e2e
   # редактируйте e2e_test.sh: TOKEN, CHANNEL_ID
   ./e2e_test.sh post "@analyst-bot привет"
   ./e2e_test.sh waitread 30 15
   ```

   Для проверки защиты от петель:

   ```bash
   # редактируйте e2e_loop_test.sh: ANALYST_TOKEN, DEV_TOKEN, CHANNEL_ID
   ./e2e_loop_test.sh
   ```

## Важно

- Файлы `config-*.yaml` (без `.example`) содержат секреты и **не должны** попадать
  в git. В корневом `.gitignore` уже исключены `tests/e2e/config-*.yaml`.
- Пример конфигов использует `qwen3.5:cloud` через Ollama. При использовании
  другого провайдера обновите секцию `llm`.
