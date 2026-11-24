package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
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

func TestWorkTypeCommandJSONIsStrict(t *testing.T) {
	_, h := fixture(t)
	projectBody, _ := json.Marshal(map[string]string{"name": "Strict work types", "root_path": t.TempDir()})
	projectData := request(t, h, "POST", "/v1/projects", string(projectBody), uuid.NewString(), 201)
	var project struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(projectData, &project); err != nil {
		t.Fatal(err)
	}
	taskData := request(t, h, "POST", "/v1/projects/"+project.ID+"/tasks", `{"title":"Legacy Task"}`, uuid.NewString(), 201)
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(taskData, &task); err != nil {
		t.Fatal(err)
	}
	createPath := "/v1/projects/" + project.ID + "/tasks"
	for _, body := range []string{
		`{"title":"null kind","kind":null}`,
		`{"title":"unknown kind","kind":"story"}`,
		`{"title":"null parent","parent_id":null}`,
		`{"title":"bad parent","parent_id":"not-a-uuid"}`,
		`{"title":"bad Bug field","expected_behavior":1}`,
	} {
		request(t, h, "POST", createPath, body, uuid.NewString(), 400)
	}
	for _, body := range []string{
		`{"kind":"bug","expected_version":1}`,
		`{"parent_id":null,"expected_version":1}`,
		`{"parent_id":"not-a-uuid","expected_version":1}`,
		`{"fix_evidence":false,"expected_version":1}`,
	} {
		request(t, h, "PATCH", "/v1/tasks/"+task.ID, body, uuid.NewString(), 400)
	}
	request(t, h, "POST", "/v1/tasks/"+task.ID+"/transition", `{"state":"open","expected_version":1}`, uuid.NewString(), 409)
}

type legacyDomainReceipts struct {
	projectID, taskID            string
	createKey, editKey           string
	createRequest, editRequest   string
	createResponse, editResponse []byte
}

func legacyCommandHash(command string) string {
	sum := sha256.Sum256([]byte(command))
	return hex.EncodeToString(sum[:])
}

func execLegacySchema(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	// #nosec G304 -- The caller selects only checked-in schema fixture filenames.
	data, err := os.ReadFile(filepath.Join("..", "store", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), string(data)); err != nil {
		t.Fatal(err)
	}
}

func insertLegacyDomainReceipt(t *testing.T, db *sql.DB, key, method, path, canonicalCommand string, status int, body string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), "INSERT INTO mutation_receipts(key,method,path,payload_hash,status,body) VALUES(?,?,?,?,?,?)",
		key, method, path, legacyCommandHash(canonicalCommand), status, body); err != nil {
		t.Fatal(err)
	}
}

