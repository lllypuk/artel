package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/app"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/observerapi"
	"github.com/lllypuk/artel/internal/store"
)

// deliveryOwnerEnv is a bound observer with an active, ready delivery binding
// and one seeded task, exercised through the owner HTTP surface.
type deliveryOwnerEnv struct {
	s             *store.Store
	owner         http.Handler
	bridge        http.Handler
	observer      string
	runtime       store.ObserverRegistration
	project       domain.Project
	task          domain.Task
	binding       store.DeliveryBinding
	deliveryToken string
	writerID      string
	instance      string
}

func newDeliveryOwnerEnv(t *testing.T, active bool) *deliveryOwnerEnv {
	t.Helper()
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	e := &deliveryOwnerEnv{s: s, owner: New(t.Context(), s), bridge: observerapi.New(s), deliveryToken: strings.Repeat("9c", 32), writerID: uuid.NewString()}
	e.project, e.task = seedTask(t, s)
	observerVerifier := sha256Hex(uuid.NewString())
	provisions, err := s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: uuid.NewString(), Key: uuid.NewString(), Action: "provision", Label: "Fictional observer", Verifier: observerVerifier})
	if err != nil {
		t.Fatal(err)
	}
	e.observer = provisions.ObserverID
	e.runtime = store.ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1.5.2", BridgeVersion: "0.1.0", Cwd: "/fictional/project"}
	if _, err = s.RegisterObserver(t.Context(), observerVerifier, e.runtime); err != nil {
		t.Fatal(err)
	}
	bound, err := s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "bind", ExpectedVersion: provisions.Version, RuntimeID: e.runtime.RuntimeID, ProjectID: e.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	e.instance = snapshot.InstanceID
	e.binding, err = s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "enable", ExpectedVer: bound.Version,
		BindingID: uuid.NewString(), JournalID: uuid.NewString(), Verifier: sha256Hex(e.deliveryToken),
		RuntimeID: e.runtime.RuntimeID, ProjectID: e.project.ID, PiSessionID: e.runtime.PiSessionID, InstanceID: e.instance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if active {
		if err = s.DeliveryAttach(t.Context(), e.binding.ID, e.writerID, "", e.binding.JournalID, e.runtime.RuntimeID, e.runtime.PiSessionID); err != nil {
			t.Fatal(err)
		}
		if err = s.DeliveryReady(t.Context(), e.binding.ID, e.writerID); err != nil {
			t.Fatal(err)
		}
		e.binding.Status = "active"
	}
	return e
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func seedTask(t *testing.T, s *store.Store) (domain.Project, domain.Task) {
	t.Helper()
	project := domain.Project{ID: uuid.NewString(), RootPath: "/delivery/" + uuid.NewString(), Name: "fictional", Version: 1, QueueVersion: 1}
	task := domain.Task{ID: uuid.NewString(), ProjectID: project.ID, Title: "fictional", State: domain.Queued, Version: 1}
	_, err := s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Projects = append(snap.Projects, project)
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[project.ID] = []string{task.ID}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: project.ID, TaskID: task.ID, Timestamp: "2026-11-19T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return project, task
}

func (e *deliveryOwnerEnv) post(t *testing.T, taskID, key, text, binding, instance string, status int) store.DeliveryMessage {
	t.Helper()
	body := fmt.Sprintf(`{"text":%q,"delivery_binding_id":%q,"instance_id":%q}`, text, binding, instance)
	r := httptest.NewRequest("POST", "/v1/tasks/"+taskID+"/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	e.owner.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("POST message: got %d want %d: %s", w.Code, status, w.Body.String())
	}
	if status == 200 {
		r.Body = io.NopCloser(strings.NewReader(body))
		checkContract(t, r, w)
	}
	var message store.DeliveryMessage
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &message); err != nil {
			t.Fatal(err)
		}
	}
	return message
}

func (e *deliveryOwnerEnv) list(t *testing.T, taskID, query string, status int) []store.DeliveryMessage {
	t.Helper()
	r := httptest.NewRequest("GET", "/v1/tasks/"+taskID+"/messages"+query, nil)
	w := httptest.NewRecorder()
	e.owner.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("GET messages: got %d want %d: %s", w.Code, status, w.Body.String())
	}
	if status == 200 {
		checkContract(t, r, w)
	}
	var page struct {
		Messages []store.DeliveryMessage `json:"messages"`
	}
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
	}
	return page.Messages
}

func (e *deliveryOwnerEnv) bridgeRequest(t *testing.T, method, path, body string, status int) []byte {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "artel-observer"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+e.deliveryToken)
	w := httptest.NewRecorder()
	e.bridge.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if strings.Contains(w.Body.String(), e.deliveryToken) {
		t.Fatal("credential in response")
	}
	return w.Body.Bytes()
}

