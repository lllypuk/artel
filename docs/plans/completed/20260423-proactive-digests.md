# Proactive Digests: финансы утром + новости и финансы вечером

## Overview

Расширить проактивный планировщик: добавить финансовую сводку в утренний брифинг и два вечерних
дайджеста (новости + финансы). Также добавить явный `Proxy` в `StocksConfig` для единообразия с
другими инструментами.

**Что изменится:**

- Утренний брифинг (существующий): погода + **котировки основных индексов** — одно сообщение
- Вечер: к существующему чек-ину добавляются два отдельных больших сообщения:
    - **Дайджест новостей** из всех подписанных фидов
    - **Финансовая сводка** по основным индексам RU + US

**Что решает:** автоматическая информационная витрина утром и вечером без ручных запросов.

**Интеграция:** планировщик уже умеет вызывать агента с промптом → агент вызывает инструменты → отправляет сообщение.

## Context (from discovery)

- **Основные файлы:**
    - `cmd/agent/proactive.go` — планировщик, `fireMorningBriefing`, `fireEveningCheckin`, `tick()`
    - `cmd/agent/main.go` — запуск `runProactiveScheduler`, передача `cfg.Weather.Enabled`
    - `internal/config/config.go` — `ProactiveConfig`, `StocksConfig`
    - `internal/tools/stocks.go` — `newStocksTool`, использует `http.ProxyFromEnvironment`
    - `cmd/agent/tools.go` — `registerStocksTool`
    - `config.example.yaml` — пример конфига
- **Паттерны:**
    - `weatherEnabled bool` — флаг пробрасывается в планировщик из `main.go`
    - `maybeFireEvent(ctx, key, time, now, handler)` — идиома регистрации события
    - `dispatch(ctx, chatID, prompt)` — отправка через агента; каждый вызов = одно сообщение
- **Прокси Yahoo:** `http.ProxyFromEnvironment` — HTTPS_PROXY из env уже работает; нет явного поля `Proxy` в конфиге (в
  отличие от `WeatherConfig`)
- **Лимит:** `MaxMessagesPerDay` default=7. После изменений: утро(1) + вечер(3) + goal_reminder(1) = 5/день, норм.

## Development Approach

- **Testing approach**: Regular (код → тесты)
- Завершать каждую задачу полностью перед переходом к следующей
- **Каждая задача должна включать тесты** для изменённого кода
- Все тесты должны проходить перед переходом к следующей задаче
- Запускать `make lint test` после каждого изменения

## Testing Strategy

- **Unit tests**: для всех изменённых функций планировщика (промпты, флаги)
- **Proxy test**: `newStocksTool` с явным прокси — проверить что transport использует его
- Существующие тесты планировщика расширяются, не заменяются

## Progress Tracking

- `[x]` — выполнено
- `➕` — добавлено в процессе
- `⚠️` — блокер/проблема

## Solution Overview

1. `StocksConfig` + `newStocksTool` получают явный `Proxy` field (как WeatherConfig)
2. `ProactiveConfig` получает `FinancialTickers []string` — список тикеров для сводок (default: IMOEX, RTSI, sp500,
   nasdaq)
3. `proactiveScheduler` получает поля `stocksEnabled bool`, `newsEnabled bool`
4. `fireMorningBriefing` модифицируется: если `stocksEnabled` — добавляет в промпт запрос котировок
5. Новые функции: `fireEveningNews`, `fireEveningFinance` — каждая отправляет отдельное сообщение
6. `tick()` вызывает оба новых события при `EveningCheckinTime` с ключами `evening_news`, `evening_finance`
7. `main.go` передаёт `cfg.News.Enabled`, `cfg.Stocks.Enabled` в `runProactiveScheduler`

## Technical Details

**Промпты:**

Утренний брифинг (с финансами):

```
... + 3) Краткая сводка рынков: {tickers} (используй инструмент stocks) + 4) Мотивация
```

Вечерний дайджест новостей:

```
🔔 [АВТО: ВЕЧЕРНИЕ НОВОСТИ] Составь подробный дайджест новостей за сегодня.
Используй инструмент news (action=list) для получения свежих новостей.
Для каждой новости: заголовок, источник, краткое содержание 2-3 предложения.
Группируй по тематике. Подробно, без ограничений по объёму.
```

Вечерняя финансовая сводка:

