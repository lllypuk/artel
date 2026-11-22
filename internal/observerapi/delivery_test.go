package observerapi_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

// deliveryChannelFixture provisions one bound observer with an enabled pending
// delivery binding and returns the synthetic channel tokens.
type deliveryChannelFixture struct {
	*fixture
	runtime       store.ObserverRegistration
	bindingID     string
	deliveryToken string
	projectID     string
}

func newDeliveryChannelFixture(t *testing.T) *deliveryChannelFixture {
	t.Helper()
	f := newFixture(t)
	d := &deliveryChannelFixture{fixture: f, deliveryToken: strings.Repeat("7b", 32)}
	r := reg()
	d.runtime = r
	d.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	d.projectID = seedProject(t, f.s)
	d.request(t, false, "PATCH", "/v1/observers/"+f.id+"/binding", f.ownerToken, uuid.NewString(), `{"expected_version":1,"runtime_id":"`+r.RuntimeID+`","project_id":"`+d.projectID+`"}`, 200)
	d.bindingID = uuid.NewString()
	enable := `{"expected_version":2,"delivery_binding_id":"` + d.bindingID + `","journal_id":"` + uuid.NewString() +
		`","verifier":"` + local.TokenVerifier(d.deliveryToken) + `","runtime_id":"` + r.RuntimeID +
		`","project_id":"` + d.projectID + `","pi_session_id":"` + r.PiSessionID + `","instance_id":"` + d.instanceID(t) + `"}`
	d.exchange(t, false, "PUT", "/v1/observers/"+f.id+"/delivery", f.ownerToken, enable, map[string]string{"Idempotency-Key": uuid.NewString()}, 200)
	return d
}

// exchange fires one raw request at the chosen surface and asserts the status,
// without leaking credentials into failures.
func (d *deliveryChannelFixture) exchange(t *testing.T, observer bool, method, path, token, body string, headers map[string]string, status int) string {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Host = "127.0.0.1:7331"
	handler := d.owner
	if observer {
		r.Host = "artel-observer"
		handler = d.observer
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		if strings.EqualFold(name, "Host") {
			r.Host = value
			continue
		}
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	response := w.Body.String()
	for _, secret := range []string{d.deliveryToken, d.token, d.ownerToken, local.TokenVerifier(d.deliveryToken)} {
		if strings.Contains(response, secret) {
			t.Fatal("credential in response")
		}
	}
	return response
}

func TestDeliveryRouteSeparationMatrix(t *testing.T) {
	d := newDeliveryChannelFixture(t)
	contextQuery := "/delivery/v1/context"
	observerContext := "/observer/v1/context?runtime_id=" + d.runtime.RuntimeID
	registerBody, _ := json.Marshal(reg())
	// Positive controls: each token works only on its own channel.
	d.exchange(t, true, "GET", contextQuery, d.deliveryToken, "", nil, 200)
	d.exchange(t, true, "GET", observerContext, d.token, "", nil, 200)
	// Observer token must not use delivery routes; delivery token must not use
	// observer routes; the owner token is valid on neither bridge channel.
	for _, path := range []string{contextQuery, "/delivery/v1/next"} {
		d.exchange(t, true, "GET", path, d.token, "", nil, 401)
		d.exchange(t, true, "GET", path, d.ownerToken, "", nil, 401)
	}
	d.exchange(t, true, "POST", "/delivery/v1/attach", d.token, `{}`, nil, 401)
	for _, path := range []string{observerContext, "/observer/v1/register", "/observer/v1/report"} {
		method, body := "GET", ""
		if strings.Contains(path, "register") {
			method, body = "POST", string(registerBody)
		}
		if strings.Contains(path, "report") {
			method, body = "PUT", `{"runtime_id":"`+d.runtime.RuntimeID+`","generation":1,"report_seq":1,"activity":"idle","tool_count":0,"lifecycle":"running"}`
		}
		d.exchange(t, true, method, path, d.deliveryToken, body, nil, 401)
	}
	// Bridge routes do not exist on the owner TCP surface and owner routes do
	// not exist on the private Unix socket, even with valid synthetic tokens.
	d.exchange(t, false, "GET", contextQuery, d.deliveryToken, "", nil, 404)
	d.exchange(t, false, "POST", "/delivery/v1/spend", d.deliveryToken, `{"writer_id":"`+uuid.NewString()+`","message_id":"`+uuid.NewString()+`"}`, nil, 404)
	// Owner routes have no channel on the Unix socket; the transport rejects the
	// foreign token before any owner handler runs.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/observers", ""},
		{"PUT", "/v1/observers/" + d.id + "/delivery", `{}`},
		{"GET", "/v1/snapshot", ""},
	} {
		d.exchange(t, true, tc.method, tc.path, d.ownerToken, tc.body, nil, 401)
	}
	// Transport filter applies to the delivery channel before authentication
	// succeeds: forged Host or any Origin is rejected with a valid token.
	d.exchange(t, true, "GET", contextQuery, d.deliveryToken, "", map[string]string{"Host": "localhost"}, 400)
	d.exchange(t, true, "GET", contextQuery, d.deliveryToken, "", map[string]string{"Origin": "http://evil"}, 400)
	// Revocation immediately fences the delivery token on the shared socket.
	d.exchange(t, false, "POST", "/v1/observers/"+d.id+"/delivery/revoke", d.ownerToken, `{"delivery_binding_id":"`+d.bindingID+`"}`, map[string]string{"Idempotency-Key": uuid.NewString()}, 200)
	d.exchange(t, true, "GET", contextQuery, d.deliveryToken, "", nil, 401)
	d.exchange(t, true, "GET", observerContext, d.token, "", nil, 200)
}

