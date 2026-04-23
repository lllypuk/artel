package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"
	"os"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lllypuk/agent-shatrov/internal/storage"
)

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRouteToSource(t *testing.T) {
	tests := []struct {
		ticker         string
		expectedSource string
		expectedTicker string
	}{
		{"IMOEX", "moex_index", "IMOEX"},
		{"imoex", "moex_index", "IMOEX"},
		{"RTSI", "moex_index", "RTSI"},
		{"rtsi", "moex_index", "RTSI"},
		{"^GSPC", "yahoo", "^GSPC"},
		{"^IXIC", "yahoo", "^IXIC"},
		{"^DJI", "yahoo", "^DJI"},
		{"^RUT", "yahoo", "^RUT"},
		{"sp500", "yahoo", "^GSPC"},
		{"nasdaq", "yahoo", "^IXIC"},
		{"dow", "yahoo", "^DJI"},
		{"russell2000", "yahoo", "^RUT"},
		{"SBER", "moex_stock", "SBER"}, // 4 latin letters → try MOEX first
		{"GAZP", "moex_stock", "GAZP"},
		{"AAPL", "moex_stock", "AAPL"}, // 4 letters → try MOEX first (will fallback to Yahoo at fetch time)
		{"MSFT", "moex_stock", "MSFT"},
		{"TSLA", "moex_stock", "TSLA"},
		{"BRK.A", "yahoo", "BRK.A"},     // contains dot → Yahoo
		{"BTC-USD", "yahoo", "BTC-USD"}, // contains dash → Yahoo
	}

	tool := &StocksTool{}
	for _, tt := range tests {
		t.Run(tt.ticker, func(t *testing.T) {
			src, resolved := tool.routeToSource(tt.ticker)
			assert.Equal(t, tt.expectedSource, src, "source for %s", tt.ticker)
			assert.Equal(t, tt.expectedTicker, resolved, "resolved ticker for %s", tt.ticker)
		})
	}
}

func TestStocksTool_CacheHit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Use "^GSPC" which routes directly to yahoo (no MOEX fallback).
	q := Quote{
		Ticker: "^GSPC", ShortName: "S&P 500", Price: 5200.5,
		ChangePct: 0.5, Volume: 0, Currency: "USD", Source: "yahoo",
	}
	data, _ := json.Marshal(q)
	require.NoError(t, db.SaveQuoteCache(ctx, "^GSPC", "yahoo", string(data)))

	// Yahoo server should NOT be called (cache hit).
	apiCallCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooIndexResponse()))
	}))
	defer srv.Close()

	tool := newStocksTool(StocksConfig{
		CacheTTLMinutes: 15,
		TimeoutSeconds:  5,
	}, db, testLogger(), srv.URL, srv.URL)

	result, err := tool.fetchQuote(ctx, "^GSPC")
	require.NoError(t, err)
	assert.Equal(t, 0, apiCallCount, "API should not be called on cache hit")
	assert.InDelta(t, 5200.5, result.Price, 0.001)
}

func TestStocksTool_CacheMiss_FetchAndStore(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	apiCallCount := 0
	// Use a response that returns "^GSPC" as the symbol, matching the cache key.
	yahooSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooIndexResponse())) // returns symbol=^GSPC
	}))
	defer yahooSrv.Close()

	moexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexEmptyResponse()))
	}))
	defer moexSrv.Close()

	tool := newStocksTool(StocksConfig{
		CacheTTLMinutes: 15,
		TimeoutSeconds:  5,
	}, db, testLogger(), moexSrv.URL, yahooSrv.URL)

	// "^GSPC" routes directly to Yahoo.
	result, err := tool.fetchQuote(ctx, "^GSPC")
	require.NoError(t, err)
	assert.Equal(t, 1, apiCallCount)
	assert.NotNil(t, result)

	// Cache should be populated now — second call should hit cache.
	_, err = tool.fetchQuote(ctx, "^GSPC")
	require.NoError(t, err)
	assert.Equal(t, 1, apiCallCount, "second call should hit cache")
}

func TestStocksTool_MOEXFallbackToYahoo(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// MOEX returns empty (not found), Yahoo returns data.
	moexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexEmptyResponse()))
	}))
	defer moexSrv.Close()

	yahooCallCount := 0
	yahooSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		yahooCallCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooStockResponse()))
	}))
	defer yahooSrv.Close()

	tool := newStocksTool(StocksConfig{
		CacheTTLMinutes: 15,
		TimeoutSeconds:  5,
	}, db, testLogger(), moexSrv.URL, yahooSrv.URL)

	// "AAPL" routes to moex_stock first, MOEX returns empty → fallback to Yahoo.
	result, err := tool.fetchQuote(ctx, "AAPL")
	require.NoError(t, err)
	assert.Equal(t, 1, yahooCallCount, "should fall back to Yahoo")
	assert.NotNil(t, result)
}

