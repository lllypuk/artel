package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stooqStockResponse() string {
	// AAPL.US: close=175.5, previous=173.2 → change=2.3, changePct≈1.328%
	return `{"symbols":[{"symbol":"AAPL.US","open":173.2,"high":176.1,"low":172.8,"close":175.5,"volume":52000000,"previous":173.2}]}`
}

func stooqIndexResponse() string {
	// ^SPX: close=5200.5, previous=5215.8 → change=-15.3, changePct≈-0.293%
	return `{"symbols":[{"symbol":"^SPX","open":5215.8,"high":5220.0,"low":5190.0,"close":5200.5,"volume":0,"previous":5215.8}]}`
}

func stooqEmptyResponse() string {
	// Symbol present but no data (Close == 0) → errNotFound
	return `{"symbols":[{"symbol":"ZZZZZ.US"}]}`
}

func TestFetchStooqQuote_Stock_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.RawQuery, "AAPL")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stooqStockResponse()))
	}))
	defer srv.Close()

	client := newStooqClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "AAPL")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "AAPL", q.Ticker)
	assert.InDelta(t, 175.5, q.Price, 0.001)
	assert.InDelta(t, 2.3, q.Change, 0.001)
	assert.InDelta(t, 1.328, q.ChangePct, 0.01)
	assert.Equal(t, int64(52000000), q.Volume)
	assert.InDelta(t, 173.2, q.Open, 0.001)
	assert.InDelta(t, 176.1, q.High, 0.001)
	assert.InDelta(t, 172.8, q.Low, 0.001)
	assert.Equal(t, "stooq", q.Source)
	assert.Equal(t, "USD", q.Currency)
}

func TestFetchStooqQuote_Index_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stooqIndexResponse()))
	}))
	defer srv.Close()

	client := newStooqClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "^GSPC")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "^GSPC", q.Ticker) // input ticker preserved
	assert.InDelta(t, 5200.5, q.Price, 0.001)
	assert.InDelta(t, -0.293, q.ChangePct, 0.01)
	assert.Equal(t, "stooq", q.Source)
}

func TestFetchStooqQuote_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(stooqEmptyResponse()))
	}))
	defer srv.Close()

	client := newStooqClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "ZZZZZ")

	assert.Nil(t, q)
	assert.ErrorIs(t, err, errNotFound)
}

func TestFetchStooqQuote_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	client := newStooqClient(http.DefaultClient, srv.URL)
	q, err := client.FetchQuote(context.Background(), "AAPL")

	assert.Nil(t, q)
	assert.Error(t, err)
}

func TestToStooqTicker(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"^GSPC", "^SPX"},
		{"^IXIC", "^NDQ"},
		{"^DJI", "^DJI"},
		{"^RUT", "^RUT"},
		{"AAPL", "AAPL.US"},
		{"MSFT", "MSFT.US"},
		{"BRK.A", "BRK-A.US"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toStooqTicker(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
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
