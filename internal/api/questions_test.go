package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/store"
)

func newQuestionAPIEnv(t *testing.T, ready bool) *deliveryOwnerEnv {
	t.Helper()
	e := newDeliveryOwnerEnv(t, false)
	if _, err := e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "revoke", BindingID: e.binding.ID,
	}); err != nil {
		t.Fatal(err)
	}
	registry, err := e.s.Observers(t.Context())
	if err != nil || len(registry.Observers) != 1 {
		t.Fatalf("observer registry: %v", err)
	}
	e.deliveryToken = strings.Repeat("8d", 32)
	e.binding, err = e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "enable", ExpectedVer: registry.Observers[0].Version,
		BindingID: uuid.NewString(), JournalID: uuid.NewString(), Verifier: sha256Hex(e.deliveryToken),
		RuntimeID: e.runtime.RuntimeID, ProjectID: e.project.ID, PiSessionID: e.runtime.PiSessionID,
		InstanceID: e.instance, AllowOwnerQuestions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeliveryAttach(t.Context(), e.binding.ID, e.writerID, "", e.binding.JournalID,
		e.runtime.RuntimeID, e.runtime.PiSessionID, store.DeliveryOwnerQuestionsCapability); err != nil {
		t.Fatal(err)
	}
	e.binding.Status = "active"
	if ready {
		if err := e.s.DeliveryReady(t.Context(), e.binding.ID, e.writerID); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *deliveryOwnerEnv) ownerQuestionRequest(t *testing.T, method, path, key, body string, status int) []byte {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	e.owner.ServeHTTP(w, r)
	r.Body = io.NopCloser(strings.NewReader(body))
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	checkContract(t, r, w)
	return w.Body.Bytes()
}

func (e *deliveryOwnerEnv) answerQuestion(t *testing.T, taskID, questionID, key, instance, text string, status int) store.OwnerQuestionAnswerResult {
	t.Helper()
	bodyBytes, err := json.Marshal(map[string]string{"instance_id": instance, "text": text})
	if err != nil {
		t.Fatal(err)
	}
	data := e.ownerQuestionRequest(t, http.MethodPost, "/v1/tasks/"+taskID+"/questions/"+questionID+"/answer", key, string(bodyBytes), status)
	var result store.OwnerQuestionAnswerResult
	if status == 200 {
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func (e *deliveryOwnerEnv) listQuestions(t *testing.T, taskID, query string, status int) ([]store.OwnerQuestion, int64) {
	t.Helper()
	data := e.ownerQuestionRequest(t, http.MethodGet, "/v1/tasks/"+taskID+"/questions"+query, "", "", status)
	var page struct {
		Questions []store.OwnerQuestion `json:"questions"`
		Next      int64                 `json:"next_after"`
	}
	if status == 200 {
		if err := json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
	}
	return page.Questions, page.Next
}

func (e *deliveryOwnerEnv) createQuestion(t *testing.T, key, taskID, writer, instance, text string, status int) store.OwnerQuestionCreateResult {
	t.Helper()
	bodyBytes, err := json.Marshal(map[string]string{"instance_id": instance, "task_id": taskID, "writer_id": writer, "text": text})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/delivery/v1/questions", bytes.NewReader(bodyBytes))
	r.Host = "artel-observer"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+e.deliveryToken)
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	e.bridge.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("create question: got %d want %d: %s", w.Code, status, w.Body.String())
	}
	var result store.OwnerQuestionCreateResult
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestOwnerQuestionHTTPReplayPaginationAndNoReadyAnswer(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	key := uuid.NewString()
	question := e.createQuestion(t, key, e.task.ID, e.writerID, e.instance, "question\r\n\x01", 200)
	replay := e.createQuestion(t, key, e.task.ID, e.writerID, e.instance, "question\r\n\x01", 200)
	if replay != question {
		t.Fatalf("create replay: %+v %+v", question, replay)
	}
	e.createQuestion(t, key, e.task.ID, e.writerID, e.instance, "changed", 409)
	answerKey := uuid.NewString()
	answer := e.answerQuestion(t, e.task.ID, question.ID, answerKey, e.instance, "answer without ready", 200)
	answerReplay := e.answerQuestion(t, e.task.ID, question.ID, answerKey, e.instance, "answer without ready", 200)
	if answerReplay != answer {
		t.Fatalf("answer replay: %+v %+v", answer, answerReplay)
	}
	e.answerQuestion(t, e.task.ID, question.ID, answerKey, e.instance, "changed", 409)
	e.answerQuestion(t, e.task.ID, question.ID, uuid.NewString(), e.instance, "second", 409)
	e.answerQuestion(t, uuid.NewString(), question.ID, answerKey, e.instance, "answer without ready", 404)
	e.answerQuestion(t, e.task.ID, uuid.NewString(), uuid.NewString(), e.instance, "missing", 404)
	e.answerQuestion(t, e.task.ID, question.ID, uuid.NewString(), uuid.NewString(), "foreign DB", 409)
	questions, _ := e.listQuestions(t, e.task.ID, "?instance_id="+e.instance, 200)
	if len(questions) != 1 || questions[0].ID != question.ID || questions[0].AnswerText != "answer without ready" || questions[0].AnswerStatus != "stored" {
		t.Fatalf("questions: %+v", questions)
	}
	e.listQuestions(t, e.task.ID, "?instance_id="+uuid.NewString(), 409)
	for i := range 4 {
		e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, fmt.Sprintf("page-%d", i), 200)
	}
	var walked []store.OwnerQuestion
	after := int64(0)
	for {
		page, next := e.listQuestions(t, e.task.ID, fmt.Sprintf("?instance_id=%s&after=%d&limit=2", e.instance, after), 200)
		walked = append(walked, page...)
		if len(page) < 2 {
			break
		}
		after = next
	}
	if len(walked) != 5 {
		t.Fatalf("pages: %d", len(walked))
	}
	refreshed, _ := e.listQuestions(t, e.task.ID, "?instance_id="+e.instance+"&ids="+question.ID+","+walked[4].ID, 200)
	if len(refreshed) != 2 || refreshed[0].ID != question.ID || refreshed[1].ID != walked[4].ID {
		t.Fatalf("refresh: %+v", refreshed)
	}
	for _, query := range []string{
		"?instance_id=" + e.instance + "&ids=" + question.ID + "&after=0",
		"?instance_id=" + e.instance + "&limit=0",
		"?instance_id=" + e.instance + "&limit=6",
		"?instance_id=" + e.instance + "&ids=",
		"?instance_id=" + e.instance + "&unknown=x",
		"?instance_id=" + e.instance + "&after=0&after=1",
	} {
		e.listQuestions(t, e.task.ID, query, 400)
	}
}