```
🔔 [АВТО: ВЕЧЕРНЯЯ СВОДКА РЫНКОВ] Составь подробную финансовую сводку дня.
Используй инструмент stocks для котировок: {tickers}.
Для каждого: цена, изменение % и в пунктах, объём.
Краткий комментарий о динамике рынков. Подробно.
```

**Изменения сигнатур:**

```go
// config.go
type StocksConfig struct {
Enabled         bool   `yaml:"enabled"`
CacheTTLMinutes int    `yaml:"cache_ttl_minutes"`
TimeoutSeconds  int    `yaml:"timeout_seconds"`
Proxy           string `yaml:"proxy"` // новое
}

type ProactiveConfig struct {
// ... существующие поля ...
FinancialTickers []string `yaml:"financial_tickers"` // новое: ["IMOEX","RTSI","sp500","nasdaq"]
}

// proactive.go
type proactiveScheduler struct {
// ... существующие поля ...
stocksEnabled bool // новое
newsEnabled   bool // новое
}

func runProactiveScheduler(..., newsEnabled bool, stocksEnabled bool, ...) // расширенная сигнатура
```

## What Goes Where

**Implementation Steps** — всё что делается в этом репо.
**Post-Completion** — ручная проверка и конфигурация.

## Implementation Steps

### Task 1: Добавить Proxy в StocksConfig и newStocksTool

**Files:**

- Modify: `internal/config/config.go`
- Modify: `internal/tools/stocks.go`
- Modify: `cmd/agent/tools.go`

- [ ] добавить поле `Proxy string \`yaml:"proxy"\`` в `StocksConfig` в `config.go`
- [ ] в `newStocksTool` — если `cfg.Proxy != ""` использовать явный `url.Parse` + `http.ProxyURL`, иначе
  `http.ProxyFromEnvironment` (аналогично `NewWeatherTool`)
- [ ] добавить `import "net/url"` в `stocks.go` (если не импортирован)
- [ ] в `registerStocksTool` (`cmd/agent/tools.go`) передавать `Proxy: cfg.Stocks.Proxy` в `StocksConfig`
- [ ] написать тест `TestNewStocksTool_Proxy` (явный прокси vs ProxyFromEnvironment) в `internal/tools/stocks_test.go`
- [ ] запустить `make lint test` — должно пройти

### Task 2: Добавить FinancialTickers в ProactiveConfig

**Files:**

- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] добавить `FinancialTickers []string \`yaml:"financial_tickers"\`` в `ProactiveConfig`
- [ ] в `config.example.yaml` добавить в секцию `proactive:` поле
  `financial_tickers: ["IMOEX", "RTSI", "sp500", "nasdaq"]` с комментарием
- [ ] написать тест на загрузку конфига с новым полем в `internal/config/config_test.go` (если файл существует) или
  проверить через существующие тесты
- [ ] запустить `make lint test` — должно пройти

### Task 3: Добавить stocksEnabled/newsEnabled в планировщик + обновить main.go

**Files:**

- Modify: `cmd/agent/proactive.go`
- Modify: `cmd/agent/main.go`

- [ ] добавить поля `stocksEnabled bool`, `newsEnabled bool` в структуру `proactiveScheduler`
- [ ] расширить сигнатуры `runProactiveScheduler` и `newProactiveScheduler` двумя новыми bool параметрами (после
  `weatherEnabled`)
- [ ] в `newProactiveScheduler` — присваивать `s.stocksEnabled = stocksEnabled`, `s.newsEnabled = newsEnabled`
- [ ] в `main.go` — передать `cfg.News.Enabled` и `cfg.Stocks.Enabled` в `runProactiveScheduler`
- [ ] обновить все тесты планировщика: добавить параметры `false, false` в вызовы `newProactiveScheduler`
- [ ] запустить `make lint test` — должно пройти

### Task 4: Расширить fireMorningBriefing — финансы утром

**Files:**

- Modify: `cmd/agent/proactive.go`

- [ ] добавить helper `defaultFinancialTickers(cfg ProactiveConfig) []string` — возвращает `cfg.FinancialTickers` если
  не пусто, иначе `["IMOEX","RTSI","sp500","nasdaq"]`
- [ ] в `fireMorningBriefing` — добавить ветку `s.stocksEnabled`: если включён, добавить в промпт пункт "Краткая сводка
  рынков: {tickers} (используй инструмент stocks)"
- [ ] тикеры форматируются через `strings.Join(defaultFinancialTickers(s.cfg), ", ")`
- [ ] сохранить все существующие кейсы промпта (с/без weather, с/без city) — добавлять finance как дополнительный пункт
  во всех случаях
