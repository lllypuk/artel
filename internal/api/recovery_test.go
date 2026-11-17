package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/store"
)

func TestHTTPResponseLostAfterCommit(t *testing.T) {
	s, routes := fixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Drop-Response") == "yes" {
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, r) // mutation and receipt are committed before disconnect
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		routes.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	payload, _ := json.Marshal(map[string]string{"name": "Lost response", "root_path": t.TempDir()})
	key := uuid.NewString()
	for _, drop := range []bool{true, false} {
		req, err := http.NewRequestWithContext(t.Context(), "POST", srv.URL+"/v1/projects", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", key)
		if drop {
			req.Header.Set("X-Test-Drop-Response", "yes")
		}
		// #nosec G704 -- Destination is exclusively this test's ephemeral HTTP server.
		response, err := client.Do(req)
		if drop {
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil {
				t.Fatal("lost response looked successful")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != 201 {
			t.Fatalf("retry: %d %s %v", response.StatusCode, body, readErr)
		}
	}
	snap, err := s.Snapshot(t.Context())
	if err != nil || snap.EventSeq != 1 || len(snap.Projects) != 1 {
		t.Fatalf("duplicate effect: %+v %v", snap, err)
	}
}

func TestHTTPConcurrentEdits(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	start := make(chan struct{})
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 2)
	requests := make([]*http.Request, 2)
	for i := range 2 {
		req := httptest.NewRequest("PATCH", "/v1/projects/"+p, strings.NewReader(`{"name":"Edited","expected_version":1}`))
		req.Header.Set("Idempotency-Key", uuid.NewString())
		req.Header.Set("Content-Type", "application/json")
		requests[i] = req.Clone(req.Context())
		requests[i].Body = io.NopCloser(strings.NewReader(`{"name":"Edited","expected_version":1}`))
		responses[i] = httptest.NewRecorder()
		wg.Go(func() { <-start; h.ServeHTTP(responses[i], req) })
	}
	close(start)
	wg.Wait()
	successes, conflicts := 0, 0
	for i, response := range responses {
		switch response.Code {
		case 200:
			successes++
		case 409:
			conflicts++
		default:
			t.Fatalf("unexpected %d: %s", response.Code, response.Body)
		}
		checkContract(t, requests[i], response)
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("CAS did not select one winner")
	}
	snap, err := s.Snapshot(t.Context())
	if err != nil || snap.EventSeq != 2 {
		t.Fatalf("partial mutation: %+v %v", snap, err)
	}
	var page store.HistoryPage
	body := request(t, h, "GET", "/v1/events?instance_id="+snap.InstanceID+"&after=0", "", "", 200)
	if err = json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 {
		t.Fatal("duplicate audit")
	}
}
