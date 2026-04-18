package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// yahooAliases maps common index names/aliases to Yahoo Finance ticker symbols.
var yahooAliases = map[string]string{
	"sp500":       "^GSPC",
	"s&p500":      "^GSPC",
	"s&p 500":     "^GSPC",
	"nasdaq":      "^IXIC",
	"dji":         "^DJI",
	"dow":         "^DJI",
	"dow jones":   "^DJI",
	"russell":     "^RUT",
	"russell2000": "^RUT",
}

// resolveYahooAlias converts common index names to Yahoo Finance ticker symbols.
// Returns the input unchanged if no alias matches.
func resolveYahooAlias(ticker string) string {
	if resolved, ok := yahooAliases[strings.ToLower(ticker)]; ok {
		return resolved
	}
	return ticker
}

// yahooQuoteResult is the inner object in the Yahoo Finance v8 response.
type yahooQuoteResult struct {
	Symbol                     string  `json:"symbol"`
	ShortName                  string  `json:"shortName"`
	RegularMarketPrice         float64 `json:"regularMarketPrice"`
	RegularMarketChange        float64 `json:"regularMarketChange"`
	RegularMarketChangePercent float64 `json:"regularMarketChangePercent"`
	RegularMarketVolume        int64   `json:"regularMarketVolume"`
	RegularMarketOpen          float64 `json:"regularMarketOpen"`
	RegularMarketDayHigh       float64 `json:"regularMarketDayHigh"`
	RegularMarketDayLow        float64 `json:"regularMarketDayLow"`
	Currency                   string  `json:"currency"`
	MarketState                string  `json:"marketState"`
}

type yahooQuoteResponse struct {
	QuoteResponse struct {
		Result []yahooQuoteResult `json:"result"`
		Error  any                `json:"error"`
	} `json:"quoteResponse"`
}

// yahooClient fetches quotes from the Yahoo Finance v8 API.
type yahooClient struct {
	http    *http.Client
	baseURL string // overridable for tests
}

func newYahooClient(httpClient *http.Client, baseURL string) *yahooClient {
	return &yahooClient{http: httpClient, baseURL: baseURL}
}

// FetchQuote fetches a quote for the given ticker from Yahoo Finance.
// Ticker aliases (e.g. "sp500") are resolved automatically.
func (c *yahooClient) FetchQuote(ctx context.Context, ticker string) (*Quote, error) {
	resolved := resolveYahooAlias(ticker)

	url := fmt.Sprintf(
		"%s/v8/finance/quote?symbols=%s&fields=regularMarketPrice,regularMarketChange,regularMarketChangePercent,regularMarketVolume,shortName,regularMarketOpen,regularMarketDayHigh,regularMarketDayLow,currency,marketState",
		c.baseURL, resolved,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent-bot/1.0)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yahoo fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo http %d", resp.StatusCode)
	}

	var body yahooQuoteResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("yahoo decode: %w", err)
	}

	if len(body.QuoteResponse.Result) == 0 {
		return nil, errNotFound
	}

	r := body.QuoteResponse.Result[0]
	currency := r.Currency
	if currency == "" {
		currency = "USD"
	}

	return &Quote{
		Ticker:    r.Symbol,
		ShortName: r.ShortName,
		Price:     r.RegularMarketPrice,
		Change:    r.RegularMarketChange,
		ChangePct: r.RegularMarketChangePercent,
		Volume:    r.RegularMarketVolume,
		Open:      r.RegularMarketOpen,
		High:      r.RegularMarketDayHigh,
		Low:       r.RegularMarketDayLow,
		Source:    "yahoo",
		Currency:  currency,
		FetchedAt: time.Now(),
	}, nil
}
