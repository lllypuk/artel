package api

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	return newAPIResultFixtureAt(t, filepath.Join(t.TempDir(), "test.sqlite"))
}

func newAPIResultFixtureAt(t *testing.T, path string) apiResultFixture {
	t.Helper()
	s, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := New(t.Context(), s)
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
		Sources:           []store.ReviewSource{{Name: "reviewer", Executor: "codex", Required: true, Responded: false, Degraded: true, PreExisting: 1, OpenQuestions: 2}},
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

func requireEmptyCurrentTaskResultHTTP(t *testing.T, f apiResultFixture) {
	t.Helper()
	currentBody := request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current", "", "", 200)
	var current store.CurrentTaskResult
	if err := json.Unmarshal(currentBody, &current); err != nil || current.Result != nil || current.Reviews == nil || len(current.Reviews) != 0 {
		t.Fatalf("empty current read model: %+v %v", current, err)
	}
	emptyReviewsBody := request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current/reviews", "", "", 200)
	var emptyReviews store.CurrentTaskReviewsPage
	if err := json.Unmarshal(emptyReviewsBody, &emptyReviews); err != nil || emptyReviews.ResultID != "" || emptyReviews.Reviews == nil ||
		len(emptyReviews.Reviews) != 0 || emptyReviews.NextAfter != "" || emptyReviews.HasMore {
		t.Fatalf("empty current reviews: %+v %v", emptyReviews, err)
	}
}

func TestCurrentTaskResultHTTPReadModel(t *testing.T) {
	f := newAPIResultFixture(t)
	requireEmptyCurrentTaskResultHTTP(t, f)
	var current store.CurrentTaskResult
	firstBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
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
	apiAttach(t, f, first.ID, uuid.NewString(), apiReviewInput(first, "run-old"), 201)
	currentReviewBody := apiAttach(t, f, second.ID, uuid.NewString(), apiReviewInput(second, "run-current"), 201)
	var currentReview store.TaskReview
	if err := json.Unmarshal(currentReviewBody, &currentReview); err != nil {
		t.Fatal(err)
	}
	currentBody := request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current", "", "", 200)
	if err := json.Unmarshal(currentBody, &current); err != nil || current.Result == nil || current.Result.ID != second.ID ||
		current.Reviews == nil || len(current.Reviews) != 0 {
		t.Fatalf("current candidate read was not bounded: %+v %v", current, err)
	}
	reviewPageBody := request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current/reviews?after=&limit=1", "", "", 200)
	var reviewPage store.CurrentTaskReviewsPage
	if err := json.Unmarshal(reviewPageBody, &reviewPage); err != nil || reviewPage.ResultID != second.ID ||
		len(reviewPage.Reviews) != 1 || reviewPage.Reviews[0].ID != currentReview.ID || reviewPage.HasMore ||
		!reviewPage.Reviews[0].EvidenceConsistent {
		t.Fatalf("current review page includes wrong history: %+v %v", reviewPage, err)
	}
	request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current?unknown=1", "", "", 400)
	request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current/reviews?limit=51", "", "", 400)
	request(t, f.h, "GET", "/v1/tasks/"+f.task.ID+"/results/current/reviews?unknown=1", "", "", 400)
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

func TestLegacyReviewLookupCannotCreateOrReplayCurrentReceipt(t *testing.T) {
	f := newAPIResultFixture(t)
	var result store.TaskResult
	if err := json.Unmarshal(apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201), &result); err != nil {
		t.Fatal(err)
	}
	path := "/v1/tasks/" + f.task.ID + "/results/" + result.ID + "/reviews"
	key := uuid.NewString()
	input := apiReviewInput(result, "current")
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request(t, f.h, "POST", path+"/legacy-receipt", string(body), key, 404)
	created := request(t, f.h, "POST", path, string(body), key, 201)
	request(t, f.h, "POST", path+"/legacy-receipt", string(body), key, 404)
	if replay := request(t, f.h, "POST", path, string(body), key, 201); !bytes.Equal(replay, created) {
		t.Fatalf("current replay changed after a legacy lookup: %s", replay)
	}
	input.IncompleteReasons = []string{"different reason"}
	body, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request(t, f.h, "POST", path+"/legacy-receipt", string(body), key, 404)
	request(t, f.h, "POST", path, string(body), key, 409)
}

func TestReviewLegacyCountersWireContract(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	f := newAPIResultFixtureAt(t, dbPath)
	resultBody := apiSubmit(t, f, uuid.NewString(), apiSubmitInput(""), 201)
	var result store.TaskResult
	if err := json.Unmarshal(resultBody, &result); err != nil {
		t.Fatal(err)
	}
	input := apiReviewInput(result, "legacy-counter")
	input.Complete, input.Sources[0].Responded, input.Sources[0].Degraded = true, true, false
	input.IncompleteReasons = []string{}
	input.Findings, input.PreExisting, input.OpenQuestions = store.MaxReviewCounter+2, store.MaxReviewCounter+2, store.MaxReviewCounter+2
	input.Sources[0].Findings, input.Sources[0].PreExisting, input.Sources[0].OpenQuestions = input.Findings, input.PreExisting, input.OpenQuestions
	key := uuid.NewString()
	originalRequest, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/tasks/" + f.task.ID + "/results/" + result.ID + "/reviews"
	legacy := seedLegacyAPICounterReceipt(t, dbPath, path, key, f.task.ID, result.ID, input, originalRequest)
	for _, suffix := range []string{"", "/legacy-receipt"} {
		replayed := request(t, f.h, "POST", path+suffix, string(originalRequest), key, 201)
		if !bytes.Equal(replayed, legacy) {
			t.Fatalf("legacy receipt changed: %s", replayed)
		}
	}
	request(t, f.h, "POST", path, string(originalRequest), uuid.NewString(), 400)
	request(t, f.h, "POST", path+"/legacy-receipt", strings.Repeat(" ", MaxBody)+string(originalRequest), key, 413)
	currentPath := "/v1/tasks/" + f.task.ID + "/results/current/reviews"
	data := request(t, f.h, "GET", currentPath, "", "", 200)
	var page store.CurrentTaskReviewsPage
	if err := json.Unmarshal(data, &page); err != nil || len(page.Reviews) != 1 {
		t.Fatalf("GET review projection: %s %v", data, err)
	}
	projected := page.Reviews[0]
	if projected.EvidenceConsistent || projected.Findings != store.MaxReviewCounter ||
		projected.PreExisting != store.MaxReviewCounter || projected.OpenQuestions != store.MaxReviewCounter ||
		projected.Sources[0].Findings != store.MaxReviewCounter || projected.Sources[0].PreExisting != store.MaxReviewCounter ||
		projected.Sources[0].OpenQuestions != store.MaxReviewCounter {
		t.Fatalf("GET did not saturate and flag the legacy counters: %s", data)
	}
	for _, from := range []string{"aggregate", "source"} {
		t.Run(from, func(t *testing.T) {
			// Each GET constraint must hold even when the other set is safe.
			changed := page.Reviews[0]
			changed.Sources = append([]store.ReviewSource{}, changed.Sources...)
			if from == "aggregate" {
				changed.Findings, changed.PreExisting, changed.OpenQuestions = input.Findings, input.PreExisting, input.OpenQuestions
			} else {
				changed.Sources[0] = input.Sources[0]
			}
			encoded, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			var model any
			if err := json.Unmarshal(encoded, &model); err != nil {
				t.Fatal(err)
			}
			if err := contract(t).Components.Schemas["TaskReview"].Value.VisitJSON(model); err == nil {
				t.Fatal("GET schema admitted a counter above the safe limit")
			}
		})
	}
	input.Run = "safe-counter"
	input.Findings, input.PreExisting, input.OpenQuestions = store.MaxReviewCounter, store.MaxReviewCounter, store.MaxReviewCounter
	input.Sources[0].Findings, input.Sources[0].PreExisting, input.Sources[0].OpenQuestions = input.Findings, input.PreExisting, input.OpenQuestions
	apiAttach(t, f, result.ID, uuid.NewString(), input, 201)
}

func seedLegacyAPICounterReceipt(t *testing.T, dbPath, urlPath, key, taskID, resultID string, input store.AttachReviewInput, request []byte) []byte {
	t.Helper()
	// Insert an original pre-limit record and exact POST receipt without
	// bypassing the schema's immutability guard or rounding via float64.
	review := store.TaskReview{ID: uuid.NewString(), TaskID: taskID, ResultID: resultID,
		Repo: input.Repo, Base: input.Base, Head: input.Head, RevmuxTask: input.RevmuxTask, Run: input.Run,
		RangeSource: input.RangeSource, ReportRef: input.ReportRef, Complete: input.Complete,
		RequiredSources: input.RequiredSources, Sources: input.Sources, IncompleteReasons: input.IncompleteReasons,
		Findings: input.Findings, PreExisting: input.PreExisting, OpenQuestions: input.OpenQuestions, CreatedAt: "2026-11-20T12:00:00Z"}
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.ReplaceAll(encoded, []byte(`,"evidence_consistent":false`), nil)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	hash := fmt.Sprintf("%x", sha256.Sum256(request))
	if _, err := db.ExecContext(t.Context(), `INSERT INTO task_reviews(id,task_id,result_id,repo,revmux_task,run,payload_hash,record)
		VALUES(?,?,?,?,?,?,?,?)`, review.ID, taskID, resultID, input.Repo, input.RevmuxTask, input.Run, hash, string(legacy)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO mutation_receipts(key,method,path,payload_hash,status,body) VALUES(?,'POST',?,?,201,?)", key, urlPath, hash, legacy); err != nil {
		t.Fatal(err)
	}
	return legacy
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
	input.Findings, input.PreExisting, input.OpenQuestions = 0, 0, 0
	apiAttach(t, f, result.ID, uuid.NewString(), input, 201)
	input.RequiredSources = append(input.RequiredSources, store.ReviewRequirement{Name: "one-too-many", Executor: "codex"})
	input.IncompleteReasons = append(input.IncompleteReasons, "required source missing: one-too-many")
	apiAttach(t, f, result.ID, uuid.NewString(), input, 400)
	for _, schemaName := range []string{"AttachTaskReview", "TaskReviewReceipt"} {
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
		{"GET", "/v1/tasks/" + f.task.ID + "/results/current"},
		{"GET", "/v1/tasks/" + f.task.ID + "/results/current/reviews"},
		{"POST", "/v1/tasks/" + f.task.ID + "/results/" + uuid.NewString() + "/reviews"},
		{"POST", "/v1/tasks/" + f.task.ID + "/results/" + uuid.NewString() + "/reviews/legacy-receipt"},
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
