package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/store"
)

func eventProject(t *testing.T, s *store.Store) string {
	t.Helper()
	name, root := "Project", t.TempDir()
	result, err := app.New(s).Execute(t.Context(), uuid.NewString(), "POST", "/v1/projects", app.Command{Action: app.CreateProject, Name: &name, RootPath: &root})
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(result.Body, &p); err != nil {
		t.Fatal(err)
	}
	return p.ID
}
func eventTask(t *testing.T, s *store.Store, p, key string) store.Result {
	t.Helper()
	title := "Never copy this description into SSE"
	result, err := app.New(s).Execute(t.Context(), key, "POST", "/v1/projects/"+p+"/tasks", app.Command{Action: app.CreateTask, ProjectID: p, Title: &title})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func openStream(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G704 -- URL is always the test-owned ephemeral httptest server.
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		_ = response.Body.Close()
		t.Fatalf("stream status %d", response.StatusCode)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}
func nextID(t *testing.T, scanner *bufio.Scanner) int {
	t.Helper()
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Never copy") {
			t.Fatal("SSE leaked full text")
		}
		var id int
		if strings.HasPrefix(line, "id: ") {
			if _, err := fmt.Sscanf(line, "id: %d", &id); err != nil {
				t.Fatal(err)
			}
			return id
		}
	}
	t.Fatalf("stream ended: %v", scanner.Err())
	return 0
}
func TestStreamReplayReconnectAndShutdown(t *testing.T) {
	s, _ := fixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	srv := httptest.NewServer(New(ctx, s))
	defer srv.Close()
	client := &http.Client{Timeout: 60 * time.Second}
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p := eventProject(t, s) // commit between snapshot and subscription
	url := srv.URL + "/v1/stream?instance_id=" + snap.InstanceID + "&after="
	response := openStream(t, client, url+"0")
	scanner := bufio.NewScanner(response.Body)
	if nextID(t, scanner) != 1 {
		t.Fatal("missed snapshot/subscribe gap")
	}
	_ = response.Body.Close()
	key := uuid.NewString()
	first := eventTask(t, s, p, key) // response is discarded by the client
	response = openStream(t, client, url+"1")
	defer func() { _ = response.Body.Close() }()
	scanner = bufio.NewScanner(response.Body)
	if nextID(t, scanner) != 2 {
		t.Fatal("missed reconnect replay")
	}
	repeated := eventTask(t, s, p, key)
	if string(first.Body) != string(repeated.Body) {
		t.Fatal("receipt changed")
	}
	for range 110 {
		eventTask(t, s, p, uuid.NewString())
	}
	for want := 3; want <= 112; want++ {
		if got := nextID(t, scanner); got != want {
			t.Fatalf("got %d want %d", got, want)
		}
	}
	snap, err = s.Snapshot(t.Context())
	if err != nil || snap.EventSeq != 112 {
		t.Fatalf("duplicate/lost event: %+v %v", snap, err)
	}
	cancel()
	if _, err = io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
}

// A ResponseController-aware slow socket fixture deterministically blocks writes
// until the per-write deadline; it avoids depending on OS TCP buffer sizes.
type slowWriter struct {
	header   http.Header
	deadline time.Time
	writes   int
}

func (w *slowWriter) Header() http.Header                { return w.header }
func (*slowWriter) WriteHeader(int)                      {}
func (w *slowWriter) SetWriteDeadline(d time.Time) error { w.deadline = d; return nil }
func (w *slowWriter) FlushError() error                  { return nil }
func (w *slowWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		return len(p), nil
	}
	time.Sleep(time.Until(w.deadline))
	return 0, context.DeadlineExceeded
}
func TestStreamSlowConsumerDoesNotHoldTransaction(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	w := &slowWriter{header: http.Header{}}
	req := httptest.NewRequest("GET", "/v1/stream?instance_id="+snap.InstanceID+"&after=0", nil)
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, req); close(done) }()
	// Independent mutations must continue while the client is blocked.
	eventTask(t, s, p, uuid.NewString())
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("slow stream not disconnected")
	}
	if w.writes != 2 {
		t.Fatalf("writes: %d", w.writes)
	}
	page, err := s.History(t.Context(), snap.InstanceID, 0, 100, "", "")
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("replay unavailable: %+v %v", page, err)
	}
}

func TestHistoryFiltersAdvanceAndLimits(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	eventTask(t, s, p, uuid.NewString())
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	url := "/v1/events?instance_id=" + snap.InstanceID + "&after=0&limit=1&project_id=" + uuid.NewString()
	var page store.HistoryPage
	if err = json.Unmarshal(request(t, h, "GET", url, "", "", 200), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 || page.NextCursor != 1 {
		t.Fatalf("cursor did not advance: %+v", page)
	}
	for _, suffix := range []string{"&limit=0", "&limit=501", "&limit=2&limit=3", "&token=secret", "&project_id=nope"} {
		request(t, h, "GET", "/v1/events?instance_id="+snap.InstanceID+"&after=0"+suffix, "", "", 400)
	}
	request(t, h, "GET", "/v1/stream?instance_id="+uuid.NewString()+"&after=0", "", "", 409)
	request(t, h, "GET", "/v1/stream?instance_id="+snap.InstanceID+"&after=99", "", "", 409)
}

func TestSnapshotLimit(t *testing.T) {
	for _, size := range []int{MaxSnapshot, MaxSnapshot + 1} {
		w := httptest.NewRecorder()
		sendSnapshot(w, make([]byte, size))
		want := 200
		if size > MaxSnapshot {
			want = 413
		}
		if w.Code != want {
			t.Fatalf("size %d status %d", size, w.Code)
		}
		if want == 413 && strings.Contains(w.Body.String(), "projects") {
			t.Fatal("partial snapshot")
		}
	}
}
