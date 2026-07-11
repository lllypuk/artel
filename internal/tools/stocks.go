package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lllypuk/artel/internal/storage"
)

// moexIndexTickers lists known MOEX index tickers (uppercase).
var moexIndexTickers = map[string]bool{
	"IMOEX": true,
	"RTSI":  true,
	"MOEX":  true,
	"MCFTR": true,
}

// StocksConfig holds configuration for the stocks tool.
type StocksConfig struct {
	Enabled         bool
	CacheTTLMinutes int
	TimeoutSeconds  int
	Proxy           string // optional proxy URL, e.g. "http://127.0.0.1:2080"; empty = use HTTPS_PROXY env
}

// StocksTool fetches market quotes for stocks and indices.
type StocksTool struct {
	moex   *moexClient
	stooq  *stooqClient
	db     *storage.DB
	ttl    time.Duration
	logger *slog.Logger
}

// newStocksTool creates a StocksTool with injectable API base URLs (for testing).
func newStocksTool(cfg StocksConfig, db *storage.DB, logger *slog.Logger, moexBaseURL, stooqBaseURL string) *StocksTool {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 15 * time.Second
	}

	transport := &http.Transport{}
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	} else {
		transport.Proxy = http.ProxyFromEnvironment
	}
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	ttl := time.Duration(cfg.CacheTTLMinutes) * time.Minute

	return &StocksTool{
		moex:   newMOEXClient(httpClient, moexBaseURL),
		stooq:  newStooqClient(httpClient, stooqBaseURL),
		db:     db,
		ttl:    ttl,
		logger: logger,
	}
}

// NewStocksTool creates a StocksTool with production API endpoints.
func NewStocksTool(cfg StocksConfig, db *storage.DB, logger *slog.Logger) *StocksTool {
	return newStocksTool(cfg, db, logger,
		"https://iss.moex.com",
		"https://stooq.com",
	)
}

// Name returns the tool name.
func (t *StocksTool) Name() string { return "stocks" }

// Description returns the tool description for the LLM.
func (t *StocksTool) Description() string {
	return `Получить рыночные котировки акций и индексов в реальном времени.
Поддерживаемые инструменты:
- Американские индексы: sp500, nasdaq, dji (dow jones), russell2000 или ^GSPC, ^IXIC, ^DJI, ^RUT
- Российские индексы: IMOEX, RTSI
- Акции: AAPL, MSFT, TSLA (US), SBER, GAZP, LKOH (RU) и любые другие по тикеру
Данные: цена, изменение %, объём, диапазон дня (high/low).

Если тикер неизвестен — сначала найди его через web_search.
Примеры запросов: "Apple stock ticker symbol", "Лукойл тикер MOEX", "Nvidia ticker NASDAQ".`
}

// Parameters returns the JSON Schema for the tool arguments.
func (t *StocksTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tickers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Список тикеров или названий индексов, например: [\"AAPL\", \"SBER\", \"sp500\", \"IMOEX\"]",
				"minItems":    1,
			},
		},
		"required": []string{"tickers"},
	}
}

type stocksArgs struct {
	Tickers []string `json:"tickers"`
}

// Execute fetches quotes for the given tickers and returns a formatted string.
func (t *StocksTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var a stocksArgs
	if err := json.Unmarshal(args, &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}
	if len(a.Tickers) == 0 {
		return "Укажите хотя бы один тикер.", nil
	}

	var results []string
	for _, ticker := range a.Tickers {
		ticker = strings.TrimSpace(ticker)
		if ticker == "" {
			continue
		}

		q, err := t.fetchQuote(ctx, ticker)
		if err != nil {
			if errors.Is(err, errNotFound) {
				results = append(results, fmt.Sprintf("❓ **%s** — не найден", strings.ToUpper(ticker)))
			} else {
				t.logger.Warn("stocks fetch error", "ticker", ticker, "err", err)
				results = append(results, fmt.Sprintf("⚠️ **%s** — ошибка получения данных: %v", strings.ToUpper(ticker), err))
			}
			continue
		}
		results = append(results, formatQuote(q))
	}

	return strings.Join(results, "\n\n"), nil
}

// routeToSource determines the data source and canonical ticker for the given input.
// Returns (source, resolvedTicker).
func (t *StocksTool) routeToSource(ticker string) (source, resolved string) {
	upper := strings.ToUpper(ticker)

	// 1. Known MOEX indices.
	if moexIndexTickers[upper] {
		return "moex_index", upper
	}

	// 2. Stooq alias or explicit ^-prefixed ticker.
	yahooResolved := resolveYahooAlias(ticker)
	if strings.HasPrefix(yahooResolved, "^") || yahooResolved != ticker {
		return "stooq", yahooResolved
	}

	// 3. Tickers with dots or dashes are always Stooq (e.g. BRK.A, BTC-USD).
	if strings.ContainsAny(ticker, ".-") {
		return "stooq", ticker
	}

	// 4. Latin-letter-only tickers (2–5 chars): try MOEX first, fallback at fetch time.
	if isLatinOnly(upper) && len(upper) >= 2 && len(upper) <= 5 {
		return "moex_stock", upper
	}

	// 5. Default: Stooq.
	return "stooq", ticker
}