func TestOwnerTaskMessageAppendListProjection(t *testing.T) {
	e := newDeliveryOwnerEnv(t, true)
	key := uuid.NewString()
	message := e.post(t, e.task.ID, key, "line one\nline two", e.binding.ID, e.instance, 200)
	if message.Status != "stored" || message.TaskID != e.task.ID || message.Text != "line one\nline two" {
		t.Fatalf("message: %+v", message)
	}
	messages := e.list(t, e.task.ID, "", 200)
	if len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("list: %+v", messages)
	}
	byIDs := e.list(t, e.task.ID, "?ids="+message.ID, 200)
	if len(byIDs) != 1 || byIDs[0].Text != message.Text {
		t.Fatalf("ids reread: %+v", byIDs)
	}
	// The projection reports the exact assignment and writer readiness without
	// tokens, text or writer ownership markers.
	r := httptest.NewRequest("GET", "/v1/tasks/"+e.task.ID+"/delivery", nil)
	w := httptest.NewRecorder()
	e.owner.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	checkContract(t, r, w)
	var projection struct {
		TaskID    string `json:"task_id"`
		ProjectID string `json:"project_id"`
		Ready     bool   `json:"ready"`
		Binding   struct {
			ID         string `json:"delivery_binding_id"`
			Status     string `json:"status"`
			RuntimeID  string `json:"runtime_id"`
			PiSession  string `json:"pi_session_id"`
			WriterID   string `json:"writer_id"`
			Generation int64  `json:"generation"`
		} `json:"binding"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.TaskID != e.task.ID || projection.ProjectID != e.project.ID || !projection.Ready ||
		projection.Binding.ID != e.binding.ID || projection.Binding.Status != "active" ||
		projection.Binding.RuntimeID != e.runtime.RuntimeID || projection.Binding.PiSession != e.runtime.PiSessionID ||
		projection.Binding.WriterID != "" {
		t.Fatalf("projection: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), e.deliveryToken) || strings.Contains(w.Body.String(), e.writerID) {
		t.Fatal("secret or ownership marker in projection")
	}
}

func TestOwnerMessageReplayAfterCloseAndRevoke(t *testing.T) {
	e := newDeliveryOwnerEnv(t, true)
	key := uuid.NewString()
	original := e.post(t, e.task.ID, key, "commission", e.binding.ID, e.instance, 200)
	// Close the task: the irreversible lock must not erase a matched replay.
	setTaskState(t, e.s, e.task.ID, domain.Done)
	replayed := e.post(t, e.task.ID, key, "commission", e.binding.ID, e.instance, 200)
	if replayed.ID != original.ID {
		t.Fatal("replay after close did not return the original commission")
	}
	e.post(t, e.task.ID, uuid.NewString(), "new attempt", e.binding.ID, e.instance, 409)
	if messages := e.list(t, e.task.ID, "", 200); len(messages) != 1 {
		t.Fatalf("closed task accepted new records: %+v", messages)
	}
	// Revocation fences new commissions but never a matched replay in this DB.
	revoke := func() {
		t.Helper()
		if _, err := e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "revoke", BindingID: e.binding.ID}); err != nil {
			t.Fatal(err)
		}
	}
	revoke()
	replayed = e.post(t, e.task.ID, key, "commission", e.binding.ID, e.instance, 200)
	if replayed.ID != original.ID {
		t.Fatal("replay after revoke did not return the original commission")
	}
	e.post(t, e.task.ID, uuid.NewString(), "after revoke", e.binding.ID, e.instance, 409)
}

func TestOwnerMessageStaleInstanceRefusedWithoutReplayOrRecord(t *testing.T) {
	e := newDeliveryOwnerEnv(t, true)
	key := uuid.NewString()
	original := e.post(t, e.task.ID, key, "pinned", e.binding.ID, e.instance, 200)
	staleInstance := uuid.NewString()
	e.post(t, e.task.ID, key, "pinned", e.binding.ID, staleInstance, 409)
	e.post(t, e.task.ID, uuid.NewString(), "other", e.binding.ID, staleInstance, 409)
	messages := e.list(t, e.task.ID, "", 200)
	if len(messages) != 1 || messages[0].ID != original.ID {
		t.Fatalf("stale instance created or replayed records: %+v", messages)
	}
}

func TestOwnerPendingBindingRejectsPostAndRevokes(t *testing.T) {
	e := newDeliveryOwnerEnv(t, false)
	if e.binding.Status != "pending" {
		t.Fatalf("binding: %+v", e.binding)
	}
	e.post(t, e.task.ID, uuid.NewString(), "too early", e.binding.ID, e.instance, 409)
	if messages := e.list(t, e.task.ID, "", 200); len(messages) != 0 {
		t.Fatalf("pending binding accepted a record: %+v", messages)
	}
	// A pending grant is revocable; the writer token is fenced immediately.
	if _, err := e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "revoke", BindingID: e.binding.ID}); err != nil {
		t.Fatal(err)
	}
	e.bridgeRequest(t, "GET", "/delivery/v1/context", "", 401)
	binding, err := e.s.DeliveryBindingStatus(t.Context(), e.observer, e.binding.ID)
	if err != nil || binding.Status != "revoked" {
		t.Fatalf("pending revoke: %+v %v", binding, err)
	}
}

func TestBridgeSpendReplayGrantsNoRight(t *testing.T) {
	e := newDeliveryOwnerEnv(t, true)
	e.post(t, e.task.ID, uuid.NewString(), "deliver me", e.binding.ID, e.instance, 200)
	next := e.bridgeRequest(t, "GET", "/delivery/v1/next?writer_id="+e.writerID, "", 200)
	var envelope struct {
		Message *struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal(next, &envelope); err != nil || envelope.Message == nil {
		t.Fatalf("next: %s", next)
	}
	spend := func() store.DeliveryAttemptResult {
		t.Helper()
		body := fmt.Sprintf(`{"writer_id":%q,"message_id":%q}`, e.writerID, envelope.Message.ID)
		raw := e.bridgeRequest(t, "POST", "/delivery/v1/spend", body, 200)
		var attempt store.DeliveryAttemptResult
		if err := json.Unmarshal(raw, &attempt); err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	first := spend()
	if !first.Right || first.AttemptID == "" {
		t.Fatalf("first spend: %+v", first)
	}
	// Any replay or state read reports the attempt without ever returning the
	// right again; only the first committing spend carried it.
	replay := spend()
	if replay.Right || replay.AttemptID != first.AttemptID || replay.State != "spent" {
		t.Fatalf("spend replay granted or altered a right: %+v", replay)
	}
	verifyBody := fmt.Sprintf(`{"writer_id":%q,"attempt_id":%q}`, e.writerID, first.AttemptID)
	verified := e.bridgeRequest(t, "POST", "/delivery/v1/verify", verifyBody, 200)
	if bytes.Contains(verified, []byte(`"right":true`)) {
		t.Fatal("verify returned a right")
	}
	// The same message is never spendable again even by the owning writer.
	again := spend()
	if again.Right {
		t.Fatal("second spend returned a right")
	}
}

func setTaskState(t *testing.T, s *store.Store, taskID string, state domain.State) {
	t.Helper()
	service := app.New(s)
	transition := func(state domain.State, version, queueVersion int64) {
		t.Helper()
		_, err := service.Execute(t.Context(), uuid.NewString(), "POST", "/v1/tasks/"+taskID+"/transition",
			app.Command{Action: app.Transition, TaskID: taskID, State: state, ExpectedVersion: version, ExpectedQueueVersion: queueVersion})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Queued -> in_progress -> done exercises the real manual transition path.
	transition(domain.InProgress, 1, 1)
	transition(state, 2, 2)
}

func TestOwnerEnableMissingVersionAndBridgeNextWriterFence(t *testing.T) {
	e := newDeliveryOwnerEnv(t, false)
	// Missing expected_version is a 400 with a public error, never an empty 200.
	body := fmt.Sprintf(`{"delivery_binding_id":%q,"journal_id":%q,"verifier":%q,"runtime_id":%q,"project_id":%q,"pi_session_id":%q,"instance_id":%q}`,
		e.binding.ID, e.binding.JournalID, uuid.NewString(), e.runtime.RuntimeID, e.project.ID, e.runtime.PiSessionID, e.instance)
	r := httptest.NewRequest("PUT", "/v1/observers/"+e.observer+"/delivery", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	e.owner.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") {
		t.Fatalf("missing version: %d %s", w.Code, w.Body.String())
	}
	// The projection without any binding validates against the contract.
	s2, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "plain.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	p2, task2 := seedTask(t, s2)
	owner2 := New(t.Context(), s2)
	r = httptest.NewRequest("GET", "/v1/tasks/"+task2.ID+"/delivery", nil)
	w = httptest.NewRecorder()
	owner2.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("projection: %d %s", w.Code, w.Body.String())
	}
	checkContract(t, r, w)
	if !strings.Contains(w.Body.String(), `"binding":null`) {
		t.Fatalf("disabled projection: %s", w.Body.String())
	}
	_ = p2
	// next under a foreign writer id is fenced even before any activation.
	e.bridgeRequest(t, "GET", "/delivery/v1/next?writer_id="+uuid.NewString(), "", 409)
}