func seedLegacyDomainReceipts(t *testing.T, path string) legacyDomainReceipts {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"testdata/v1.sql", "observer_schema.sql", "delivery_schema.sql", "questions_schema.sql", "results_schema.sql", "current_reviews_schema.sql"} {
		execLegacySchema(t, db, name)
	}
	const (
		projectID = "11111111-1111-4111-8111-111111111111"
		taskID    = "22222222-2222-4222-8222-222222222222"
		createKey = "33333333-3333-4333-8333-333333333333"
		editKey   = "44444444-4444-4444-8444-444444444444"
		created   = "2026-11-21T12:00:00Z"
		edited    = "2026-11-21T12:01:00Z"
	)
	// These bytes and hashes are frozen v6 data, not values produced by the
	// current domain or app.Command serializers. Reordering current fields must
	// therefore break this fixture rather than silently changing both sides.
	projectData := `{"id":"` + projectID + `","name":"Legacy","root_path":"/fictional/v6","description":"","version":1,"queue_version":1,"archived":false,"created_at":"` + created + `","updated_at":"` + created + `"}`
	createBody := `{"id":"` + taskID + `","project_id":"` + projectID + `","title":"Legacy","description":"","acceptance_criteria":"","state":"backlog","version":1,"created_at":"` + created + `","updated_at":"` + created + `"}`
	editBody := `{"id":"` + taskID + `","project_id":"` + projectID + `","title":"Edited","description":"","acceptance_criteria":"","state":"backlog","version":2,"created_at":"` + created + `","updated_at":"` + edited + `"}`
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata(id,instance_id) VALUES(1,?)", "55555555-5555-4555-8555-555555555555"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO projects(id,root_path,archived,record) VALUES(?,?,0,?)", projectID, "/fictional/v6", projectData); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO tasks(id,project_id,state,record) VALUES(?,?,?,?)", taskID, projectID, domain.Backlog, editBody); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO events(action,project_id,task_id,source,timestamp,changes) VALUES
		('task.create',?,?,'owner_manual',?,'[]'),('task.edit',?,?,'owner_manual',?,'[]')`, projectID, taskID, created, projectID, taskID, edited); err != nil {
		t.Fatal(err)
	}
	createPath, editPath := "/v1/projects/"+projectID+"/tasks", "/v1/tasks/"+taskID
	const createCanonical = `{"action":"task.create","project_id":"11111111-1111-4111-8111-111111111111","title":"Legacy"}`
	const editCanonical = `{"action":"task.edit","task_id":"22222222-2222-4222-8222-222222222222","title":"Edited","expected_version":1}`
	insertLegacyDomainReceipt(t, db, createKey, "POST", createPath, createCanonical, 201, createBody)
	insertLegacyDomainReceipt(t, db, editKey, "PATCH", editPath, editCanonical, 200, editBody)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return legacyDomainReceipts{projectID: projectID, taskID: taskID, createKey: createKey, editKey: editKey,
		createRequest: `{"title":"Legacy"}`, editRequest: `{"title":"Edited","expected_version":1}`, createResponse: []byte(createBody), editResponse: []byte(editBody)}
}

func TestLegacyV6CreateEditReceiptReplayAfterMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v6.sqlite")
	legacy := seedLegacyDomainReceipts(t, path)
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := New(t.Context(), s)
	before, err := s.Snapshot(t.Context())
	if err != nil || before.EventSeq != 2 {
		t.Fatalf("migrated v6 history: %+v %v", before, err)
	}
	createReplay := request(t, h, "POST", "/v1/projects/"+legacy.projectID+"/tasks", legacy.createRequest, legacy.createKey, 201)
	if !bytes.Equal(createReplay, legacy.createResponse) {
		t.Fatalf("v6 create receipt changed: %s", createReplay)
	}
	editReplay := request(t, h, "PATCH", "/v1/tasks/"+legacy.taskID, legacy.editRequest, legacy.editKey, 200)
	if !bytes.Equal(editReplay, legacy.editResponse) {
		t.Fatalf("v6 edit receipt changed: %s", editReplay)
	}
	afterReplay, err := s.Snapshot(t.Context())
	if err != nil || afterReplay.EventSeq != before.EventSeq || len(afterReplay.Tasks) != 1 || afterReplay.Tasks[0].Kind != domain.KindTask {
		t.Fatalf("v6 replay wrote data or lost default kind: %+v %v", afterReplay, err)
	}
	request(t, h, "POST", "/v1/projects/"+legacy.projectID+"/tasks", `{"title":"Fresh legacy client"}`, uuid.NewString(), 201)
	afterNew, err := s.Snapshot(t.Context())
	if err != nil || afterNew.EventSeq != before.EventSeq+1 || len(afterNew.Tasks) != 2 {
		t.Fatalf("new legacy request did not create exactly one Task: %+v %v", afterNew, err)
	}
	for _, task := range afterNew.Tasks {
		if task.Kind != domain.KindTask || task.ParentID != "" {
			t.Fatalf("legacy-compatible new Task: %+v", task)
		}
	}
}

func createEpicForAPI(t *testing.T, h http.Handler, projectID string) string {
	t.Helper()
	data := request(t, h, "POST", "/v1/projects/"+projectID+"/tasks", `{"kind":"epic","title":"Gate Epic"}`, uuid.NewString(), 201)
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &task); err != nil {
		t.Fatal(err)
	}
	return task.ID
}

func convertTaskToEpic(t *testing.T, s *store.Store, taskID string) {
	t.Helper()
	_, err := s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		found := false
		projectID := ""
		for i := range snap.Tasks {
			if snap.Tasks[i].ID == taskID {
				snap.Tasks[i].Kind = domain.KindEpic
				snap.Tasks[i].State = domain.Open
				snap.Tasks[i].ParentID = ""
				projectID = snap.Tasks[i].ProjectID
				snap.Queues[projectID] = []string{}
				found = true
			}
		}
		if !found {
			t.Fatal("missing task conversion fixture")
		}
		return store.Result{Status: 200, Body: []byte(`{}`)}, &store.Event{Action: "test.convert_epic", ProjectID: projectID, TaskID: taskID, Timestamp: "2026-11-22T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEpicCommissionAndOwnerQuestionGates(t *testing.T) {
	t.Run("commission", func(t *testing.T) {
		e := newDeliveryOwnerEnv(t, true)
		epicID := createEpicForAPI(t, e.owner, e.project.ID)
		before, err := e.s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		e.post(t, epicID, uuid.NewString(), "not executable", e.binding.ID, e.instance, 400)
		if messages := e.list(t, epicID, "", 200); len(messages) != 0 {
			t.Fatalf("Epic commission wrote messages: %+v", messages)
		}
		after, err := e.s.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq {
			t.Fatalf("Epic commission wrote events: %v", err)
		}
	})
	t.Run("question create and replay", func(t *testing.T) {
		e := newQuestionAPIEnv(t, false)
		epicID := createEpicForAPI(t, e.owner, e.project.ID)
		before, err := e.s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		e.createQuestion(t, uuid.NewString(), epicID, e.writerID, e.instance, "not executable", 400)
		if page := e.listQuestions(t, epicID, "?instance_id="+e.instance, 200); len(page.Questions) != 0 {
			t.Fatalf("Epic question wrote rows: %+v", page.Questions)
		}
		key := uuid.NewString()
		question := e.createQuestion(t, key, e.task.ID, e.writerID, e.instance, "replay before conversion", 200)
		convertTaskToEpic(t, e.s, e.task.ID)
		if replay := e.createQuestion(t, key, e.task.ID, e.writerID, e.instance, "replay before conversion", 200); replay != question {
			t.Fatal("exact question receipt did not survive type conversion")
		}
		after, err := e.s.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq+1 {
			t.Fatalf("unexpected question events: %v", err)
		}
	})
	t.Run("question answer", func(t *testing.T) {
		e := newQuestionAPIEnv(t, false)
		question := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "prepared question", 200)
		convertTaskToEpic(t, e.s, e.task.ID)
		before, err := e.s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		e.answerQuestion(t, e.task.ID, question.ID, uuid.NewString(), e.instance, "not executable", 400)
		page := e.listQuestions(t, e.task.ID, "?instance_id="+e.instance+"&ids="+question.ID, 200)
		if len(page.Questions) != 1 || page.Questions[0].Status != "open" || page.Questions[0].AnswerMessageID != "" {
			t.Fatalf("Epic answer wrote question/message: %+v", page.Questions)
		}
		after, err := e.s.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq {
			t.Fatalf("Epic answer wrote events: %v", err)
		}
	})
}

func TestEpicResultSubmitAndAttachGates(t *testing.T) {
	t.Run("submit", func(t *testing.T) {
		f := newAPIResultFixture(t)
		epicID := createEpicForAPI(t, f.h, f.project.ID)
		epicFixture := f
		epicFixture.task.ID = epicID
		before, err := f.store.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		key := uuid.NewString()
		apiSubmit(t, epicFixture, key, apiSubmitInput(""), 409)
		history, err := f.store.TaskResultHistory(t.Context(), epicID, 0, 10)
		if err != nil || len(history.Entries) != 0 {
			t.Fatalf("Epic result wrote history: %+v %v", history, err)
		}
		after, err := f.store.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq {
			t.Fatalf("Epic result wrote events: %v", err)
		}
		// A rejected request must not claim its global mutation key.
		apiSubmit(t, f, key, apiSubmitInput(""), 201)
	})
	t.Run("attach and exact replay", func(t *testing.T) {
		f := newAPIResultFixture(t)
		resultData := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
		var result store.TaskResult
		if err := json.Unmarshal(resultData, &result); err != nil {
			t.Fatal(err)
		}
		input := apiReviewInput(result, "before-epic")
		key := uuid.NewString()
		original := apiAttach(t, f, result.ID, key, input, 201)
		convertTaskToEpic(t, f.store, f.task.ID)
		before, err := f.store.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		fresh := apiReviewInput(result, "fresh-epic")
		freshKey := uuid.NewString()
		apiAttach(t, f, result.ID, freshKey, fresh, 409)
		apiAttach(t, f, result.ID, freshKey, fresh, 409)
		if replay := apiAttach(t, f, result.ID, key, input, 201); !bytes.Equal(replay, original) {
			t.Fatal("exact review receipt did not survive type conversion")
		}
		page, err := f.store.CurrentTaskReviews(t.Context(), f.task.ID, "", 10)
		if err != nil || len(page.Reviews) != 1 || page.Reviews[0].Run != "before-epic" {
			t.Fatalf("Epic attach wrote a review: %+v %v", page, err)
		}
		after, err := f.store.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq {
			t.Fatalf("Epic attach wrote events: %v", err)
		}
	})
}

type testDeliveryReceipt struct {
	MessageID, AttemptID, Type, MessageType, PiSessionID string
	Details                                              map[string]any
}

func prepareDeliveryReceipt(t *testing.T, e *deliveryOwnerEnv) (store.DeliveryMessage, store.DeliveryAttemptResult, string, testDeliveryReceipt) {
	t.Helper()
	message := e.post(t, e.task.ID, uuid.NewString(), "prepared commission", e.binding.ID, e.instance, 200)
	next, receiptKey, err := e.s.DeliveryNext(t.Context(), e.binding.ID, e.instance, e.writerID)
	if err != nil || next.ID != message.ID {
		t.Fatalf("next: %+v %v", next, err)
	}
	attempt, err := e.s.DeliverySpend(t.Context(), e.binding.ID, e.instance, e.writerID, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.DeliveryVerify(t.Context(), e.binding.ID, e.instance, e.writerID, attempt.AttemptID); err != nil {
		t.Fatal(err)
	}
	receipt := testDeliveryReceipt{MessageID: message.ID, AttemptID: attempt.AttemptID, Type: "before_agent_start", MessageType: "custom",
		PiSessionID: e.runtime.PiSessionID, Details: map[string]any{"message_id": message.ID, "attempt_id": attempt.AttemptID}}
	return message, attempt, receiptKey, receipt
}

func TestEpicDeliveryReceiptGateAndReplay(t *testing.T) {
	t.Run("fresh receipt rejected without publication", func(t *testing.T) {
		e := newDeliveryOwnerEnv(t, true)
		message, _, receiptKey, receipt := prepareDeliveryReceipt(t, e)
		convertTaskToEpic(t, e.s, e.task.ID)
		before, err := e.s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writerID, receiptKey, receipt); err == nil {
			t.Fatal("Epic accepted a fresh DeliveryReceipt")
		}
		messages := e.list(t, e.task.ID, "", 200)
		if len(messages) != 1 || messages[0].ID != message.ID || messages[0].Status != "unknown" {
			t.Fatalf("rejected receipt changed message: %+v", messages)
		}
		after, err := e.s.Snapshot(t.Context())
		if err != nil || after.EventSeq != before.EventSeq {
			t.Fatalf("rejected DeliveryReceipt wrote events: %v", err)
		}
	})
	t.Run("exact receipt replay remains available", func(t *testing.T) {
		e := newDeliveryOwnerEnv(t, true)
		_, _, receiptKey, receipt := prepareDeliveryReceipt(t, e)
		original, err := e.s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writerID, receiptKey, receipt)
		if err != nil {
			t.Fatal(err)
		}
		convertTaskToEpic(t, e.s, e.task.ID)
		replay, err := e.s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writerID, receiptKey, receipt)
		if err != nil || replay.ID != original.ID || replay.Status != "observed" {
			t.Fatalf("exact DeliveryReceipt replay: %+v %v", replay, err)
		}
	})
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