func TestOwnerQuestionHTTPCloseRevokeAndReplay(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	createKey := uuid.NewString()
	answered := e.createQuestion(t, createKey, e.task.ID, e.writerID, e.instance, "answered before close", 200)
	open := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "closed unanswered", 200)
	key := uuid.NewString()
	answer := e.answerQuestion(t, e.task.ID, answered.ID, key, e.instance, "saved", 200)
	setTaskState(t, e.s, e.task.ID, "done")
	replayed := e.answerQuestion(t, e.task.ID, answered.ID, key, e.instance, "saved", 200)
	if replayed != answer {
		t.Fatal("answer replay changed after close")
	}
	e.answerQuestion(t, e.task.ID, open.ID, uuid.NewString(), e.instance, "too late", 409)
	e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "new after close", 409)
	// Question ACK recovery remains valid after task close while the exact grant
	// and writer remain active.
	created := e.createQuestion(t, createKey, e.task.ID, e.writerID, e.instance, "answered before close", 200)
	if created != answered {
		t.Fatal("question replay changed after close")
	}
	if _, err := e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "revoke", BindingID: e.binding.ID,
	}); err != nil {
		t.Fatal(err)
	}
	replayed = e.answerQuestion(t, e.task.ID, answered.ID, key, e.instance, "saved", 200)
	if replayed != answer {
		t.Fatal("answer replay changed after revoke")
	}
	e.createQuestion(t, createKey, e.task.ID, e.writerID, e.instance, "answered before close", 401)
}

