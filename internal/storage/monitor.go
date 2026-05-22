package storage

import (
	"context"
	"database/sql"
	"time"
)

// MonitorCheck represents a single HTTP check result.
type MonitorCheck struct {
	ID         int64
	SiteURL    string
	SiteName   string
	CheckedAt  time.Time
	StatusCode int
	ResponseMS int64
	IsUp       bool
	ErrorText  string
}

// MonitorState represents the last known state of a monitored site.
type MonitorState struct {
	SiteURL        string
	SiteName       string
	IsUp           bool
	LastCheckedAt  time.Time
	LastStatusCode int
	LastResponseMS int64
	LastError      string
}

// InsertMonitorCheck stores one check result.
func (s *DB) InsertMonitorCheck(ctx context.Context, c MonitorCheck) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO monitor_checks (site_url, site_name, checked_at, status_code, response_ms, is_up, error_text)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.SiteURL, c.SiteName, c.CheckedAt.UTC().Format("2006-01-02 15:04:05"),
		c.StatusCode, c.ResponseMS, boolToInt(c.IsUp), c.ErrorText,
	)
	return err
}

// UpsertMonitorState creates or updates the last known state for a site.
func (s *DB) UpsertMonitorState(ctx context.Context, st MonitorState) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO monitor_state (site_url, site_name, is_up, last_checked_at, last_status_code, last_response_ms, last_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(site_url) DO UPDATE SET
		   site_name       = excluded.site_name,
		   is_up           = excluded.is_up,
		   last_checked_at = excluded.last_checked_at,
		   last_status_code = excluded.last_status_code,
		   last_response_ms = excluded.last_response_ms,
		   last_error      = excluded.last_error`,
		st.SiteURL, st.SiteName, boolToInt(st.IsUp),
		st.LastCheckedAt.UTC().Format("2006-01-02 15:04:05"),
		st.LastStatusCode, st.LastResponseMS, st.LastError,
	)
	return err
}

// GetMonitorState returns the last known state for a given site URL.
// Returns nil, nil if no state recorded yet.
func (s *DB) GetMonitorState(ctx context.Context, siteURL string) (*MonitorState, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT site_url, site_name, is_up, last_checked_at, last_status_code, last_response_ms, last_error
		 FROM monitor_state WHERE site_url = ?`,
		siteURL,
	)
	st, err := scanMonitorState(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return st, err
}

// GetAllMonitorStates returns last known states for all sites.
func (s *DB) GetAllMonitorStates(ctx context.Context) ([]MonitorState, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT site_url, site_name, is_up, last_checked_at, last_status_code, last_response_ms, last_error
		 FROM monitor_state ORDER BY site_url`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []MonitorState
	for rows.Next() {
		st, err := scanMonitorState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// GetMonitorHistory returns the N most recent checks for a given site URL.
func (s *DB) GetMonitorHistory(ctx context.Context, siteURL string, limit int) ([]MonitorCheck, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, site_url, site_name, checked_at, status_code, response_ms, is_up, error_text
		 FROM monitor_checks WHERE site_url = ?
		 ORDER BY checked_at DESC LIMIT ?`,
		siteURL, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck

	var out []MonitorCheck
	for rows.Next() {
		var c MonitorCheck
		var isUpInt int
		if err := rows.Scan(
			&c.ID, &c.SiteURL, &c.SiteName, &c.CheckedAt,
			&c.StatusCode, &c.ResponseMS, &isUpInt, &c.ErrorText,
		); err != nil {
			return nil, err
		}
		c.IsUp = isUpInt == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type monitorStateScanner interface {
	Scan(dest ...any) error
}

func scanMonitorState(s monitorStateScanner) (*MonitorState, error) {
	var st MonitorState
	var isUpInt int
	if err := s.Scan(
		&st.SiteURL, &st.SiteName, &isUpInt, &st.LastCheckedAt,
		&st.LastStatusCode, &st.LastResponseMS, &st.LastError,
	); err != nil {
		return nil, err
	}
	st.IsUp = isUpInt == 1
	return &st, nil
}
