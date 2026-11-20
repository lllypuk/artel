package observerapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/observerapi"
	"github.com/lllypuk/artel/internal/store"
)

type questionChannelFixture struct {
	*deliveryChannelFixture
	writerID string
	taskID   string
}

func newQuestionChannelFixture(t *testing.T) *questionChannelFixture {
	t.Helper()
	f := newFixture(t)
	d := &questionChannelFixture{deliveryChannelFixture: &deliveryChannelFixture{fixture: f, deliveryToken: strings.Repeat("6a", 32)}, writerID: uuid.NewString()}
	runtime := reg()
	d.runtime = runtime
	d.request(t, true, http.MethodPost, "/observer/v1/register", f.token, "", runtime, 200)
	d.projectID = seedProject(t, f.s)
	d.taskID = uuid.NewString()
	task := domain.Task{ID: d.taskID, ProjectID: d.projectID, Title: "fictional question task", State: domain.Backlog, Version: 1}
	_, err := f.s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[d.projectID] = []string{task.ID}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: d.projectID, TaskID: task.ID, Timestamp: "2026-11-20T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.request(t, false, http.MethodPatch, "/v1/observers/"+f.id+"/binding", f.ownerToken, uuid.NewString(),
		`{"expected_version":1,"runtime_id":"`+runtime.RuntimeID+`","project_id":"`+d.projectID+`"}`, 200)
	d.bindingID = uuid.NewString()
	enable := `{"expected_version":2,"delivery_binding_id":"` + d.bindingID + `","journal_id":"` + uuid.NewString() +
		`","verifier":"` + local.TokenVerifier(d.deliveryToken) + `","runtime_id":"` + runtime.RuntimeID +
		`","project_id":"` + d.projectID + `","pi_session_id":"` + runtime.PiSessionID + `","instance_id":"` + d.instanceID(t) +
		`","allow_owner_questions":true}`
	d.exchange(t, false, http.MethodPut, "/v1/observers/"+f.id+"/delivery", f.ownerToken, enable,
		map[string]string{"Idempotency-Key": uuid.NewString()}, 200)
	attach := `{"writer_id":"` + d.writerID + `","journal_id":"` + questionJournalID(t, f.s, d.bindingID) +
		`","runtime_id":"` + runtime.RuntimeID + `","pi_session_id":"` + runtime.PiSessionID +
		`","capability":"` + store.DeliveryOwnerQuestionsCapability + `","instance_id":"` + d.instanceID(t) + `"}`
	d.exchange(t, true, http.MethodPost, "/delivery/v1/attach", d.deliveryToken, attach, nil, 200)
	return d
}

