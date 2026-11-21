package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/store"
)

const (
	apiResultRepo = "/tmp/api-result/.git"
	apiResultBase = "1111111111111111111111111111111111111111"
	apiResultHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type apiResultFixture struct {
	store   *store.Store
	h       http.Handler
	project domain.Project
	task    domain.Task
}

func newAPIResultFixture(t *testing.T) apiResultFixture {
	t.Helper()
	s, h := fixture(t)
	projectBody, _ := json.Marshal(map[string]string{"name": "Results", "root_path": t.TempDir()})
	projectData := request(t, h, "POST", "/v1/projects", string(projectBody), uuid.NewString(), 201)
	var project domain.Project
	if err := json.Unmarshal(projectData, &project); err != nil {
		t.Fatal(err)
	}
	taskData := request(t, h, "POST", "/v1/projects/"+project.ID+"/tasks", `{"title":"Review candidate"}`, uuid.NewString(), 201)
	var task domain.Task
	if err := json.Unmarshal(taskData, &task); err != nil {
		t.Fatal(err)
	}
	return apiResultFixture{store: s, h: h, project: project, task: task}
}

func apiSubmitInput(expected string) store.SubmitResultInput {
	return store.SubmitResultInput{Repo: apiResultRepo, Base: apiResultBase, Head: apiResultHead, Summary: "ready for owner review", ExpectedResultID: expected}
}

func apiSubmit(t *testing.T, f apiResultFixture, key string, in store.SubmitResultInput, status int) []byte {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return request(t, f.h, "POST", "/v1/tasks/"+f.task.ID+"/results", string(body), key, status)
}

func apiReviewInput(result store.TaskResult, run string) store.AttachReviewInput {
	return store.AttachReviewInput{
		Repo: result.Repo, Base: result.Base, Head: result.Head,
		RevmuxTask: "api-review", Run: run, RangeSource: "operator", ReportRef: "/tmp/report/" + run,
		Complete:          false,
		RequiredSources:   []store.ReviewRequirement{{Name: "reviewer", Executor: "codex"}},
		Sources:           []store.ReviewSource{{Name: "reviewer", Executor: "codex", Required: true, Responded: false, Degraded: true}},
		IncompleteReasons: []string{"required source degraded"}, Findings: 0, PreExisting: 1, OpenQuestions: 2,
	}
}

func apiAttach(t *testing.T, f apiResultFixture, resultID, key string, in store.AttachReviewInput, status int) []byte {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return request(t, f.h, "POST", "/v1/tasks/"+f.task.ID+"/results/"+resultID+"/reviews", string(body), key, status)
}

func TestTaskResultHTTPWorkflowAuthorityAndRegression(t *testing.T) {
	f := newAPIResultFixture(t)
	invalid := apiSubmitInput("")
	invalid.Head = "not-a-sha"
	apiSubmit(t, f, uuid.NewString(), invalid, 400)
	firstKey := uuid.NewString()
	firstBody := apiSubmit(t, f, firstKey, apiSubmitInput(""), 201)
	var first store.TaskResult
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatal(err)
	}
	secondInput := apiSubmitInput(first.ID)
	secondInput.Head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	secondBody := apiSubmit(t, f, uuid.NewString(), secondInput, 201)
	var second store.TaskResult
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatal(err)
	}
	if replay := apiSubmit(t, f, firstKey, apiSubmitInput(""), 201); !bytes.Equal(replay, firstBody) {
		t.Fatal("HTTP receipt did not recover original submission")
	}
	apiSubmit(t, f, uuid.NewString(), apiSubmitInput(first.ID), 409)
	before, err := f.store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	attachKey := uuid.NewString()
	reviewBody := apiAttach(t, f, first.ID, attachKey, apiReviewInput(first, "run-1"), 201)
	var review store.TaskReview
	if err := json.Unmarshal(reviewBody, &review); err != nil {
		t.Fatal(err)
	}
	if replay := apiAttach(t, f, first.ID, attachKey, apiReviewInput(first, "run-1"), 201); !bytes.Equal(replay, reviewBody) {
		t.Fatal("attach receipt changed")
	}
	duplicate := apiAttach(t, f, first.ID, uuid.NewString(), apiReviewInput(first, "run-1"), 200)
	var existing store.TaskReview
	if err := json.Unmarshal(duplicate, &existing); err != nil || existing.ID != review.ID {
		t.Fatal("business replay created another review")
	}
	after, err := f.store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Tasks, after.Tasks) || !reflect.DeepEqual(before.Projects, after.Projects) || !reflect.DeepEqual(before.Queues, after.Queues) {
		t.Fatal("review import changed task status, queue or assignment")
	}
	otherTaskData := request(t, f.h, "POST", "/v1/projects/"+f.project.ID+"/tasks", `{"title":"Other task"}`, uuid.NewString(), 201)
	var otherTask domain.Task
	if err := json.Unmarshal(otherTaskData, &otherTask); err != nil {
		t.Fatal(err)
	}
	otherFixture := f
	otherFixture.task = otherTask
	apiAttach(t, otherFixture, first.ID, uuid.NewString(), apiReviewInput(first, "foreign"), 404)
	for _, mutate := range []func(*store.AttachReviewInput){
		func(in *store.AttachReviewInput) { in.Repo = "/tmp/forged/.git" },
		func(in *store.AttachReviewInput) { in.Base = apiResultHead },
		func(in *store.AttachReviewInput) { in.Head = apiResultBase },
	} {
		forged := apiReviewInput(first, uuid.NewString())
		mutate(&forged)
		apiAttach(t, f, first.ID, uuid.NewString(), forged, 409)
	}
	historyBody := request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results?after=0&limit=100", "", "", 200)
	var history store.TaskResultHistoryPage
	if err := json.Unmarshal(historyBody, &history); err != nil {
		t.Fatal(err)
	}
	if history.CurrentResultID != second.ID || len(history.Entries) != 3 || history.Entries[2].Review.ResultID != first.ID {
		t.Fatalf("unexpected history: %+v", history)
	}
	request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results?limit=101", "", "", 400)
	request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results?unknown=1", "", "", 400)
}

