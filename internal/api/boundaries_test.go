package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

func TestAPIBoundaries(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	other := eventProject(t, s)
	result := eventTask(t, s, p, uuid.NewString())
	var task domain.Task
	if err := json.Unmarshal(result.Body, &task); err != nil {
		t.Fatal(err)
	}
	project := "/v1/projects/" + p
	queue := project + "/queue"
	request(t, h, "PUT", "/v1/projects/"+other+"/queue", `{"task_ids":["`+task.ID+`"],"expected_queue_version":1}`, uuid.NewString(), 409)
	request(t, h, "PUT", queue, `{"task_ids":["`+task.ID+`","`+task.ID+`"],"expected_queue_version":1}`, uuid.NewString(), 400)
	request(t, h, "PUT", queue, `{"task_ids":[],"expected_queue_version":1}`, uuid.NewString(), 200)
	request(t, h, "PATCH", project, `{"expected_version":1}`, uuid.NewString(), 200)
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snap.EventSeq != 3 {
		t.Fatal("no-op emitted events")
	}
	request(t, h, "PUT", queue, `{"task_ids":["`+task.ID+`"],"expected_queue_version":1}`, uuid.NewString(), 200)
	request(t, h, "POST", "/v1/tasks/"+task.ID+"/transition", `{"state":"in_progress","expected_version":2}`, uuid.NewString(), 400)
	request(t, h, "PATCH", project, `{"archived":true,"expected_version":1}`, uuid.NewString(), 409)
	request(t, h, "PATCH", "/v1/tasks/"+task.ID, `{"state":"done","expected_version":2}`, uuid.NewString(), 400)
	request(t, h, "PATCH", "/v1/tasks/"+uuid.NewString(), `{"title":"Missing","expected_version":1}`, uuid.NewString(), 404)
	for _, body := range []string{`{"title":"` + strings.Repeat("я", 201) + `"}`, `{"title":"x","description":"` + strings.Repeat("x", 65537) + `"}`, `{"title":"x","acceptance_criteria":"` + strings.Repeat("x", 65537) + `"}`} {
		request(t, h, "POST", project+"/tasks", body, uuid.NewString(), 400)
	}
	request(t, h, "POST", project+"/tasks", `{"title":"`+strings.Repeat("x", MaxBody)+`"}`, uuid.NewString(), 413)
	// Task history includes queue mutations affecting the task, not only events
	// whose primary task_id is set.
	page, err := s.History(t.Context(), snap.InstanceID, 0, 100, "", task.ID)
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("task audit: %+v %v", page, err)
	}
	if page.Events[1].Action != "queue.replace" {
		t.Fatal("missing queue audit")
	}
}

func TestOversizedRealSnapshot(t *testing.T) {
	s, h := fixture(t)
	p := eventProject(t, s)
	// Seed legal-sized task records in one store transaction, avoiding quadratic
	// fixture setup through the intentionally full-snapshot MVP app persistence.
	_, err := s.Apply(t.Context(), store.Mutation{Key: uuid.NewString(), Method: "fixture", Path: "fixture", Hash: "fixture"}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for range 520 {
			snap.Tasks = append(snap.Tasks, domain.Task{ID: uuid.NewString(), ProjectID: p, Title: "Large fixture", Description: strings.Repeat("x", 65536), State: domain.Backlog, Version: 1, CreatedAt: now, UpdatedAt: now})
		}
		return store.Result{Status: 200, Body: []byte(`{}`)}, &store.Event{Action: "fixture", ProjectID: p, Source: "owner_manual", Timestamp: now, Changes: []store.Change{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request(t, h, "GET", "/v1/snapshot", "", "", 413)
}

func TestInternalErrorsAreSanitized(t *testing.T) {
	s, h := fixture(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/snapshot", "/v1/events?instance_id=" + uuid.NewString() + "&after=0"} {
		body := request(t, h, "GET", path, "", "", 500)
		if strings.Contains(string(body), "sql") || strings.Contains(string(body), "sqlite") {
			t.Fatal("internal detail leaked")
		}
	}
	w := httptest.NewRecorder()
	publicError(w, domain.Invalid("safe"))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