func questionJournalID(t *testing.T, s *store.Store, bindingID string) string {
	t.Helper()
	registry, err := s.Observers(t.Context())
	if err != nil || len(registry.Observers) != 1 {
		t.Fatalf("registry: %v", err)
	}
	// DeliveryContext is delivery-authenticated at HTTP; this test fixture reads
	// the non-secret journal ID through the owner status projection instead.
	status, err := s.DeliveryBindingStatus(t.Context(), registry.Observers[0].ObserverID, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	return status.JournalID
}

func (d *questionChannelFixture) create(t *testing.T, key, token, writer, instance, task, text string, status int) store.OwnerQuestionCreateResult {
	t.Helper()
	bodyBytes, err := json.Marshal(map[string]string{"instance_id": instance, "task_id": task, "writer_id": writer, "text": text})
	if err != nil {
		t.Fatal(err)
	}
	raw := d.exchange(t, true, http.MethodPost, "/delivery/v1/questions", token, string(bodyBytes),
		map[string]string{"Idempotency-Key": key}, status)
	var result store.OwnerQuestionCreateResult
	if status == 200 {
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestDeliveryQuestionChannelAuthenticationReplayAndStrictJSON(t *testing.T) {
	d := newQuestionChannelFixture(t)
	instance := d.instanceID(t)
	key := uuid.NewString()
	first := d.create(t, key, d.deliveryToken, d.writerID, instance, d.taskID, "question", 200)
	replay := d.create(t, key, d.deliveryToken, d.writerID, instance, d.taskID, "question", 200)
	if replay != first {
		t.Fatalf("replay: %+v %+v", first, replay)
	}
	d.create(t, key, d.deliveryToken, d.writerID, instance, d.taskID, "changed", 409)
	d.create(t, uuid.NewString(), d.deliveryToken, uuid.NewString(), instance, d.taskID, "foreign writer", 409)
	d.create(t, uuid.NewString(), d.deliveryToken, d.writerID, uuid.NewString(), d.taskID, "foreign instance", 409)
	d.create(t, uuid.NewString(), d.deliveryToken, d.writerID, instance, uuid.NewString(), "foreign task", 404)
	for _, token := range []string{d.token, d.ownerToken, strings.Repeat("ef", 32)} {
		d.create(t, uuid.NewString(), token, d.writerID, instance, d.taskID, "wrong channel", 401)
	}
	for _, key := range []string{"", "not-a-uuid"} {
		d.create(t, key, d.deliveryToken, d.writerID, instance, d.taskID, "bad key", 400)
	}
	valid := `{"instance_id":"` + instance + `","task_id":"` + d.taskID + `","writer_id":"` + d.writerID + `","text":"ok"}`
	for _, body := range []string{
		"", "null", "[]", `{}`, valid + `{}`,
		strings.Replace(valid, `"text":"ok"`, `"text":null`, 1),
		strings.Replace(valid, `"text":"ok"`, `"text":"ok","text":"duplicate"`, 1),
		strings.Replace(valid, `"text":"ok"`, `"text":"ok","metadata":{"nested":true}`, 1),
		strings.Replace(valid, `"text":"ok"`, `"TEXT":"ok"`, 1),
	} {
		d.exchange(t, true, http.MethodPost, "/delivery/v1/questions", d.deliveryToken, body,
			map[string]string{"Idempotency-Key": uuid.NewString()}, 400)
	}
	// Owner routes are not mounted on the UDS, and the delivery route is not
	// mounted on the owner listener.
	d.exchange(t, true, http.MethodPost, "/v1/tasks/"+d.taskID+"/questions/"+first.ID+"/answer", d.deliveryToken,
		`{"instance_id":"`+instance+`","text":"no"}`, map[string]string{"Idempotency-Key": uuid.NewString()}, 401)
	d.exchange(t, false, http.MethodPost, "/delivery/v1/questions", d.deliveryToken, valid,
		map[string]string{"Idempotency-Key": uuid.NewString()}, 404)
	registry := d.exchange(t, false, http.MethodGet, "/v1/observers", d.ownerToken, "", nil, 200)
	if strings.Contains(registry, "question") {
		t.Fatal("question text leaked into observer registry")
	}
}

func TestDeliveryQuestionExactJSONUnicode(t *testing.T) {
	d := newQuestionChannelFixture(t)
	instance, key := d.instanceID(t), uuid.NewString()
	post := func(raw string, status int) string {
		return d.exchange(t, true, http.MethodPost, "/delivery/v1/questions", d.deliveryToken,
			fmt.Sprintf(`{"instance_id":%q,"task_id":%q,"writer_id":%q,"text":%s}`, instance, d.taskID, d.writerID, raw),
			map[string]string{"Idempotency-Key": key}, status)
	}
	for _, malformed := range []string{`"\ud800"`, `"\udc00"`, `"\ud800x"`, `"\ud800\u0041"`} {
		post(malformed, 400)
	}
	ack := post(`"pair=\ud83d\ude00 replacement=� slash=\\ud800"`, 200)
	if replay := post(`"pair=😀 replacement=\ufffd slash=\\ud800"`, 200); replay != ack {
		t.Fatal("equivalent valid Unicode did not replay")
	}
	rows, _, err := d.s.ListOwnerQuestions(t.Context(), d.taskID, instance, 0, 5, nil)
	if err != nil || len(rows) != 1 || rows[0].Text != `pair=😀 replacement=� slash=\ud800` {
		t.Fatalf("question text changed: %+v %v", rows, err)
	}
}

func TestDeliveryQuestionRequiresOptIn(t *testing.T) {
	d := newDeliveryChannelFixture(t)
	writer := uuid.NewString()
	status, err := d.s.DeliveryBindingStatus(t.Context(), d.id, d.bindingID)
	if err != nil {
		t.Fatal(err)
	}
	attach := fmt.Sprintf(`{"writer_id":%q,"journal_id":%q,"runtime_id":%q,"pi_session_id":%q}`,
		writer, status.JournalID, d.runtime.RuntimeID, d.runtime.PiSessionID)
	d.exchange(t, true, http.MethodPost, "/delivery/v1/attach", d.deliveryToken, attach, nil, 200)
	taskID := uuid.NewString()
	task := domain.Task{ID: taskID, ProjectID: d.projectID, Title: "plain grant", State: domain.Backlog, Version: 1}
	_, err = d.s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Tasks = append(snap.Tasks, task)
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: d.projectID, TaskID: taskID, Timestamp: "2026-11-20T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"instance_id":%q,"task_id":%q,"writer_id":%q,"text":"disabled"}`,
		d.instanceID(t), taskID, writer)
	d.exchange(t, true, http.MethodPost, "/delivery/v1/questions", d.deliveryToken, body,
		map[string]string{"Idempotency-Key": uuid.NewString()}, 409)
}

func questionUDSClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "observer.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}

func postQuestionUDS(t *testing.T, client *http.Client, body []byte, token, key string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://artel-observer/delivery/v1/questions", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", key)
	// #nosec G704 -- Fixed host on this test's bounded private Unix-socket transport; no proxy or redirects.
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data
}

func nextQuestionUDS(t *testing.T, client *http.Client, token, writerID, instance string) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://artel-observer/delivery/v1/next?writer_id="+writerID+"&instance_id="+instance, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	// #nosec G704 -- Fixed host on this test's bounded private Unix-socket transport; no proxy or redirects.
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("next: %d %s %v", response.StatusCode, body, err)
	}
	if len(body) > observerapi.MaxDeliveryBody {
		t.Fatalf("next exceeds wire limit: %d", len(body))
	}
	return body
}

