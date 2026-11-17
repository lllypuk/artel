package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lllypuk/artel/internal/store"
)

const streamWriteTimeout = 2 * time.Second

// Each client holds at most one 100-event page and one small invalidation frame.
// Polling the committed SQLite log avoids a commit/notification loss window.
// Network writes happen only after History closes its transaction.
func (a *server) stream(w http.ResponseWriter, r *http.Request) {
	c, err := parseCursor(r, true)
	if err != nil {
		publicError(w, err)
		return
	}
	page, err := a.store.History(r.Context(), c.instance, c.after, 100, "", "")
	if err != nil {
		publicError(w, err)
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	if err = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
		return
	}
	if _, err = fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	if err = rc.Flush(); err != nil {
		return
	}
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		if !writePage(w, rc, page) {
			return
		}
		c.after = page.NextCursor
		if len(page.Events) == 100 {
			page, err = a.store.History(r.Context(), c.instance, c.after, 100, "", "")
			if err != nil {
				return
			}
			continue
		}
		select {
		case <-a.ctx.Done():
			return
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
				return
			}
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err = rc.Flush(); err != nil {
				return
			}
		case <-poll.C:
		}
		page, err = a.store.History(r.Context(), c.instance, c.after, 100, "", "")
		if err != nil {
			return
		}
	}
}

func writePage(w http.ResponseWriter, rc *http.ResponseController, page store.HistoryPage) bool {
	for _, event := range page.Events {
		data, _ := json.Marshal(struct {
			Kind      string `json:"kind"`
			ProjectID string `json:"project_id"`
			TaskID    string `json:"task_id,omitempty"`
		}{event.Action, event.ProjectID, event.TaskID})
		if err := rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout)); err != nil {
			return false
		}
		// #nosec G705 -- text/event-stream with JSON-encoded IDs; no HTML or raw user text.
		if _, err := fmt.Fprintf(w, "id: %d\nevent: invalidation\ndata: %s\n\n", event.Seq, data); err != nil {
			return false
		}
		if err := rc.Flush(); err != nil {
			return false
		}
	}
	return true
}
