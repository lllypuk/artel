package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// stooqIndexMap maps canonical Yahoo-style index symbols to their Stooq equivalents.
var stooqIndexMap = map[string]string{
	"^gspc": "^SPX",
	"^ixic": "^NDQ",
	"^dji":  "^DJI",
	"^rut":  "^RUT",
}

// toStooqTicker converts a canonical (Yahoo-style) ticker to the Stooq API symbol.
//
//   - Known index symbols (^GSPC → ^SPX, ^IXIC → ^NDQ) are remapped.
//   - Other ^-prefixed symbols pass through unchanged.
//   - Class-share notation (BRK.A) is converted to Stooq format (BRK-A.US).
//   - Plain US stock tickers (AAPL) get a .US exchange suffix.
func toStooqTicker(ticker string) string {
	if mapped, ok := stooqIndexMap[strings.ToLower(ticker)]; ok {
		return mapped
	}
	if strings.HasPrefix(ticker, "^") {
		return ticker
	}
	// Class-share like BRK.A → BRK-A.US
	if dotIdx := strings.IndexByte(ticker, '.'); dotIdx > 0 {
		return ticker[:dotIdx] + "-" + ticker[dotIdx+1:] + ".US"
	}
	return ticker + ".US"
}

type stooqSymbol struct {
	Symbol   string  `json:"symbol"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   int64   `json:"volume"`
	Previous float64 `json:"previous"`
}

type stooqAPIResponse struct {
	Symbols []stooqSymbol `json:"symbols"`
}

// stooqClient fetches quotes from stooq.com.
type stooqClient struct {
	http    *http.Client
	baseURL string
}

func newStooqClient(httpClient *http.Client, baseURL string) *stooqClient {
	return &stooqClient{http: httpClient, baseURL: baseURL}
}

// FetchQuote fetches a quote from Stooq for the given ticker.
// Accepts Yahoo-style symbols (^GSPC, AAPL) — toStooqTicker handles the mapping.
// The returned Quote.Ticker is always the input ticker for cache-key stability.
func (c *stooqClient) FetchQuote(ctx context.Context, ticker string) (*Quote, error) {
	apiTicker := toStooqTicker(ticker)

	url := fmt.Sprintf("%s/q/l/?s=%s&f=sd2t2ohlcvp&h&e=json", c.baseURL, apiTicker)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; agent-bot/1.0)")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stooq fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("stooq http %d", resp.StatusCode)
	}

	var body stooqAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("stooq decode: %w", err)
	}

	if len(body.Symbols) == 0 || body.Symbols[0].Close == 0 {
		return nil, errNotFound
	}

	s := body.Symbols[0]
	change := s.Close - s.Previous
	changePct := 0.0
	if s.Previous != 0 {
		changePct = change / s.Previous * 100
	}

	return &Quote{
		Ticker:    ticker,
		Price:     s.Close,
		Change:    change,
		ChangePct: changePct,
		Volume:    s.Volume,
		Open:      s.Open,
		High:      s.High,
		Low:       s.Low,
		Source:    "stooq",
		Currency:  "USD",
		FetchedAt: time.Now(),
	}, nil
}