func TestDeliveryQuestionRealUDSWireLimitsAndNext(t *testing.T) {
	d := newQuestionChannelFixture(t)
	client := questionUDSClient(t, d.observer)
	post := func(body []byte, token, key string) (int, []byte) {
		return postQuestionUDS(t, client, body, token, key)
	}
	instance := d.instanceID(t)
	questionText := strings.Repeat("\x01", 4096)
	body, err := json.Marshal(map[string]string{"instance_id": instance, "task_id": d.taskID, "writer_id": d.writerID, "text": questionText})
	if err != nil {
		t.Fatal(err)
	}
	if len(body) >= 32768 {
		t.Fatalf("maximum escaped question unexpectedly exceeds body limit: %d", len(body))
	}
	status, response := post(body, d.deliveryToken, uuid.NewString())
	if status != 200 {
		t.Fatalf("escaped boundary: %d %s", status, response)
	}
	var question store.OwnerQuestionCreateResult
	if err := json.Unmarshal(response, &question); err != nil {
		t.Fatal(err)
	}
	answerText := strings.Repeat("\x02", 4096)
	answer, err := d.s.AnswerOwnerQuestion(t.Context(), d.taskID, question.ID, store.OwnerQuestionAnswer{
		Key: uuid.NewString(), InstanceID: instance, Text: answerText,
	})
	if err != nil {
		t.Fatal(err)
	}
	nextBody := nextQuestionUDS(t, client, d.deliveryToken, d.writerID, instance)
	var next struct {
		Message store.DeliveryMessage `json:"message"`
	}
	if err := json.Unmarshal(nextBody, &next); err != nil || next.Message.ID != answer.MessageID ||
		next.Message.Kind != "answer" || next.Message.QuestionID != question.ID ||
		next.Message.QuestionText != questionText || next.Message.Text != answerText {
		t.Fatalf("answer next context: %+v %v", next.Message, err)
	}
	oversize, err := json.Marshal(map[string]string{"instance_id": instance, "task_id": d.taskID, "writer_id": d.writerID,
		"text": "x", "padding": strings.Repeat("x", 32768)})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := post(oversize, d.deliveryToken, uuid.NewString()); status != 413 {
		t.Fatalf("oversize UDS body: %d", status)
	}
	// Authentication runs before the body filter, even for an oversize body.
	if status, _ := post(oversize, d.token, uuid.NewString()); status != 401 {
		t.Fatalf("oversize unauthenticated body: %d", status)
	}
	questions, _, err := d.s.ListOwnerQuestions(t.Context(), d.taskID, instance, 0, 5, nil)
	if err != nil || len(questions) != 1 {
		t.Fatalf("oversize request wrote question: %+v %v", questions, err)
	}
}

