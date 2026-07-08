# Stocks Market Data Tool

## Overview

Новый инструмент `stocks` для агента — получение рыночных котировок по акциям и индексам в реальном времени.

**Что реализуем:**
- Американские индексы: S&P 500, Nasdaq, Dow Jones, Russell 2000 (через Yahoo Finance)
- Российские индексы: IMOEX, RTSI (через MOEX ISS API)
- Акции по тикерам: US (AAPL, MSFT и др.) и RU (SBER, GAZP и др.)
- Данные: цена, абсолютное изменение, изменение %, объём торгов, диапазон дня

**Проблема:** Агент не умеет получать актуальные рыночные данные — нет способа ответить на «как там S&P 500?» или «сколько стоит SBER?»

**Интеграция:** Инструмент регистрируется в `cmd/agent/tools.go`, конфиг в `config.yaml`, кэш в SQLite.

## Context (from discovery)

- **Файлы-образцы:** `internal/tools/weather.go` (HTTP-клиент без ключа), `internal/tools/web_search.go` (с ключом), `internal/storage/stats.go` (новая таблица + CRUD)
- **Паттерн конфига:** `WeatherConfig` → поле в `Config` → `registerWeatherTool` в `tools.go`
- **Паттерн инструмента:** `struct` + `NewXxxTool(cfg, db)` + реализация интерфейса `Tool`
- **SQLite миграции:** добавляются в `migrate()` в `internal/storage/sqlite.go`
- **Прокси:** `http.ProxyFromEnvironment` (глобальный `HTTPS_PROXY`)
- **Зависимости:** нет новых внешних пакетов — только stdlib

## Development Approach

- **Тестирование:** TDD — сначала тесты, потом реализация
- Каждая задача завершается полностью перед переходом к следующей
- **Каждая задача ОБЯЗАТЕЛЬНО включает тесты** — они не опциональны
- **Все тесты должны проходить** перед началом следующей задачи
- Обратная совместимость: новый инструмент не меняет существующие

## Testing Strategy

- **Unit tests:** таблично-ориентированные тесты с `httptest.NewServer` для мока API
- **Cache tests:** проверка TTL, hit/miss, upsert
- **Routing tests:** все ветки авто-определения источника
- **Форматирование:** тесты на корректный вывод при разных значениях (отрицательное изменение, нулевой объём, офлайн-рынок)
- E2E-тесты не применимы (нет UI)

## Progress Tracking

- Отмечать выполненные пункты `[x]` сразу по завершении
- Новые задачи добавлять с префиксом ➕
- Блокеры отмечать ⚠️

## Solution Overview

**Авто-роутинг по тикеру (Вариант A):**

```
Входной тикер
├── IMOEX / RTSI              → MOEX Index API
├── ^GSPC / ^IXIC / ^DJI / ^RUT
│   или алиасы sp500/nasdaq/dji/russell → Yahoo Finance
├── 4–5 латинских букв        → MOEX Stocks (TQBR), при 404 → Yahoo Finance  
└── всё остальное             → Yahoo Finance
```

**Кэш в SQLite:** таблица `stock_quotes_cache(ticker, source, data_json, fetched_at)`, TTL = 15 мин (настраивается). При cache hit — данные из БД; при cache miss или истёкшем TTL — запрос к API + обновление кэша.

**Два API-клиента:**
- `moexClient` — MOEX ISS REST API (бесплатно, без ключа): акции на board TQBR + индексы
- `yahooClient` — Yahoo Finance v8 `/finance/quote` (неофициальный, без ключа, `User-Agent` обязателен)

**Общая структура данных `Quote`:**
```go
type Quote struct {
    Ticker    string
    ShortName string
    Price     float64
    Change    float64   // абсолютное изменение
    ChangePct float64   // изменение в %
    Volume    int64
    Open      float64
    High      float64
    Low       float64
    Source    string    // "yahoo" | "moex_stock" | "moex_index"
    Currency  string    // "USD" | "RUB"
    FetchedAt time.Time
}
```

## Technical Details

**MOEX ISS API — акции:**
```
GET https://iss.moex.com/iss/engines/stock/markets/shares/boards/TQBR/securities/{TICKER}.json
    ?iss.meta=off&iss.only=marketdata,securities
```
Поля: `LAST` (цена), `LASTTOPREVPRICE` (изм. %), `VOLTODAY` (объём в лотах), `OPEN`, `HIGH`, `LOW`, `SHORTNAME`

