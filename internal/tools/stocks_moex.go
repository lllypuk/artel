package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// errNotFound is returned when a ticker is not found on the exchange.
var errNotFound = errors.New("ticker not found")

// Quote represents a market quote for a stock or index.
type Quote struct {
	Ticker    string
	ShortName string
	Price     float64
	Change    float64 // absolute change
	ChangePct float64 // percentage change
	Volume    int64
	Open      float64
	High      float64
	Low       float64
	Source    string // "yahoo" | "moex_stock" | "moex_index"
	Currency  string // "USD" | "RUB"
	FetchedAt time.Time
}

// moexColumnarData represents the MOEX ISS columnar response format.
type moexColumnarData struct {
	Columns []string `json:"columns"`
	Data    [][]any  `json:"data"`
}

// toMap converts row i of the columnar data to a map[columnName]value.
// Returns an empty map (not nil) if the row index is out of range.
func (c *moexColumnarData) toMap(i int) map[string]any {
	result := make(map[string]any, len(c.Columns))
	if i >= len(c.Data) {
		return result
	}
	for j, col := range c.Columns {
		if j < len(c.Data[i]) {
			result[col] = c.Data[i][j]
		}
	}
	return result
}

// moexSecurityResponse is the top-level response from MOEX ISS for securities.
type moexSecurityResponse struct {
	Marketdata moexColumnarData `json:"marketdata"`
	Securities moexColumnarData `json:"securities"`
}

// moexClient fetches quotes from the MOEX ISS REST API.
type moexClient struct {
	http    *http.Client
	baseURL string // overridable for tests
}

func newMOEXClient(httpClient *http.Client, baseURL string) *moexClient {
	return &moexClient{http: httpClient, baseURL: baseURL}
}

// FetchStock fetches a quote for a stock traded on MOEX board TQBR.
func (c *moexClient) FetchStock(ctx context.Context, ticker string) (*Quote, error) {
	url := fmt.Sprintf(
		"%s/iss/engines/stock/markets/shares/boards/TQBR/securities/%s.json?iss.meta=off&iss.only=marketdata,securities",
		c.baseURL, ticker,
	)

	var resp moexSecurityResponse
	if err := c.fetch(ctx, url, &resp); err != nil {
		return nil, err
	}

	if len(resp.Marketdata.Data) == 0 {
		return nil, errNotFound
	}

	md := resp.Marketdata.toMap(0)
	sec := resp.Securities.toMap(0)

	price, _ := toFloat(md["LAST"])
	changePct, _ := toFloat(md["LASTTOPREVPRICE"])
	volume, _ := toInt64(md["VOLTODAY"])
	open, _ := toFloat(md["OPEN"])
	high, _ := toFloat(md["HIGH"])
	low, _ := toFloat(md["LOW"])
	shortName, _ := sec["SHORTNAME"].(string)

	// Absolute change: price * changePct / 100 (prev price = price - change)
	prevPrice := 0.0
	if changePct != 0 {
		prevPrice = price * 100 / (100 + changePct)
	}
	change := price - prevPrice

	return &Quote{
		Ticker:    ticker,
		ShortName: shortName,
		Price:     price,
		Change:    change,
		ChangePct: changePct,
		Volume:    volume,
		Open:      open,
		High:      high,
		Low:       low,
		Source:    "moex_stock",
		Currency:  "RUB",
		FetchedAt: time.Now(),
	}, nil
}

// FetchIndex fetches a quote for a MOEX index (IMOEX, RTSI, etc.).
func (c *moexClient) FetchIndex(ctx context.Context, ticker string) (*Quote, error) {
	url := fmt.Sprintf(
		"%s/iss/engines/stock/markets/index/securities/%s.json?iss.meta=off&iss.only=marketdata,securities",
		c.baseURL, ticker,
	)

	var resp moexSecurityResponse
	if err := c.fetch(ctx, url, &resp); err != nil {
		return nil, err
	}

	if len(resp.Marketdata.Data) == 0 {
		return nil, errNotFound
	}

	md := resp.Marketdata.toMap(0)
	sec := resp.Securities.toMap(0)

	price, _ := toFloat(md["CURRENTVALUE"])
	lastValue, _ := toFloat(md["LASTVALUE"])
	changePct, _ := toFloat(md["CHANGE"])
	open, _ := toFloat(md["OPEN"])
	high, _ := toFloat(md["HIGH"])
	low, _ := toFloat(md["LOW"])
	shortName, _ := sec["SHORTNAME"].(string)

	change := price - lastValue

	return &Quote{
		Ticker:    ticker,
		ShortName: shortName,
		Price:     price,
		Change:    change,
		ChangePct: changePct,
		Open:      open,
		High:      high,
		Low:       low,
		Source:    "moex_index",
		Currency:  "RUB",
		FetchedAt: time.Now(),
	}, nil
}

func (c *moexClient) fetch(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("moex fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("moex http %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("moex decode: %w", err)
	}
	return nil
}

// toFloat safely converts an interface{} JSON number to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// toInt64 safely converts an interface{} JSON number to int64.
func toInt64(v any) (int64, bool) {
	f, ok := toFloat(v)
	return int64(f), ok
}