func TestDeliveryQuestionSwappedDatabaseDoesNotWriteOrReplay(t *testing.T) {
	original := newQuestionChannelFixture(t)
	instance := original.instanceID(t)
	key := uuid.NewString()
	question := original.create(t, key, original.deliveryToken, original.writerID, instance, original.taskID, "original", 200)
	replacementStore, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "replacement.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacementStore.Close() })
	project := domain.Project{ID: original.projectID, RootPath: "/replacement/" + uuid.NewString(), Name: "replacement", Version: 1, QueueVersion: 1}
	task := domain.Task{ID: original.taskID, ProjectID: project.ID, Title: "replacement", State: domain.Backlog, Version: 1}
	_, err = replacementStore.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Projects = append(snap.Projects, project)
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[project.ID] = []string{task.ID}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: project.ID, TaskID: task.ID, Timestamp: "2026-11-20T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	provisioned, err := replacementStore.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: original.id,
		Key: uuid.NewString(), Action: "provision", Label: "replacement", Verifier: local.TokenVerifier(original.token)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replacementStore.RegisterObserver(t.Context(), local.TokenVerifier(original.token), original.runtime); err != nil {
		t.Fatal(err)
	}
	bound, err := replacementStore.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: original.id, Key: uuid.NewString(),
		Action: "bind", ExpectedVersion: provisioned.Version, RuntimeID: original.runtime.RuntimeID, ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	oldBinding, err := original.s.DeliveryBindingStatus(t.Context(), original.id, original.bindingID)
	if err != nil {
		t.Fatal(err)
	}
	replacementSnapshot, err := replacementStore.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = replacementStore.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{ObserverID: original.id,
		Key: uuid.NewString(), Action: "enable", ExpectedVer: bound.Version, BindingID: original.bindingID,
		JournalID: oldBinding.JournalID, Verifier: local.TokenVerifier(original.deliveryToken), RuntimeID: original.runtime.RuntimeID,
		ProjectID: project.ID, PiSessionID: original.runtime.PiSessionID, InstanceID: replacementSnapshot.InstanceID, AllowOwnerQuestions: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := replacementStore.DeliveryAttach(t.Context(), original.bindingID, replacementSnapshot.InstanceID, original.writerID, "", oldBinding.JournalID,
		original.runtime.RuntimeID, original.runtime.PiSessionID, store.DeliveryOwnerQuestionsCapability); err != nil {
		t.Fatal(err)
	}
	// Endpoint, credential, binding, writer and task now match. Only the pinned
	// database identity differs, so the old request cannot write or replay.
	replacementHandler := observerapi.New(replacementStore)
	r := httptest.NewRequest(http.MethodPost, "/delivery/v1/questions", strings.NewReader(fmt.Sprintf(
		`{"instance_id":%q,"task_id":%q,"writer_id":%q,"text":"original"}`, instance, original.taskID, original.writerID)))
	r.Host = "artel-observer"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+original.deliveryToken)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	replacementHandler.ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "instance_mismatch") {
		t.Fatalf("replacement accepted old instance: %d %s", w.Code, w.Body.String())
	}
	questions, _, err := replacementStore.ListOwnerQuestions(t.Context(), original.taskID, replacementSnapshot.InstanceID, 0, 5, nil)
	if err != nil || len(questions) != 0 {
		t.Fatalf("replacement wrote or replayed question: %+v %v", questions, err)
	}
	_ = question
	assertReplacementAnswerChain(t, original, replacementStore, replacementSnapshot.InstanceID, instance)
}