**MOEX ISS API — индексы:**
```
GET https://iss.moex.com/iss/engines/stock/markets/index/securities/{TICKER}.json
    ?iss.meta=off&iss.only=marketdata,securities
```
Поля: `CURRENTVALUE`, `LASTVALUE`, `CHANGE`, `OPEN`, `HIGH`, `LOW`, `SHORTNAME`

**Yahoo Finance v8:**
```
GET https://query1.finance.yahoo.com/v8/finance/quote
    ?symbols={TICKER}&fields=regularMarketPrice,regularMarketChange,
     regularMarketChangePercent,regularMarketVolume,shortName,
     regularMarketOpen,regularMarketDayHigh,regularMarketDayLow,marketState
```
Headers: `User-Agent: Mozilla/5.0`

**SQLite cache:**
```sql
CREATE TABLE IF NOT EXISTS stock_quotes_cache (
    ticker     TEXT NOT NULL,
    source     TEXT NOT NULL,
    data_json  TEXT NOT NULL,
    fetched_at DATETIME NOT NULL,
    PRIMARY KEY (ticker, source)
)
```

**Конфигурация:**
```yaml
stocks:
  enabled: true
  cache_ttl_minutes: 15   # 0 = без кэша
  timeout_seconds: 15
```

**Алиасы индексов (нечувствительно к регистру):**
| Вход | Тикер Yahoo |
|------|-------------|
| sp500, s&p500, s&p 500 | ^GSPC |
| nasdaq | ^IXIC |
| dji, dow jones, dow | ^DJI |
| russell, russell2000 | ^RUT |

## What Goes Where

**Implementation Steps** — всё, что реализуется в этом репозитории с `[ ]` чекбоксами.

**Post-Completion** — ручная проверка котировок на актуальность.

## Implementation Steps

### Task 1: SQLite cache — таблица и методы хранения

**Files:**
- Create: `internal/storage/stocks.go`
- Create: `internal/storage/stocks_test.go`
- Modify: `internal/storage/sqlite.go` (добавить миграцию)

- [x] написать тест `TestSaveAndGetQuoteCache_Hit` — сохранить котировку, получить обратно, данные совпадают
- [x] написать тест `TestGetQuoteCache_Miss` — несуществующий тикер возвращает `nil, nil`
- [x] написать тест `TestGetQuoteCache_Expired` — TTL истёк, возвращает `nil, nil`
- [x] написать тест `TestSaveQuoteCache_Upsert` — повторное сохранение обновляет запись
- [x] добавить миграцию `stock_quotes_cache` в `sqlite.go`
- [x] реализовать `SaveQuoteCache(ctx, ticker, source, dataJSON)` в `stocks.go`
- [x] реализовать `GetQuoteCache(ctx, ticker, source, ttl) (string, error)` — возвращает JSON или `""`
- [x] запустить тесты — должны пройти перед Task 2

### Task 2: MOEX API клиент

**Files:**
- Create: `internal/tools/stocks_moex.go`
- Create: `internal/tools/stocks_moex_test.go`

- [x] написать тест `TestFetchMOEXStock_Success` — mock HTTP, корректный JSON, проверить `Quote` поля
- [x] написать тест `TestFetchMOEXStock_NotFound` — mock HTTP 200 с пустыми данными → ошибка
- [x] написать тест `TestFetchMOEXIndex_Success` — mock HTTP, IMOEX/RTSI, проверить `Quote` поля
- [x] написать тест `TestFetchMOEXIndex_HTTPError` — mock HTTP 500 → ошибка
- [x] реализовать `moexClient` struct с методами `FetchStock(ctx, ticker)` и `FetchIndex(ctx, ticker)`
- [x] парсинг MOEX ISS JSON: `columns` + `data` → `Quote` (MOEX возвращает columnar format)
- [x] установить `Source: "moex_stock"` / `"moex_index"`, `Currency: "RUB"`
- [x] запустить тесты — должны пройти перед Task 3

### Task 3: Yahoo Finance API клиент

**Files:**
- Create: `internal/tools/stocks_yahoo.go`
- Create: `internal/tools/stocks_yahoo_test.go`

- [x] написать тест `TestFetchYahooQuote_Stock_Success` — mock HTTP, AAPL, проверить `Quote` поля
- [x] написать тест `TestFetchYahooQuote_Index_Success` — mock HTTP, ^GSPC
- [x] написать тест `TestFetchYahooQuote_NotFound` — пустой `quoteResponse.result` → ошибка
- [x] написать тест `TestFetchYahooQuote_HTTPError` — mock HTTP 403 → ошибка
- [x] написать тест `TestResolveYahooAlias` — таблица: `sp500`→`^GSPC`, `nasdaq`→`^IXIC` и др.
- [x] реализовать `yahooClient` struct с методом `FetchQuote(ctx, ticker)`
- [x] реализовать `resolveAlias(ticker) string` — нормализация алиасов к Yahoo-тикеру
- [x] парсинг Yahoo v8 JSON → `Quote`, `Source: "yahoo"`, определить `Currency` из `currency` поля
- [x] запустить тесты — должны пройти перед Task 4

