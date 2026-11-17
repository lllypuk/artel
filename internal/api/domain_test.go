package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/store"
)

func fixture(t *testing.T) (*store.Store, http.Handler) {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, New(t.Context(), s)
}

func request(t *testing.T, h http.Handler, method, path, body, key string, status int) []byte {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Idempotency-Key", key)
	r.Header.Set("Content-Type", "application/json")
	original := r.Clone(r.Context())
	original.Body = io.NopCloser(bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body)
	}
	checkContract(t, original, w)
	return w.Body.Bytes()
}

func TestManualWorkflow(t *testing.T) {
	s, h := fixture(t)
	body, _ := json.Marshal(map[string]any{"name": "Проект", "root_path": t.TempDir()})
	key := uuid.NewString()
	pbody := request(t, h, "POST", "/v1/projects", string(body), key, 201)
	if !bytes.Equal(pbody, request(t, h, "POST", "/v1/projects", string(body), key, 201)) {
		t.Fatal("receipt changed")
	}
	var p struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(pbody, &p)
	request(t, h, "POST", "/v1/projects", `{}`, key, 409)
	tdata := request(t, h, "POST", "/v1/projects/"+p.ID+"/tasks", `{"title":"Task"}`, uuid.NewString(), 201)
	var task struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(tdata, &task)
	request(t, h, "PATCH", "/v1/tasks/"+task.ID, `{"title":"Edited","expected_version":1}`, uuid.NewString(), 200)
	request(t, h, "PATCH", "/v1/tasks/"+task.ID, `{"title":"Stale","expected_version":1}`, uuid.NewString(), 409)
	q := `/v1/projects/` + p.ID + `/queue`
	request(t, h, "PUT", q, `{"task_ids":["`+task.ID+`"],"expected_queue_version":1}`, uuid.NewString(), 200)
	request(t, h, "POST", "/v1/tasks/"+task.ID+"/transition", `{"state":"in_progress","expected_version":3,"expected_queue_version":2}`, uuid.NewString(), 200)
	request(t, h, "POST", "/v1/tasks/"+task.ID+"/transition", `{"state":"done","expected_version":4}`, uuid.NewString(), 200)
	request(t, h, "PATCH", "/v1/projects/"+p.ID, `{"archived":true,"expected_version":1}`, uuid.NewString(), 200)
	request(t, h, "POST", "/v1/projects/"+p.ID+"/tasks", `{"title":"Denied"}`, uuid.NewString(), 409)
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.EventSeq != 7 {
		t.Fatalf("seq %d", snap.EventSeq)
	}
	request(t, h, "GET", "/v1/snapshot", "", "", 200)
	request(t, h, "GET", "/v1/events?instance_id="+snap.InstanceID+"&after=0&limit=1", "", "", 200)
	request(t, h, "GET", "/v1/events?instance_id="+uuid.NewString()+"&after=0", "", "", 409)
	request(t, h, "GET", "/v1/events?instance_id="+snap.InstanceID+"&after=999", "", "", 409)
}

func TestInvalidMutations(t *testing.T) {
	_, h := fixture(t)
	for _, body := range []string{``, `null`, `[]`, `{`, `{} {}`, `{"name":"x","root_path":"/","extra":1}`, `{"name":null,"root_path":"/"}`, `{"name":"x","name":"y","root_path":"/"}`} {
		t.Run(body, func(t *testing.T) { request(t, h, "POST", "/v1/projects", body, uuid.NewString(), 400) })
	}
	request(t, h, "POST", "/v1/projects", `{"name":"x","root_path":"/"}`, "", 400)
	request(t, h, "PATCH", "/v1/tasks/"+uuid.NewString(), `{"title":"x"}`, uuid.NewString(), 400)
	request(t, h, "PUT", "/v1/projects/"+uuid.NewString()+"/queue", `{"expected_queue_version":1}`, uuid.NewString(), 400)
	request(t, h, "GET", "/v1/events?after=-1", "", "", 400)
}