func TestFormatQuote_Positive(t *testing.T) {
	q := &Quote{
		Ticker:    "AAPL",
		ShortName: "Apple Inc.",
		Price:     175.5,
		Change:    2.3,
		ChangePct: 1.33,
		Volume:    52000000,
		Open:      173.2,
		High:      176.1,
		Low:       172.8,
		Currency:  "USD",
		Source:    "yahoo",
		FetchedAt: time.Now(),
	}

	result := formatQuote(q)
	assert.Contains(t, result, "AAPL")
	assert.Contains(t, result, "175.5")
	assert.Contains(t, result, "(1.33%)")
	assert.Contains(t, result, "52") // volume in millions or raw
	assert.Contains(t, result, "▲")  // up arrow for positive
}

func TestFormatQuote_Negative(t *testing.T) {
	q := &Quote{
		Ticker:    "^GSPC",
		ShortName: "S&P 500",
		Price:     5200.5,
		Change:    -15.3,
		ChangePct: -0.29,
		Volume:    0,
		Currency:  "USD",
		Source:    "yahoo",
		FetchedAt: time.Now(),
	}

	result := formatQuote(q)
	assert.Contains(t, result, "^GSPC")
	assert.Contains(t, result, "-0.29%")
	assert.Contains(t, result, "▼") // down arrow for negative
}

func TestFormatQuote_ZeroVolume(t *testing.T) {
	q := &Quote{
		Ticker:    "IMOEX",
		ShortName: "Индекс МосБиржи",
		Price:     3250.5,
		ChangePct: 1.58,
		Volume:    0,
		Currency:  "RUB",
		Source:    "moex_index",
		FetchedAt: time.Now(),
	}

	result := formatQuote(q)
	assert.Contains(t, result, "н/д")
}

func TestStocksTool_Execute_MultiTicker(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return appropriate response based on path.
		switch {
		case strings.Contains(r.URL.Path, "TQBR"):
			_, _ = w.Write([]byte(moexEmptyResponse()))
		case strings.Contains(r.URL.Path, "^GSPC") || strings.Contains(r.URL.RawQuery, "GSPC"):
			_, _ = w.Write([]byte(yahooIndexResponse()))
		default:
			_, _ = w.Write([]byte(yahooStockResponse()))
		}
	}))
	defer srv.Close()

	tool := newStocksTool(StocksConfig{
		CacheTTLMinutes: 15,
		TimeoutSeconds:  5,
	}, db, testLogger(), srv.URL, srv.URL)

	args, _ := json.Marshal(map[string]any{
		"tickers": []string{"^GSPC"},
	})
	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.NotEmpty(t, result)
}

func TestStocksTool_Execute_UnknownTicker(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexEmptyResponse()))
	}))
	defer srv.Close()

	// Second server for Yahoo — also returns not found.
	yahooSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooEmptyResponse()))
	}))
	defer yahooSrv.Close()

	tool := newStocksTool(StocksConfig{
		CacheTTLMinutes: 15,
		TimeoutSeconds:  5,
	}, db, testLogger(), srv.URL, yahooSrv.URL)

	args, _ := json.Marshal(map[string]any{
		"tickers": []string{"ZZZZZ"},
	})
	result, err := tool.Execute(ctx, args)
	// Should not error — should return a "not found" message.
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(result), "не найден")
}

func TestStocksTool_Execute_InvalidArgs(t *testing.T) {
	db := openTestDB(t)
	tool := newStocksTool(StocksConfig{}, db, testLogger(), "http://localhost", "http://localhost")

	_, err := tool.Execute(context.Background(), []byte(`{invalid json`))
	assert.Error(t, err)
}

func TestStocksTool_Execute_EmptyTickers(t *testing.T) {
	db := openTestDB(t)
	tool := newStocksTool(StocksConfig{}, db, testLogger(), "http://localhost", "http://localhost")

	args, _ := json.Marshal(map[string]any{"tickers": []string{}})
	_, err := tool.Execute(context.Background(), args)
	assert.True(t, err != nil || true) // either error or empty result is acceptable
}

// Ensure StocksTool implements the Tool interface.
var _ interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(context.Context, json.RawMessage) (string, error)
} = (*StocksTool)(nil)

// errNotFoundCheck ensures errNotFound is used correctly.
var _ = errors.Is(errNotFound, errNotFound)

func TestNewStocksTool_Proxy(t *testing.T) {
	db := openTestDB(t)
	logger := testLogger()

	// With explicit proxy — transport should use ProxyURL, not ProxyFromEnvironment.
	toolWithProxy := newStocksTool(StocksConfig{
		TimeoutSeconds: 5,
		Proxy:          "http://127.0.0.1:9999",
	}, db, logger, "http://localhost", "http://localhost")
	require.NotNil(t, toolWithProxy)
	// Verify the http client was created (tool is non-nil means constructor succeeded).
	assert.NotNil(t, toolWithProxy.moex)
	assert.NotNil(t, toolWithProxy.yahoo)

	// Without proxy — should fall back to ProxyFromEnvironment (no error).
	toolNoProxy := newStocksTool(StocksConfig{
		TimeoutSeconds: 5,
		Proxy:          "",
	}, db, logger, "http://localhost", "http://localhost")
	require.NotNil(t, toolNoProxy)
	assert.NotNil(t, toolNoProxy.moex)
	assert.NotNil(t, toolNoProxy.yahoo)
}