func TestOwnerQuestionHTTPStrictJSONAndTextLimits(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	question := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "strict", 200)
	path := "/v1/tasks/" + e.task.ID + "/questions/" + question.ID + "/answer"
	base := fmt.Sprintf(`{"instance_id":%q,"text":"ok"}`, e.instance)
	for _, body := range []string{
		"", "null", "[]", `{}`, base + `{}`, strings.Replace(base, `"text"`, `"TEXT"`, 1),
		strings.Replace(base, `"text":"ok"`, `"text":null`, 1),
		strings.Replace(base, `"text":"ok"`, `"text":"ok","text":"twice"`, 1),
		strings.Replace(base, `"text":"ok"`, `"text":"ok","extra":{}`, 1),
	} {
		e.ownerQuestionRequest(t, http.MethodPost, path, uuid.NewString(), body, 400)
	}
	for _, key := range []string{"", "not-a-uuid"} {
		e.ownerQuestionRequest(t, http.MethodPost, path, key, base, 400)
	}
	for _, text := range []string{"", "nul\x00", strings.Repeat("x", 4097)} {
		e.answerQuestion(t, e.task.ID, question.ID, uuid.NewString(), e.instance, text, 400)
	}
	// All non-NUL control bytes occupy six wire bytes each. The 4 KiB text
	// boundary still fits the independent 32 KiB answer body limit.
	control := strings.Repeat("\x01", 4096)
	controlled := e.answerQuestion(t, e.task.ID, question.ID, uuid.NewString(), e.instance, control, 200)
	if controlled.MessageID == "" {
		t.Fatal("maximum escaped answer rejected")
	}
	other := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "body limit", 200)
	tooLarge := fmt.Sprintf(`{"instance_id":%q,"text":%q,"padding":%q}`, e.instance, "x", strings.Repeat("x", maxQuestionBody))
	e.ownerQuestionRequest(t, http.MethodPost, "/v1/tasks/"+e.task.ID+"/questions/"+other.ID+"/answer", uuid.NewString(), tooLarge, 413)
	questions, _ := e.listQuestions(t, e.task.ID, "?instance_id="+e.instance+"&ids="+other.ID, 200)
	if len(questions) != 1 || questions[0].Status != "open" {
		t.Fatalf("oversize body wrote answer: %+v", questions)
	}
}

func TestOwnerQuestionHTTPRealWireAndAuthentication(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	token := strings.Repeat("5e", 32)
	var protected http.Handler
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protected.ServeHTTP(w, r)
	}))
	defer server.Close()
	protected = Protect(token, strings.TrimPrefix(server.URL, "http://"), e.owner)
	client := server.Client()
	question := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "real wire", 200)
	path := "/v1/tasks/" + e.task.ID + "/questions/" + question.ID + "/answer"
	request := func(body string, auth bool) int {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", uuid.NewString())
		if auth {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		// #nosec G704 -- Destination is exclusively this test's bounded ephemeral loopback server.
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode
	}
	tooLarge := fmt.Sprintf(`{"instance_id":%q,"text":%q,"padding":%q}`, e.instance, "x", strings.Repeat("x", maxQuestionBody))
	if got := request(tooLarge, false); got != 401 {
		t.Fatalf("body parsed before owner auth: %d", got)
	}
	if got := request(tooLarge, true); got != 413 {
		t.Fatalf("real wire body limit: %d", got)
	}
	validBytes, err := json.Marshal(map[string]string{"instance_id": e.instance, "text": strings.Repeat("\x01", 4096)})
	if err != nil {
		t.Fatal(err)
	}
	if got := request(string(validBytes), true); got != 200 {
		t.Fatalf("real wire escaped boundary: %d", got)
	}
}

func TestOwnerQuestionHTTPSwappedDatabaseDoesNotWriteOrReplay(t *testing.T) {
	original := newQuestionAPIEnv(t, false)
	question := original.createQuestion(t, uuid.NewString(), original.task.ID, original.writerID, original.instance, "original DB", 200)
	key := uuid.NewString()
	original.answerQuestion(t, original.task.ID, question.ID, key, original.instance, "original answer", 200)
	// Build a different database with exactly the same public IDs, credentials,
	// binding and writer. Only its instance identity differs.
	path := filepath.Join(t.TempDir(), "replacement.sqlite")
	replacementStore, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacementStore.Close() })
	replacement := &deliveryOwnerEnv{s: replacementStore, owner: New(t.Context(), replacementStore), bridge: original.bridge,
		observer: original.observer, runtime: original.runtime, project: original.project, task: original.task,
		binding: original.binding, deliveryToken: original.deliveryToken, writerID: original.writerID}
	seedQuestionReplacement(t, replacement)
	snap, err := replacementStore.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	replacement.instance = snap.InstanceID
	if replacement.instance == original.instance {
		t.Fatal("replacement reused database identity")
	}
	// Replaying the old owner payload against the replacement is fenced before
	// receipt lookup and cannot expose or create an answer.
	replacement.answerQuestion(t, replacement.task.ID, question.ID, key, original.instance, "original answer", 409)
	rows, _ := replacement.listQuestions(t, replacement.task.ID, "?instance_id="+replacement.instance, 200)
	if len(rows) != 0 {
		t.Fatalf("replacement exposed or created question history: %+v", rows)
	}
}