func TestOwnerReviewCompletenessAndUTF8ByteLimits(t *testing.T) {
	f := newAPIResultFixture(t)
	resultBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
	var result store.TaskResult
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatal(err)
	}
	empty := apiReviewInput(result, "empty-complete")
	empty.Complete = true
	empty.RequiredSources = []store.ReviewRequirement{}
	empty.Sources = []store.ReviewSource{}
	empty.IncompleteReasons = []string{}
	empty.Findings, empty.PreExisting, empty.OpenQuestions = 0, 0, 0
	apiAttach(t, f, result.ID, uuid.NewString(), empty, 400)
	empty.Complete = false
	empty.Run = "empty-incomplete"
	empty.IncompleteReasons = []string{"required source list is empty"}
	apiAttach(t, f, result.ID, uuid.NewString(), empty, 201)

	missing := apiReviewInput(result, "missing-required")
	missing.Complete = true
	missing.Sources = []store.ReviewSource{}
	missing.IncompleteReasons = []string{}
	apiAttach(t, f, result.ID, uuid.NewString(), missing, 400)

	other := newAPIResultFixture(t)
	accepted := apiSubmitInput("")
	accepted.Summary = strings.Repeat("é", 2048)
	apiSubmit(t, other, uuid.NewString(), accepted, 201)
	rejected := apiSubmitInput("")
	rejected.Summary = strings.Repeat("é", 2049)
	apiSubmit(t, other, uuid.NewString(), rejected, 400)
}