// fetchQuote returns a Quote, using cache when available.
func (t *StocksTool) fetchQuote(ctx context.Context, ticker string) (*Quote, error) {
	source, resolved := t.routeToSource(ticker)

	// Check cache (skip if TTL=0 or no DB).
	if t.db != nil && t.ttl > 0 {
		cached, err := t.db.GetQuoteCache(ctx, resolved, source, t.ttl)
		if err != nil {
			t.logger.Warn("cache read error", "ticker", resolved, "err", err)
		} else if cached != "" {
			var q Quote
			if jsonErr := json.Unmarshal([]byte(cached), &q); jsonErr == nil {
				t.logger.Debug("stocks cache hit", "ticker", resolved, "source", source)
				return &q, nil
			}
		}
	}

	// Fetch from API.
	q, err := t.fetchFromSource(ctx, source, resolved, ticker)
	if err != nil {
		return nil, err
	}

	// Store in cache.
	if t.db != nil && t.ttl > 0 {
		if data, jsonErr := json.Marshal(q); jsonErr == nil {
			if saveErr := t.db.SaveQuoteCache(ctx, q.Ticker, q.Source, string(data)); saveErr != nil {
				t.logger.Warn("cache save error", "ticker", q.Ticker, "err", saveErr)
			}
		}
	}

	return q, nil
}

// fetchFromSource fetches a Quote from the appropriate API, with MOEX→Yahoo fallback.
func (t *StocksTool) fetchFromSource(ctx context.Context, source, resolved, original string) (*Quote, error) {
	switch source {
	case "moex_index":
		return t.moex.FetchIndex(ctx, resolved)

	case "moex_stock":
		q, err := t.moex.FetchStock(ctx, resolved)
		if err == nil {
			return q, nil
		}
		if !errors.Is(err, errNotFound) {
			return nil, err
		}
		// MOEX not found → fallback to Stooq.
		t.logger.Debug("MOEX not found, falling back to Stooq", "ticker", resolved)
		return t.stooq.FetchQuote(ctx, original)

	default: // "stooq"
		return t.stooq.FetchQuote(ctx, resolved)
	}
}

// formatQuote returns a human-readable string for a Quote.
func formatQuote(q *Quote) string {
	arrow := "▲"
	sign := "+"
	if q.ChangePct < 0 {
		arrow = "▼"
		sign = ""
	} else if q.ChangePct == 0 {
		arrow = "●"
		sign = ""
	}

	name := q.ShortName
	if name == "" {
		name = q.Ticker
	}

	priceStr := formatPrice(q.Price, q.Currency)
	changeStr := fmt.Sprintf("%s%.2f (%.2f%%)", sign, q.Change, q.ChangePct)

	volStr := "н/д"
	if q.Volume > 0 {
		volStr = formatVolume(q.Volume)
	}

	rangeStr := ""
	if q.High > 0 || q.Low > 0 {
		rangeStr = fmt.Sprintf("\n  📊 День: %s — %s",
			formatPrice(q.Low, q.Currency),
			formatPrice(q.High, q.Currency),
		)
	}

	freshness := ""
	if !q.FetchedAt.IsZero() && time.Since(q.FetchedAt) > time.Minute {
		freshness = fmt.Sprintf(" (данные: %s)", q.FetchedAt.Format("15:04"))
	}

	return fmt.Sprintf("%s **%s** (%s)\n  💰 %s  %s %s\n  📦 Объём: %s%s%s",
		arrow, q.Ticker, name,
		priceStr, arrow, changeStr,
		volStr, rangeStr, freshness,
	)
}

// formatPrice formats a price value with currency symbol.
func formatPrice(price float64, currency string) string {
	sym := currencySymbol(currency)
	if price == math.Trunc(price) {
		return fmt.Sprintf("%s%.0f", sym, price)
	}
	if price >= 100 {
		return fmt.Sprintf("%s%.2f", sym, price)
	}
	return fmt.Sprintf("%s%.4f", sym, price)
}

func currencySymbol(currency string) string {
	switch strings.ToUpper(currency) {
	case "USD":
		return "$"
	case "RUB", "RUR":
		return "₽"
	case "EUR":
		return "€"
	default:
		if currency != "" {
			return currency + " "
		}
		return ""
	}
}

// formatVolume returns a human-readable volume string (e.g. "52.0M", "1.2B").
func formatVolume(v int64) string {
	switch {
	case v >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(v)/1_000_000_000)
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000)
	case v >= 1_000:
		return fmt.Sprintf("%.1fK", float64(v)/1_000)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// isLatinOnly returns true if s contains only ASCII letters.
func isLatinOnly(s string) bool {
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}
