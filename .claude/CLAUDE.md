# Local CLAUDE.md — Artel

Стратегические паттерны и архитектурные решения, обнаруженные в процессе разработки.

## Структура бэклога и планирования

- Главный файл бэклога: `docs/backlog.md` — таблица эпиков со ссылками и прогрессом
- Каждый эпик → отдельный план: `docs/plans/YYYY-MM-DD-<name>.md`
- Формат плана: чеклист задач (- [ ] / - [x]), примерная схема БД, пример конфига, зависимости между эпиками
- При завершении задачи: обновить чекбокс в плане + прогресс в `backlog.md`

## Архитектура транспорта и multi-agent

- `internal/transport/transport.go` — общий интерфейс `Transport` и нормализованные
  `InboundMessage`/`Message`. Позволяет ядру агента не знать о мессенджере.
- `internal/telegram/transport.go` и `internal/mattermost/transport.go` — реализации.
- `cmd/agent/main.go` выбирает транспорт по `config.transport.type`.
- `internal/transport/auth.go` (`AuthGate`) решает, обрабатывать ли входящее сообщение:
  - `owner_user_id` — человек-супервизор (полный доступ);
  - `team_bots` — whitelist ботов-коллег, разрешённых звать через @-mention;
  - боты вне whitelist и собственные сообщения игнорируются.
- `internal/safeguards/safeguards.go` отвечает за предохранители:
  - self-skip,
  - лимит бот-сообщений в треде (warn/suppress + эскалация человеку),
  - rate-limit ответов на тред.
- `config.role` даёт инстансу идентичность/«должность» (name + system_prompt).
- Руководство по multi-instance: `docs/multi-instance-guide.md`.
- Шаблоны конфигов ролей: `docs/roles/{analyst,developer,tester}.yaml`.
- `cmd/agent/dispatcher.go` — диспетчер входящих сообщений для Mattermost: auth/safeguards,
  маппинг chat_id и вызов агента.
- `internal/storage/chatids.go` — маппинг строковых внешних chat_id (Mattermost) к
  внутренним integer id хранилища.
- Первый позиционный аргумент `bin/artel` — путь к `config.yaml`;
  так поднимаются разные роли multi-instance (`./bin/artel config-analyst.yaml`).

## Регистрация инструментов — два уровня

**Always-on** (не требуют конфига, всегда активны):
```go
// cmd/agent/main.go
toolRegistry.Register(tools.NewNotesTool(db))
toolRegistry.Register(tools.NewRemindersTool(db))
toolRegistry.Register(tools.NewDisciplineTool(db))
toolRegistry.Register(tools.NewGoalsTool(db))
```

**Опциональные** (включаются через config.yaml):
```go
// cmd/agent/tools.go — registerOptionalTools()
// web, shell, files, weather, stocks, browser, pi_coder, code_runner, news
```

Новый always-on инструмент: добавить в `main.go`. Новый опциональный: добавить в `registerOptionalTools` в `tools.go` + секцию в `config.go` + `config.example.yaml`.

## Паттерн хранилища

- **Миграции** — только в `coreMigrations []string` в `internal/storage/sqlite.go`
- **CRUD** — в отдельном файле `internal/storage/<feature>.go` (пример: `discipline.go`, `news.go`, `stats.go`, `stocks.go`)
- **Тестовый хелпер** `openTestDB` уникален по имени в каждом файле во избежание `redeclared`:
  ```go
  // discipline_test.go
  func openDisciplineTestDB(t *testing.T) *storage.DB { ... }
  ```
- Используй `ON CONFLICT(...) DO UPDATE SET` (UPSERT) для операций save/upsert
- Изоляция по `chat_id` для всех пользовательских данных — обязательна

## Изоляция связанных сущностей по chat_id

Когда сущность не имеет прямого `chat_id` (например, `key_results`), проверяй владение через JOIN с родительской таблицей:

```sql
-- UpdateKeyResult — только для KR, чья objective принадлежит chatID
UPDATE key_results
SET progress = ?, note = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND objective_id IN (SELECT id FROM objectives WHERE chat_id = ?)

-- GetKeyResult с проверкой
SELECT kr.* FROM key_results kr
JOIN objectives o ON kr.objective_id = o.id
WHERE kr.id = ? AND o.chat_id = ?
```

Правило: перед добавлением дочерней сущности (kr_add) — проверить ownership родителя через `GetObjective(ctx, chatID, id)`.

## Паттерн *int для частичных обновлений в инструментах

В args-структурах инструментов числовые опциональные поля используют `*int`:

```go
type goalsArgs struct {
    Progress *int `json:"progress"` // nil = не передано, 0 = явный ноль
}

// В обработчике: fetch-merge
current, _ := db.GetObjective(ctx, chatID, id)
progress := current.Progress
if a.Progress != nil {
    progress = *a.Progress
}
```

Для строковых полей: "непустая строка = обновить, пусто = оставить текущее" (ограничение: нельзя очистить поле в пустую строку через tool call).

## Проактивный планировщик — ключевые решения

Файл: `cmd/agent/proactive.go`

| Решение | Значение |
|---------|----------|
| `eventWindow = 3 min` | Событие срабатывает в окне [scheduled, scheduled+3min) — перехватывает пропущенные события при рестарте |
| `nowFn func() time.Time` | Инъекция времени для тестов; в production = `time.Now()` |
| `sentToday map[int64]int` | Лимит per-user (не глобальный); сбрасывается в полночь |
| Лимит в `dispatch()` | `maybeFireEvent` не трогает счётчик — лимит проверяется при отправке каждому пользователю |
| `tick()` при старте | `run()` вызывает `tick()` до первого тика таймера — ловит события в окне после рестарта |

Тесты планировщика: используй `s.nowFn = func() time.Time { return fixedTime }` и `parseScheduledTime(hhmm, now, loc)` напрямую.

## Проактивные сообщения — промпт-конвенция

Триггеры от планировщика передаются в агент с префиксом `[АВТО: ...]`:
```
"🔔 [АВТО: УТРЕННИЙ БРИФИНГ] Составь краткое утреннее сообщение: ..."
```

System prompt обучен обрабатывать `[АВТО:]` сообщения как системные триггеры и не упоминать префикс в ответе. Ответ агента доставляется пользователю через `notifier(chatID, resp.Text)`.

## Добавление конфигурации

При добавлении нового раздела конфига:
1. Добавить struct в `internal/config/config.go` + поле в `Config`
2. Добавить тесты в `internal/config/config_test.go` (парсинг + дефолты)
3. Добавить секцию в `config.example.yaml` с комментариями