func seedQuestionReplacement(t *testing.T, e *deliveryOwnerEnv) {
	t.Helper()
	_, err := e.s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Projects = append(snap.Projects, e.project)
		snap.Tasks = append(snap.Tasks, e.task)
		snap.Queues[e.project.ID] = []string{e.task.ID}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: e.project.ID, TaskID: e.task.ID, Timestamp: "2026-11-20T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	observerVerifier := sha256Hex(uuid.NewString())
	provisioned, err := e.s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "provision", Label: "replacement", Verifier: observerVerifier})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.RegisterObserver(t.Context(), observerVerifier, e.runtime); err != nil {
		t.Fatal(err)
	}
	bound, err := e.s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "bind", ExpectedVersion: provisioned.Version, RuntimeID: e.runtime.RuntimeID, ProjectID: e.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.binding, err = e.s.MutateDeliveryBinding(t.Context(), store.DeliveryOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(), Action: "enable", ExpectedVer: bound.Version,
		BindingID: e.binding.ID, JournalID: uuid.NewString(), Verifier: sha256Hex(e.deliveryToken), RuntimeID: e.runtime.RuntimeID,
		ProjectID: e.project.ID, PiSessionID: e.runtime.PiSessionID, InstanceID: mustOwnerInstance(t, e.s), AllowOwnerQuestions: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.DeliveryAttach(t.Context(), e.binding.ID, e.writerID, "", e.binding.JournalID, e.runtime.RuntimeID, e.runtime.PiSessionID, store.DeliveryOwnerQuestionsCapability); err != nil {
		t.Fatal(err)
	}
}

func mustOwnerInstance(t *testing.T, s *store.Store) string {
	t.Helper()
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return snap.InstanceID
}

func TestOwnerQuestionListResponseLimit(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	for range 5 {
		q := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, strings.Repeat("\x01", 4096), 200)
		e.answerQuestion(t, e.task.ID, q.ID, uuid.NewString(), e.instance, strings.Repeat("\x01", 4096), 200)
	}
	rows, _ := e.listQuestions(t, e.task.ID, "?instance_id="+e.instance, 200)
	if len(rows) != 5 {
		t.Fatalf("maximum list rows: %d", len(rows))
	}
}

func TestOwnerQuestionOpenAPIContract(t *testing.T) {
	doc := contract(t)
	create := doc.Paths.Find("/delivery/v1/questions").Post
	if create == nil || create.Servers == nil || len(*create.Servers) != 1 || (*create.Servers)[0].URL != "http://artel-observer" {
		t.Fatal("question creation is not pinned to the private delivery UDS")
	}
	list := doc.Paths.Find("/v1/tasks/{task_id}/questions").Get
	answer := doc.Paths.Find("/v1/tasks/{task_id}/questions/{question_id}/answer").Post
	if list == nil || answer == nil {
		t.Fatal("owner question routes missing")
	}
	page := doc.Components.Schemas["OwnerQuestionPage"].Value
	if page.Properties["questions"].Value.MaxItems == nil || *page.Properties["questions"].Value.MaxItems != 5 {
		t.Fatal("question page is not bounded to five rows")
	}
	enable := doc.Components.Schemas["EnableDelivery"].Value
	if _, ok := enable.Properties["allow_owner_questions"]; !ok {
		t.Fatal("enable option bit missing from contract")
	}
	question := doc.Components.Schemas["OwnerQuestion"].Value
	if _, ok := question.Properties["answer_block_reason"]; !ok {
		t.Fatal("persisted answer block reason missing from contract")
	}
}

func TestOwnerQuestionInvalidUTF8OverWire(t *testing.T) {
	e := newQuestionAPIEnv(t, false)
	question := e.createQuestion(t, uuid.NewString(), e.task.ID, e.writerID, e.instance, "utf8", 200)
	path := "/v1/tasks/" + e.task.ID + "/questions/" + question.ID + "/answer"
	body := bytes.ReplaceAll([]byte(fmt.Sprintf(`{"instance_id":%q,"text":"x"}`, e.instance)), []byte("x"), []byte{0xff})
	e.ownerQuestionRequest(t, http.MethodPost, path, uuid.NewString(), string(body), 400)
}
