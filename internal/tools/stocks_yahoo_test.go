package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func yahooStockResponse() string {
	return `{
		"quoteResponse": {
			"result": [{
				"symbol": "AAPL",
				"shortName": "Apple Inc.",
				"regularMarketPrice": 175.5,
				"regularMarketChange": 2.30,
				"regularMarketChangePercent": 1.33,
				"regularMarketVolume": 52000000,
				"regularMarketOpen": 173.2,
				"regularMarketDayHigh": 176.1,
				"regularMarketDayLow": 172.8,
				"currency": "USD",
				"marketState": "REGULAR"
			}],
			"error": null
		}
	}`
}

func yahooIndexResponse() string {
	return `{
		"quoteResponse": {
			"result": [{
				"symbol": "^GSPC",
				"shortName": "S&P 500",
				"regularMarketPrice": 5200.5,
				"regularMarketChange": -15.30,
				"regularMarketChangePercent": -0.29,
				"regularMarketVolume": 0,
				"regularMarketOpen": 5215.8,
				"regularMarketDayHigh": 5220.0,
				"regularMarketDayLow": 5190.0,
				"currency": "USD",
				"marketState": "REGULAR"
			}],
			"error": null
		}
	}`
}

func yahooEmptyResponse() string {
	return `{
		"quoteResponse": {
			"result": [],
			"error": null
		}
	}`
}

func TestFetchYahooQuote_Stock_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Query().Get("symbols"), "AAPL")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooStockResponse()))
	}))
	defer srv.Close()

	client := newYahooClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "AAPL")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "AAPL", q.Ticker)
	assert.Equal(t, "Apple Inc.", q.ShortName)
	assert.InDelta(t, 175.5, q.Price, 0.001)
	assert.InDelta(t, 2.30, q.Change, 0.001)
	assert.InDelta(t, 1.33, q.ChangePct, 0.001)
	assert.Equal(t, int64(52000000), q.Volume)
	assert.InDelta(t, 173.2, q.Open, 0.001)
	assert.InDelta(t, 176.1, q.High, 0.001)
	assert.InDelta(t, 172.8, q.Low, 0.001)
	assert.Equal(t, "yahoo", q.Source)
	assert.Equal(t, "USD", q.Currency)
}

func TestFetchYahooQuote_Index_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooIndexResponse()))
	}))
	defer srv.Close()

	client := newYahooClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "^GSPC")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "^GSPC", q.Ticker)
	assert.Equal(t, "S&P 500", q.ShortName)
	assert.InDelta(t, 5200.5, q.Price, 0.001)
	assert.InDelta(t, -0.29, q.ChangePct, 0.001)
	assert.Equal(t, "yahoo", q.Source)
}

func TestFetchYahooQuote_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(yahooEmptyResponse()))
	}))
	defer srv.Close()

	client := newYahooClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "ZZZZZ")

	assert.Nil(t, q)
	assert.ErrorIs(t, err, errNotFound)
}

func TestFetchYahooQuote_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	client := newYahooClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "AAPL")

	assert.Nil(t, q)
	assert.Error(t, err)
}

func TestResolveYahooAlias(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"sp500", "^GSPC"},
		{"SP500", "^GSPC"},
		{"s&p500", "^GSPC"},
		{"s&p 500", "^GSPC"},
		{"nasdaq", "^IXIC"},
		{"NASDAQ", "^IXIC"},
		{"dji", "^DJI"},
		{"dow jones", "^DJI"},
		{"dow", "^DJI"},
		{"russell", "^RUT"},
		{"russell2000", "^RUT"},
		{"AAPL", "AAPL"},   // no alias → unchanged
		{"^GSPC", "^GSPC"}, // already resolved
		{"SBER", "SBER"},   // Russian stock → unchanged
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := resolveYahooAlias(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
