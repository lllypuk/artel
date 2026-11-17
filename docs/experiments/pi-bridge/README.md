# Изолированный эксперимент Pi bridge

Это тестовый стенд, **не продуктовый bridge** и не глобальное расширение Pi.
Сценарии выполняются через SDK Pi 1.5.2 с extension bindings в режиме `json`.
Интерактивный TUI/agterm, API Artel и реальные LLM не используются.

`probe.mjs` поднимает loopback OpenAI-compatible fixture на случайном порту,
создаёт временные сессии без инструментов и проектных ресурсов, затем проверяет
инъекцию, события, историю и аварийные границы. SIGKILL отправляется только трём
собственным дочерним процессам по объектам `ChildProcess`.

## Запуск

Нужны Node 22.23.2 (проверенная версия) и установленный Pi **1.5.2**. Указать путь
к пакету и Node своей установки; не устанавливать/обновлять глобальный Pi ради теста.
Запускать из корня Artel. Ни конфиги, ни credentials текущего пользователя не копировать.

```bash
PACKAGE=/path/to/node_modules/@earendil-works/pi-coding-agent
NODE=/absolute/path/to/node
PROBE="$PWD/docs/experiments/pi-bridge/probe.mjs"
umask 077
ROOT=$(mktemp -d /tmp/artel-pi-bridge-XXXXXXXX)
mkdir -p "$ROOT/home" "$ROOT/config" "$ROOT/cache" "$ROOT/state"
env -i \
  HOME="$ROOT/home" \
  XDG_CONFIG_HOME="$ROOT/config" \
  XDG_CACHE_HOME="$ROOT/cache" \
  XDG_STATE_HOME="$ROOT/state" \
  PI_CODING_AGENT_DIR="$ROOT/agent-global" \
  PATH="$(dirname "$NODE"):/usr/bin:/bin" \
  PROBE_ROOT="$ROOT" PI_TEST_PACKAGE="$PACKAGE" \
  "$NODE" "$PROBE"
```

Все runtime-файлы остаются в напечатанном временном каталоге, а не в проекте или
`~/.pi`. Данные фиктивные, API key — литерал `fake-not-a-secret`. Настоящие agent/mailbox
переменные и ключи провайдеров не наследуются благодаря `env -i`. Это изоляция ресурсов
и конфигурации, не OS sandbox от произвольного кода с правами текущего UID.

Результат: `result.json`, `trace-main.json`, `trace-reopen.json`, `provider-requests.json`
и session JSONL. Отсутствующий session-файл в двух crash-сценариях — проверяемый результат.
Строка PASS означает, что подтверждено ожидаемое поведение, в том числе потеря данных;
она **не означает готовность продуктового протокола**.

[Отчёт и ограничения](../../architecture/20261117-artel-v2-pi-experiment-01.md).
`result.json` рядом с этим README — сохранённый результат финального прогона,
не результат текущего запуска.
