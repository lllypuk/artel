package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// moexStockResponse returns a realistic MOEX ISS columnar JSON for a stock.
func moexStockResponse() string {
	return `{
		"marketdata": {
			"columns": ["SECID","BOARDID","LAST","LASTTOPREVPRICE","VOLTODAY","OPEN","HIGH","LOW","SYSTIME"],
			"data": [["SBER","TQBR",285.5,1.23,45000000,282.0,287.0,281.5,"2026-04-18 15:30:00"]]
		},
		"securities": {
			"columns": ["SECID","BOARDID","SHORTNAME","LOTSIZE","FACEVALUE","STATUS"],
			"data": [["SBER","TQBR","Сбербанк",10,3.0,"A"]]
		}
	}`
}

// moexIndexResponse returns a realistic MOEX ISS columnar JSON for an index.
func moexIndexResponse() string {
	return `{
		"marketdata": {
			"columns": ["SECID","BOARDID","CURRENTVALUE","LASTVALUE","CHANGE","OPEN","HIGH","LOW","SYSTIME"],
			"data": [["IMOEX","SNDX",3250.5,3200.0,1.58,3200.0,3260.0,3190.0,"2026-04-18 15:30:00"]]
		},
		"securities": {
			"columns": ["SECID","BOARDID","SHORTNAME","REGNUMBER","PREVLEGALCLOSEPRICE"],
			"data": [["IMOEX","SNDX","Индекс МосБиржи","","3200.0"]]
		}
	}`
}

// moexEmptyResponse simulates a ticker not found on MOEX.
func moexEmptyResponse() string {
	return `{
		"marketdata": {
			"columns": ["SECID","BOARDID","LAST","LASTTOPREVPRICE","VOLTODAY","OPEN","HIGH","LOW","SYSTIME"],
			"data": []
		},
		"securities": {
			"columns": ["SECID","BOARDID","SHORTNAME","LOTSIZE","FACEVALUE","STATUS"],
			"data": []
		}
	}`
}

func TestFetchMOEXStock_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexStockResponse()))
	}))
	defer srv.Close()

	client := newMOEXClient(http.DefaultClient, srv.URL)
	q, err := client.FetchStock(context.Background(), "SBER")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "SBER", q.Ticker)
	assert.Equal(t, "Сбербанк", q.ShortName)
	assert.InDelta(t, 285.5, q.Price, 0.001)
	assert.InDelta(t, 1.23, q.ChangePct, 0.001)
	assert.Equal(t, int64(45000000), q.Volume)
	assert.InDelta(t, 282.0, q.Open, 0.001)
	assert.InDelta(t, 287.0, q.High, 0.001)
	assert.InDelta(t, 281.5, q.Low, 0.001)
	assert.Equal(t, "moex_stock", q.Source)
	assert.Equal(t, "RUB", q.Currency)
	assert.WithinDuration(t, time.Now(), q.FetchedAt, 5*time.Second)
}

func TestFetchMOEXStock_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexEmptyResponse()))
	}))
	defer srv.Close()

	client := newMOEXClient(http.DefaultClient, srv.URL)
	q, err := client.FetchStock(context.Background(), "ZZZZZ")

	assert.Nil(t, q)
	assert.ErrorIs(t, err, errNotFound)
}

func TestFetchMOEXIndex_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(moexIndexResponse()))
	}))
	defer srv.Close()

	client := newMOEXClient(http.DefaultClient, srv.URL)
	q, err := client.FetchIndex(context.Background(), "IMOEX")

	require.NoError(t, err)
	require.NotNil(t, q)
	assert.Equal(t, "IMOEX", q.Ticker)
	assert.Equal(t, "Индекс МосБиржи", q.ShortName)
	assert.InDelta(t, 3250.5, q.Price, 0.001)
	assert.InDelta(t, 1.58, q.ChangePct, 0.001)
	assert.InDelta(t, 3200.0, q.Open, 0.001)
	assert.Equal(t, "moex_index", q.Source)
	assert.Equal(t, "RUB", q.Currency)
}

func TestFetchMOEXIndex_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newMOEXClient(http.DefaultClient, srv.URL)
	q, err := client.FetchIndex(context.Background(), "IMOEX")

	assert.Nil(t, q)
	assert.Error(t, err)
}

func TestMOEXColumnarParse_MissingColumn(t *testing.T) {
	// Ensure parsing doesn't panic when an expected column is absent.
	raw := `{
		"marketdata": {
			"columns": ["SECID","BOARDID"],
			"data": [["SBER","TQBR"]]
		},
		"securities": {
			"columns": ["SECID","BOARDID","SHORTNAME"],
			"data": [["SBER","TQBR","Сбербанк"]]
		}
	}`

	var resp moexSecurityResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))

	row := resp.Marketdata.toMap(0)
	// Missing columns return nil, not panic.
	assert.Nil(t, row["LAST"])
}