### Task 4: StocksTool — роутинг, кэш, форматирование

**Files:**
- Create: `internal/tools/stocks.go`
- Create: `internal/tools/stocks_test.go`

- [x] написать тест `TestRouteToSource` (table-driven): IMOEX→moex_index, SBER→moex_stock, AAPL→yahoo, sp500→yahoo, ^GSPC→yahoo
- [x] написать тест `TestStocksTool_CacheHit` — при cache hit не делать HTTP-запрос
- [x] написать тест `TestStocksTool_CacheMiss_FetchAndStore` — при cache miss — запрос + сохранение
- [x] написать тест `TestStocksTool_MOEXFallbackToYahoo` — MOEX вернул «не найдено» → Yahoo
- [x] написать тест `TestFormatQuote_Positive` — рост: зелёная стрелка, знак `+`
- [x] написать тест `TestFormatQuote_Negative` — падение: красная стрелка
- [x] написать тест `TestFormatQuote_ZeroVolume` — нулевой объём отображается как «н/д»
- [x] реализовать `StocksTool` struct + конструктор `NewStocksTool(cfg StocksConfig, db *storage.DB, logger)`
- [x] реализовать `routeToSource(ticker) (source, resolvedTicker)` — авто-роутинг
- [x] реализовать `fetchQuote(ctx, ticker)` — check cache → fetch API → save cache → return Quote
- [x] реализовать `formatQuote(q Quote) string` — красивый вывод: эмодзи, цена, %, объём, диапазон
- [x] реализовать `Execute(ctx, args)` — принять `tickers []string`, вернуть multi-quote результат
- [x] реализовать `Name`, `Description`, `Parameters` (JSON Schema)
- [x] запустить тесты — должны пройти перед Task 5

### Task 5: Конфигурация и регистрация инструмента

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/agent/tools.go`
- Modify: `config.example.yaml`

- [x] добавить `StocksConfig` в `config.go` (`Enabled`, `CacheTTLMinutes`, `TimeoutSeconds`)
- [x] добавить поле `Stocks StocksConfig \`yaml:"stocks"\`` в `Config`
- [x] добавить `registerStocksTool` функцию в `tools.go` и вызов из `registerOptionalTools`
- [x] добавить секцию `stocks:` в `config.example.yaml` с комментариями
- [x] запустить `make build` — должен собираться без ошибок

### Task 6: Проверка критериев приёмки

**Files:**
- None (верификация)

- [x] проверить: `{"tickers": ["IMOEX"]}` → MOEX index, цена, изм.%, объём
- [x] проверить: `{"tickers": ["SBER"]}` → MOEX stock, цена в RUB
- [x] проверить: `{"tickers": ["AAPL"]}` → Yahoo, цена в USD
- [x] проверить: `{"tickers": ["sp500"]}` → Yahoo ^GSPC через алиас
- [x] проверить: `{"tickers": ["SBER", "AAPL", "IMOEX"]}` → смешанный запрос, все три результата
- [x] проверить: повторный вызов в течение 15 мин — данные из кэша (лог «cache hit»)
- [x] запустить полный тест-сьют: `make lint test`

### Task 7: [Final] Обновление документации

**Files:**
- Modify: `docs/backlog.md`
- Modify: `CLAUDE.md`
- Move: `docs/plans/20260418-stocks-market-data-tool.md` → `docs/plans/completed/`

- [ ] отметить задачу в `docs/backlog.md` как ✅ Done с описанием реализованного
- [ ] добавить `stocks` в список инструментов в `CLAUDE.md`
- [ ] создать `docs/plans/completed/` и переместить этот файл

## Post-Completion

**Ручная проверка:**
- Убедиться, что котировки актуальны в торговые часы
- Проверить поведение при закрытом рынке (MOEX после 18:50 МСК, NYSE после 16:00 EST) — цена должна быть последней известной с пометкой
- Проверить неизвестный тикер: ожидается понятное сообщение об ошибке, а не паника
- Yahoo Finance: если сменится неофициальное API — обновить endpoint (возможно потребует патча)