// This handler represents the replacement daemon after the original writer's
// context check. Each stage must check the pin independently on the wire.
func assertReplacementAnswerChain(t *testing.T, d *questionChannelFixture, replacement *store.Store, instance, foreign string) {
	t.Helper()
	copyFixture := *d.fixture
	copyFixture.observer = observerapi.New(replacement)
	channel := &deliveryChannelFixture{fixture: &copyFixture, deliveryToken: d.deliveryToken}
	call := func(method, route, pin string, body map[string]any, status int) string {
		t.Helper()
		path := "/delivery/v1/" + route
		var data []byte
		if method == http.MethodGet {
			path += "?writer_id=" + d.writerID + "&instance_id=" + pin
		} else {
			body["instance_id"], body["writer_id"] = pin, d.writerID
			data, _ = json.Marshal(body)
		}
		return channel.exchange(t, true, method, path, d.deliveryToken, string(data), nil, status)
	}
	check := func(method, route string, body map[string]any) string {
		t.Helper()
		if denied := call(method, route, foreign, body, 409); denied != "{\"error\":\"instance_mismatch\"}\n" {
			t.Fatalf("%s did not fence the old database: %s", route, denied)
		}
		return call(method, route, instance, body, 200)
	}
	q, err := replacement.CreateOwnerQuestion(t.Context(), d.bindingID, store.OwnerQuestionCreate{
		Key: uuid.NewString(), InstanceID: instance, TaskID: d.taskID, WriterID: d.writerID, Text: "Replacement private question",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := replacement.AnswerOwnerQuestion(t.Context(), d.taskID, q.ID, store.OwnerQuestionAnswer{
		Key: uuid.NewString(), InstanceID: instance, Text: "Replacement private answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	var next struct {
		Message store.DeliveryMessage `json:"message"`
		Key     string                `json:"receipt_key"`
	}
	if err := json.Unmarshal([]byte(check(http.MethodGet, "next", nil)), &next); err != nil || next.Message.ID != a.MessageID || next.Message.Text != "Replacement private answer" {
		t.Fatalf("replacement next: %+v %v", next, err)
	}
	var attempt store.DeliveryAttemptResult
	if err := json.Unmarshal([]byte(check(http.MethodPost, "spend", map[string]any{"message_id": a.MessageID})), &attempt); err != nil || !attempt.Right {
		t.Fatalf("old pin consumed the right: %+v %v", attempt, err)
	}
	check(http.MethodPost, "verify", map[string]any{"attempt_id": attempt.AttemptID})
	body := map[string]any{"key": next.Key, "message_id": a.MessageID, "attempt_id": attempt.AttemptID,
		"type": "before_agent_start", "message_type": "custom", "pi_session_id": d.runtime.PiSessionID,
		"details": map[string]any{"message_id": a.MessageID, "attempt_id": attempt.AttemptID, "custom_type": "artel-owner-answer", "question_id": q.ID}}
	first := check(http.MethodPost, "receipt", body)
	if replay := check(http.MethodPost, "receipt", body); replay != first || !strings.Contains(replay, `"status":"observed"`) {
		t.Fatal("valid receipt did not replay")
	}
}
