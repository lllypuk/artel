package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lllypuk/artel/internal/domain"
)

// HistoryPage scans a bounded range, advancing even when filters match nothing.
// Identity, high-water mark and events are read in one transaction.
type HistoryPage struct {
	InstanceID string  `json:"instance_id"`
	Events     []Event `json:"events"`
	NextCursor int64   `json:"next_cursor"`
}

func (s *Store) History(ctx context.Context, instance string, after int64, limit int, project, task string) (HistoryPage, error) {
	page := HistoryPage{Events: []Event{}, NextCursor: after}
	if after < 0 || limit < 1 || limit > 500 {
		return page, domain.Invalid("invalid event cursor or limit")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	var high int64
	if err = tx.QueryRowContext(ctx, "SELECT instance_id,(SELECT coalesce(max(seq),0) FROM events) FROM metadata WHERE id=1").Scan(&page.InstanceID, &high); err != nil {
		return page, err
	}
	if instance != page.InstanceID || after > high {
		return page, &domain.Error{Code: "reset_required", Message: "snapshot required"}
	}
	rows, err := tx.QueryContext(ctx, "SELECT seq,action,project_id,coalesce(task_id,''),source,timestamp,changes FROM events WHERE seq>? ORDER BY seq LIMIT ?", after, limit)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e Event
		var changes string
		if err = rows.Scan(&e.Seq, &e.Action, &e.ProjectID, &e.TaskID, &e.Source, &e.Timestamp, &changes); err != nil {
			return page, err
		}
		page.NextCursor = e.Seq
		if project != "" && e.ProjectID != project {
			continue
		}
		if err = json.Unmarshal([]byte(changes), &e.Changes); err != nil {
			return page, err
		}
		if task != "" && !affectsTask(e, task) {
			continue
		}
		page.Events = append(page.Events, e)
	}
	if err = rows.Err(); err != nil {
		return page, err
	}
	if err = rows.Close(); err != nil {
		return page, err
	}
	return page, tx.Commit()
}

func affectsTask(e Event, id string) bool {
	if e.TaskID == id {
		return true
	}
	for _, c := range e.Changes {
		if c.Object == "task" && c.ID == id {
			return true
		}
	}
	return false
}