func (d *deliveryChannelFixture) instanceID(t *testing.T) string {
	t.Helper()
	registry, err := d.s.Observers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return registry.DatabaseInstanceID
}

func seedProject(t *testing.T, s *store.Store) string {
	t.Helper()
	id := uuid.NewString()
	project := domain.Project{ID: id, RootPath: "/delivery/" + uuid.NewString(), Name: "fictional", Version: 1, QueueVersion: 1}
	_, err := s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Projects = append(snap.Projects, project)
		snap.Queues[id] = []string{}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: id, Timestamp: "2026-11-19T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedDeliveryChannelTask(t *testing.T, d *deliveryChannelFixture) domain.Task {
	t.Helper()
	task := domain.Task{ID: uuid.NewString(), ProjectID: d.projectID, Title: "fictional delivery work", State: domain.Backlog, Version: 1}
	_, err := d.s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Tasks = append(snap.Tasks, task)
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: d.projectID, TaskID: task.ID, Timestamp: "2026-11-22T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// setDeliveryChannelTaskKind intentionally creates a stale-client fixture after
// delivery rows exist. It is not a production type-conversion operation.
func setDeliveryChannelTaskKind(t *testing.T, d *deliveryChannelFixture, taskID string, kind domain.Kind) {
	t.Helper()
	_, err := d.s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		for i := range snap.Tasks {
			if snap.Tasks[i].ID == taskID {
				snap.Tasks[i].Kind = kind
				snap.Tasks[i].State = domain.Backlog
				if kind == domain.KindEpic {
					snap.Tasks[i].State = domain.Open
				}
			}
		}
		return store.Result{Status: 200, Body: []byte(`{}`)}, &store.Event{Action: "test.work_type_fixture", ProjectID: d.projectID, TaskID: taskID, Timestamp: "2026-11-22T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryHTTPEpicFixtureRejectsStaleOwnerAndWriterOperations(t *testing.T) {
	d := newDeliveryChannelFixture(t)
	task := seedDeliveryChannelTask(t, d)
	writer := uuid.NewString()
	binding, err := d.s.DeliveryBindingStatus(t.Context(), d.id, d.bindingID)
	if err != nil {
		t.Fatal(err)
	}
	d.exchange(t, true, http.MethodPost, "/delivery/v1/attach", d.deliveryToken, fmt.Sprintf(
		`{"writer_id":%q,"journal_id":%q,"runtime_id":%q,"pi_session_id":%q}`,
		writer, binding.JournalID, d.runtime.RuntimeID, d.runtime.PiSessionID), nil, 200)
	d.exchange(t, true, http.MethodPost, "/delivery/v1/ready", d.deliveryToken,
		fmt.Sprintf(`{"writer_id":%q}`, writer), nil, 200)
	appendMessage := func(text string) store.DeliveryMessage {
		t.Helper()
		message, err := d.s.AppendDeliveryMessage(t.Context(), task.ID, store.DeliveryAppend{
			Key: uuid.NewString(), Text: text, BindingID: d.bindingID, InstanceID: d.instanceID(t),
		})
		if err != nil {
			t.Fatal(err)
		}
		return message
	}
	next := func(messageID string) string {
		t.Helper()
		message, key, err := d.s.DeliveryNext(t.Context(), d.bindingID, "", writer)
		if err != nil || message.ID != messageID || key == "" {
			t.Fatalf("fixture next: %+v %q %v", message, key, err)
		}
		return key
	}
	spend := func(messageID string) store.DeliveryAttemptResult {
		t.Helper()
		attempt, err := d.s.DeliverySpend(t.Context(), d.bindingID, "", writer, messageID)
		if err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	verify := func(attempt store.DeliveryAttemptResult) {
		t.Helper()
		if _, err := d.s.DeliveryVerify(t.Context(), d.bindingID, "", writer, attempt.AttemptID); err != nil {
			t.Fatal(err)
		}
	}
	receipt := func(messageID, attemptID string) struct {
		MessageID, AttemptID, Type, MessageType, PiSessionID string
		Details                                              map[string]any
	} {
		return struct {
			MessageID, AttemptID, Type, MessageType, PiSessionID string
			Details                                              map[string]any
		}{messageID, attemptID, "before_agent_start", "custom", d.runtime.PiSessionID,
			map[string]any{"message_id": messageID, "attempt_id": attemptID}}
	}

	observedMessage := appendMessage("observed fixture")
	observedKey := next(observedMessage.ID)
	observedAttempt := spend(observedMessage.ID)
	verify(observedAttempt)
	if _, err := d.s.DeliveryReceipt(t.Context(), d.bindingID, "", "", observedKey, receipt(observedMessage.ID, observedAttempt.AttemptID)); err != nil {
		t.Fatal(err)
	}
	verifiedMessage := appendMessage("verified fixture")
	verifiedKey := next(verifiedMessage.ID)
	verifiedAttempt := spend(verifiedMessage.ID)
	verify(verifiedAttempt)
	spentMessage := appendMessage("spent fixture")
	spentAttempt := spend(spentMessage.ID)
	pendingMessage := appendMessage("pending fixture")
	setDeliveryChannelTaskKind(t, d, task.ID, domain.KindEpic)
	beforeMessages, _, err := d.s.ListDeliveryMessages(t.Context(), task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := d.s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	d.exchange(t, false, http.MethodPost, "/v1/tasks/"+task.ID+"/messages", d.ownerToken, fmt.Sprintf(
		`{"text":"stale owner commission","delivery_binding_id":%q,"instance_id":%q}`, d.bindingID, d.instanceID(t)),
		map[string]string{"Idempotency-Key": uuid.NewString()}, 400)
	d.exchange(t, true, http.MethodGet, "/delivery/v1/next?writer_id="+writer, d.deliveryToken, "", nil, 400)
	for _, messageID := range []string{pendingMessage.ID, spentMessage.ID} {
		d.exchange(t, true, http.MethodPost, "/delivery/v1/spend", d.deliveryToken,
			fmt.Sprintf(`{"writer_id":%q,"message_id":%q}`, writer, messageID), nil, 400)
	}
	for _, attemptID := range []string{spentAttempt.AttemptID, verifiedAttempt.AttemptID} {
		d.exchange(t, true, http.MethodPost, "/delivery/v1/verify", d.deliveryToken,
			fmt.Sprintf(`{"writer_id":%q,"attempt_id":%q}`, writer, attemptID), nil, 400)
	}
	receiptRequest := func(key, messageID, attemptID string) string {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"key": key, "writer_id": writer, "message_id": messageID, "attempt_id": attemptID,
			"type": "before_agent_start", "message_type": "custom", "pi_session_id": d.runtime.PiSessionID,
			"details": map[string]any{"message_id": messageID, "attempt_id": attemptID},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	d.exchange(t, true, http.MethodPost, "/delivery/v1/receipt", d.deliveryToken,
		receiptRequest(verifiedKey, verifiedMessage.ID, verifiedAttempt.AttemptID), nil, 400)
	if got := d.exchange(t, true, http.MethodPost, "/delivery/v1/receipt", d.deliveryToken,
		receiptRequest(observedKey, observedMessage.ID, observedAttempt.AttemptID), nil, 200); !strings.Contains(got, `"status":"observed"`) {
		t.Fatalf("observed receipt recovery: %s", got)
	}
	afterMessages, _, err := d.s.ListDeliveryMessages(t.Context(), task.ID, 0, 50, nil)
	if err != nil || !reflect.DeepEqual(afterMessages, beforeMessages) {
		t.Fatalf("Epic HTTP operations changed delivery history: before=%+v after=%+v err=%v", beforeMessages, afterMessages, err)
	}
	afterEvents, err := d.s.Events(t.Context())
	if err != nil || !reflect.DeepEqual(afterEvents, beforeEvents) {
		t.Fatalf("Epic HTTP operations wrote events: %v", err)
	}
}
