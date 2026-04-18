package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SaveQuoteCache stores or updates a cached quote for the given ticker and source.
func (s *DB) SaveQuoteCache(ctx context.Context, ticker, source, dataJSON string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stock_quotes_cache (ticker, source, data_json, fetched_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(ticker, source) DO UPDATE SET
			data_json  = excluded.data_json,
			fetched_at = excluded.fetched_at
	`, ticker, source, dataJSON)
	if err != nil {
		return fmt.Errorf("save quote cache: %w", err)
	}
	return nil
}

// GetQuoteCache retrieves a cached quote if it exists and has not expired.
// Returns "" (empty string) on cache miss or when ttl == 0 (cache disabled).
func (s *DB) GetQuoteCache(ctx context.Context, ticker, source string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		return "", nil
	}

	var dataJSON string
	var fetchedAt time.Time

	err := s.db.QueryRowContext(ctx, `
		SELECT data_json, fetched_at
		FROM stock_quotes_cache
		WHERE ticker = ? AND source = ?
	`, ticker, source).Scan(&dataJSON, &fetchedAt)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get quote cache: %w", err)
	}

	if time.Since(fetchedAt) > ttl {
		return "", nil
	}

	return dataJSON, nil
}