- [ ] написать тесты для новых кейсов промпта в `cmd/agent/proactive_test.go`:
    - тест: `stocksEnabled=false` → промпт не содержит "stocks"
    - тест: `stocksEnabled=true` → промпт содержит "stocks" и тикеры
    - тест: `defaultFinancialTickers` с пустым slice → возвращает дефолты
- [ ] запустить `make lint test` — должно пройти

### Task 5: Добавить fireEveningNews

**Files:**

- Modify: `cmd/agent/proactive.go`

- [ ] реализовать `fireEveningNews(ctx context.Context)`:
    - если `!s.newsEnabled` — return сразу (no-op)
    - промпт с `action=list`, просит агента составить подробный дайджест всех свежих новостей
    - вызывает `s.dispatch(ctx, chatID, prompt)` для каждого пользователя
- [ ] в `tick()` — добавить вызов
  `s.maybeFireEvent(ctx, "evening_news", s.cfg.EveningCheckinTime, now, s.fireEveningNews)` (только если
  `s.newsEnabled && s.cfg.EveningCheckinTime != ""`)
- [ ] написать тесты:
    - тест: `newsEnabled=false` → `fireEveningNews` не вызывает dispatch (mock/counter)
    - тест: `newsEnabled=true` → dispatch вызывается с промптом содержащим "news"
- [ ] запустить `make lint test` — должно пройти

### Task 6: Добавить fireEveningFinance

**Files:**

- Modify: `cmd/agent/proactive.go`

- [ ] реализовать `fireEveningFinance(ctx context.Context)`:
    - если `!s.stocksEnabled` — return сразу (no-op)
    - промпт запрашивает котировки `defaultFinancialTickers(s.cfg)` через инструмент stocks
    - вызывает `s.dispatch(ctx, chatID, prompt)` для каждого пользователя
- [ ] в `tick()` — добавить
  `s.maybeFireEvent(ctx, "evening_finance", s.cfg.EveningCheckinTime, now, s.fireEveningFinance)` (только если
  `s.stocksEnabled && s.cfg.EveningCheckinTime != ""`)
- [ ] написать тесты:
    - тест: `stocksEnabled=false` → `fireEveningFinance` не вызывает dispatch
    - тест: `stocksEnabled=true` → dispatch вызывается с промптом содержащим тикеры
    - тест: `tick()` с `stocksEnabled=true, newsEnabled=true` — проверить что все 3 вечерних события регистрируются (
      evening_checkin, evening_news, evening_finance)
- [ ] запустить `make lint test` — должно пройти

### Task 7: Проверка критериев приёмки

- [ ] убедиться что `fireMorningBriefing` во всех 3 кейсах (city+weather, city only, default) добавляет финансовый блок
  при `stocksEnabled=true`
- [ ] убедиться что `fireEveningNews` и `fireEveningFinance` работают независимо (можно включить одно без другого)
- [ ] убедиться что `MaxMessagesPerDay=7` (default) покрывает: morning(1) + evening_checkin(1) + evening_news(1) +
  evening_finance(1) + goal_reminder(1) = 5 ≤ 7 ✓
- [ ] запустить финальный `make lint test` — все тесты проходят

### Task 8: [Final] Обновить документацию

**Files:**

- Modify: `CLAUDE.md`
- Move: `docs/plans/20260423-proactive-digests.md` → `docs/plans/completed/`

- [ ] обновить раздел "Реализованные функции" в `CLAUDE.md` — добавить пункт про финансовую сводку утром и дайджесты
  вечером
- [ ] обновить описание `ProactiveConfig` в CLAUDE.md (добавить `financial_tickers`)
- [ ] переместить этот план в `docs/plans/completed/`

## Post-Completion

**Ручная проверка:**

- Включить `stocks.enabled: true` и `news.enabled: true` в `config.yaml`
- Добавить `financial_tickers` в секцию `proactive:` (или оставить дефолт)
- Временно поставить `morning_briefing` и `evening_checkin` на текущее время +2 мин и проверить что приходят все
  сообщения
- Проверить что Yahoo Finance запросы идут через прокси (можно через `HTTPS_PROXY` лог или отключив прокси)

**Опционально:**

- При большом объёме новостей можно ограничить `limit` в промпте (например, последние 20 новостей)
- Рассмотреть добавление отдельного поля `EveningNewsTime` / `EveningFinanceTime` если нужны разные времена