func TestResultWireRejectsNullOmittedAndAliasFields(t *testing.T) {
	f := newAPIResultFixture(t)
	for _, body := range []string{
		`{"repo":"/tmp/api-result/.git","base":"1111111111111111111111111111111111111111","head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_result_id":null}`,
		`{"repo":"/tmp/api-result/.git","base":"1111111111111111111111111111111111111111","head":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","summary":null,"expected_result_id":""}`,
	} {
		request(t, f.h, "POST", "/v1/tasks/"+f.task.ID+"/results", body, uuid.NewString(), 400)
	}
	resultBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
	var result store.TaskResult
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "top-level scalar null", mutate: func(body map[string]any) { body["findings"] = nil }},
		{name: "source scalar null", mutate: func(body map[string]any) {
			body["sources"].([]any)[0].(map[string]any)["degraded"] = nil
		}},
		{name: "source counter null", mutate: func(body map[string]any) {
			body["sources"].([]any)[0].(map[string]any)["findings"] = nil
		}},
		{name: "source field omitted", mutate: func(body map[string]any) {
			delete(body["sources"].([]any)[0].(map[string]any), "responded")
		}},
		{name: "source field alias", mutate: func(body map[string]any) {
			source := body["sources"].([]any)[0].(map[string]any)
			source["Name"] = source["name"]
			delete(source, "name")
		}},
		{name: "source unknown field", mutate: func(body map[string]any) {
			body["sources"].([]any)[0].(map[string]any)["unexpected"] = false
		}},
		{name: "requirement field omitted", mutate: func(body map[string]any) {
			delete(body["required_sources"].([]any)[0].(map[string]any), "executor")
		}},
		{name: "requirement field alias", mutate: func(body map[string]any) {
			requirement := body["required_sources"].([]any)[0].(map[string]any)
			requirement["Executor"] = requirement["executor"]
			delete(requirement, "executor")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(apiReviewInput(result, "strict-wire"))
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			test.mutate(body)
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			request(t, f.h, "POST", "/v1/tasks/"+f.task.ID+"/results/"+result.ID+"/reviews", string(data), uuid.NewString(), 400)
		})
	}
	history, err := f.store.TaskResultHistory(t.Context(), f.task.ID, 0, 100)
	if err != nil || len(history.Entries) != 1 {
		t.Fatalf("rejected wire payload mutated history: %+v %v", history, err)
	}
}

func TestReviewRequiredSourceLimitMatchesReasons(t *testing.T) {
	f := newAPIResultFixture(t)
	resultBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
	var result store.TaskResult
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatal(err)
	}
	input := apiReviewInput(result, "required-limit")
	input.RequiredSources = make([]store.ReviewRequirement, store.MaxRequiredSources)
	input.Sources = []store.ReviewSource{}
	input.IncompleteReasons = make([]string, store.MaxRequiredSources)
	for index := range input.RequiredSources {
		name := "missing-" + strconv.Itoa(index)
		input.RequiredSources[index] = store.ReviewRequirement{Name: name, Executor: "codex"}
		input.IncompleteReasons[index] = "required source missing: " + name
	}
	input.Complete = false
	apiAttach(t, f, result.ID, uuid.NewString(), input, 201)
	input.RequiredSources = append(input.RequiredSources, store.ReviewRequirement{Name: "one-too-many", Executor: "codex"})
	input.IncompleteReasons = append(input.IncompleteReasons, "required source missing: one-too-many")
	apiAttach(t, f, result.ID, uuid.NewString(), input, 400)
	for _, schemaName := range []string{"AttachTaskReview", "TaskReview"} {
		schema := contract(t).Components.Schemas[schemaName].Value.Properties["required_sources"].Value
		if schema.MaxItems == nil || *schema.MaxItems != store.MaxRequiredSources {
			t.Fatalf("%s required_sources max=%v", schemaName, schema.MaxItems)
		}
	}
}

func TestTaskResultLostHTTPResponseRetry(t *testing.T) {
	f := newAPIResultFixture(t)
	var committed []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.h.ServeHTTP(recorder, r)
		if r.Header.Get("X-Test-Drop-Response") == "yes" {
			committed = bytes.Clone(recorder.Body.Bytes())
			connection, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		for name, values := range recorder.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	defer server.Close()
	payload, err := json.Marshal(apiSubmitInput(""))
	if err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	for _, drop := range []bool{true, false} {
		req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/v1/tasks/"+f.task.ID+"/results", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
		if drop {
			req.Header.Set("X-Test-Drop-Response", "yes")
		}
		// #nosec G704 -- Destination is exclusively this test's ephemeral server.
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
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
		if readErr != nil || response.StatusCode != 201 || !bytes.Equal(body, committed) {
			t.Fatalf("retry status=%d body=%s err=%v", response.StatusCode, body, readErr)
		}
	}
	history, err := f.store.TaskResultHistory(t.Context(), f.task.ID, 0, 100)
	if err != nil || len(history.Entries) != 1 {
		t.Fatalf("lost response retry duplicated result: %+v %v", history, err)
	}
}

func TestTransitionExpectedResultAndReceiptRecovery(t *testing.T) {
	t.Run("legacy without submission", func(t *testing.T) {
		f := newAPIResultFixture(t)
		request(t, f.h, "POST", "/v1/tasks/"+f.task.ID+"/transition", `{"state":"cancelled","expected_version":1}`, uuid.NewString(), 200)
	})
	f := newAPIResultFixture(t)
	resultBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
	var result store.TaskResult
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatal(err)
	}
	path := "/v1/tasks/" + f.task.ID + "/transition"
	for _, body := range []string{
		`{"state":"cancelled","expected_version":1}`,
		`{"state":"cancelled","expected_version":1,"expected_result_id":""}`,
		`{"state":"cancelled","expected_version":1,"expected_result_id":"` + uuid.NewString() + `"}`,
	} {
		request(t, f.h, "POST", path, body, uuid.NewString(), 409)
	}
	transitionKey := uuid.NewString()
	successBody := `{"state":"cancelled","expected_version":1,"expected_result_id":"` + result.ID + `"}`
	firstTransition := request(t, f.h, "POST", path, successBody, transitionKey, 200)
	secondInput := apiSubmitInput(result.ID)
	secondInput.Head = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	secondBody := apiSubmit(t, f, uuid.NewString(), secondInput, 201)
	var second store.TaskResult
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if replay := request(t, f.h, "POST", path, successBody, transitionKey, 200); !bytes.Equal(replay, firstTransition) {
		t.Fatal("transition receipt replay changed response")
	}
	after, err := f.store.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("replayed transition mutated after resubmission: %v", err)
	}
	// The superseded real ID, not an arbitrary UUID, must fail with the current
	// task version and a fresh idempotency key after the pointer advanced.
	staleBody := `{"state":"backlog","expected_version":2,"expected_result_id":"` + result.ID + `"}`
	request(t, f.h, "POST", path, staleBody, uuid.NewString(), 409)
	after, err = f.store.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("superseded result transition mutated task %s (current %s): %v", result.ID, second.ID, err)
	}
}

func TestTaskResultRoutesRequireOwner(t *testing.T) {
	f := newAPIResultFixture(t)
	const host = "127.0.0.1:12345"
	for _, route := range []struct{ method, path string }{
		{"POST", "/v1/tasks/" + f.task.ID + "/results"},
		{"GET", "/v1/tasks/" + f.task.ID + "/results"},
		{"POST", "/v1/tasks/" + f.task.ID + "/results/" + uuid.NewString() + "/reviews"},
	} {
		r := httptest.NewRequest(route.method, route.path, nil)
		r.Host = host
		w := httptest.NewRecorder()
		Protect("owner", host, f.h).ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("%s %s: status=%d", route.method, route.path, w.Code)
		}
		checkContract(t, r, w)
	}
}
