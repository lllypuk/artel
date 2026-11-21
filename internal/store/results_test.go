package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

const (
	resultRepoA = "/tmp/artel-test/.git"
	resultRepoB = "/tmp/artel-other/.git"
	resultBase  = "1111111111111111111111111111111111111111"
	resultHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resultHeadB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func resultMutation(path string) Mutation {
	return Mutation{Key: uuid.NewString(), Method: "POST", Path: path}
}

func submitInput(repo, head, expected string) SubmitResultInput {
	return SubmitResultInput{Repo: repo, Base: resultBase, Head: head, Summary: "candidate", ExpectedResultID: expected}
}

func reviewInput(result TaskResult, run string) AttachReviewInput {
	return AttachReviewInput{
		Repo: result.Repo, Base: result.Base, Head: result.Head,
		RevmuxTask: "feature-result", Run: run, RangeSource: "operator", ReportRef: "/reports/" + run,
		Complete:          true,
		RequiredSources:   []ReviewRequirement{{Name: "reviewer", Executor: "codex"}},
		Sources:           []ReviewSource{{Name: "reviewer", Executor: "codex", Required: true, Responded: true}},
		IncompleteReasons: []string{},
	}
}

func decodeTaskResult(t *testing.T, result Result) TaskResult {
	t.Helper()
	var decoded TaskResult
	if err := json.Unmarshal(result.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mustSubmitResult(t *testing.T, s *Store, taskID string, m Mutation, in SubmitResultInput) TaskResult {
	t.Helper()
	result, err := s.SubmitResult(t.Context(), taskID, m, in)
	if err != nil || result.Status != 201 {
		t.Fatalf("submit status=%d err=%v", result.Status, err)
	}
	return decodeTaskResult(t, result)
}

func mustAttachReview(t *testing.T, s *Store, taskID, resultID string, m Mutation, in AttachReviewInput) TaskReview {
	t.Helper()
	result, err := s.AttachReview(t.Context(), taskID, resultID, m, in)
	if err != nil || result.Status != 201 {
		t.Fatalf("attach status=%d err=%v body=%s", result.Status, err, result.Body)
	}
	var review TaskReview
	if err := json.Unmarshal(result.Body, &review); err != nil {
		t.Fatal(err)
	}
	return review
}

func seedLegacyReviewReceipt(t *testing.T, s *Store, taskID string, result TaskResult, mutation Mutation, in AttachReviewInput) (TaskReview, Result) {
	t.Helper()
	review := TaskReview{ID: uuid.NewString(), TaskID: taskID, ResultID: result.ID,
		Repo: result.Repo, Base: result.Base, Head: result.Head, RevmuxTask: in.RevmuxTask, Run: in.Run,
		RangeSource: "operator", ReportRef: in.ReportRef, Complete: in.Complete,
		RequiredSources: in.RequiredSources, Sources: in.Sources, IncompleteReasons: in.IncompleteReasons,
		Findings: in.Findings, PreExisting: in.PreExisting, OpenQuestions: in.OpenQuestions,
		CreatedAt: "2026-11-20T12:00:00Z"}
	encoded, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "evidence_consistent")
	encoded, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	mutation.Hash = observerHash(in)
	response := Result{Status: 201, Body: encoded}
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO task_reviews(id,task_id,result_id,repo,revmux_task,run,payload_hash,record)
		VALUES(?,?,?,?,?,?,?,?)`, review.ID, taskID, result.ID, result.Repo, in.RevmuxTask, in.Run, observerHash(in), string(encoded)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := resultReceipt(t.Context(), tx, mutation, response); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return review, response
}

func TestResultReceiptBeforeCASRestartAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, task := seed(t, s)
	firstMutation := resultMutation("/v1/tasks/" + task.ID + "/results")
	firstInput := submitInput(resultRepoA, resultHeadA, "")
	firstResponse, err := s.SubmitResult(t.Context(), task.ID, firstMutation, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	first := decodeTaskResult(t, firstResponse)
	current, ok := mustSnapshot(t, s).CurrentResultID(task.ID)
	if !ok || current != first.ID {
		t.Fatalf("current=%q submitted=%v", current, ok)
	}
	second := mustSubmitResult(t, s, task.ID, resultMutation(firstMutation.Path), submitInput(resultRepoA, resultHeadB, first.ID))
	beforeEvents := mustEvents(t, s)
	replay, err := s.SubmitResult(t.Context(), task.ID, firstMutation, firstInput)
	if err != nil || !bytes.Equal(replay.Body, firstResponse.Body) || replay.Status != 201 {
		t.Fatalf("lost response replay: %+v %v", replay, err)
	}
	if !reflect.DeepEqual(beforeEvents, mustEvents(t, s)) {
		t.Fatal("receipt replay appended an event after a newer submission")
	}
	staleMutation := resultMutation(firstMutation.Path)
	_, err = s.SubmitResult(t.Context(), task.ID, staleMutation, submitInput(resultRepoA, resultHeadA, first.ID))
	requireResultCode(t, err, "conflict")
	changed := firstInput
	changed.Summary = "different payload"
	_, err = s.SubmitResult(t.Context(), task.ID, firstMutation, changed)
	requireResultCode(t, err, "conflict")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertResultRestartAndHistory(t, path, task.ID, firstMutation, firstInput, firstResponse, first.ID, second.ID)
}

func assertResultRestartAndHistory(t *testing.T, path, taskID string, mutation Mutation, input SubmitResultInput, firstResponse Result, firstID, secondID string) {
	t.Helper()
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	current, ok := mustSnapshot(t, s).CurrentResultID(taskID)
	if !ok || current != secondID {
		t.Fatal("current result was not durable")
	}
	replay, err := s.SubmitResult(t.Context(), taskID, mutation, input)
	if err != nil || !bytes.Equal(replay.Body, firstResponse.Body) {
		t.Fatal("receipt was not durable")
	}
	page, err := s.TaskResultHistory(t.Context(), taskID, 0, 1)
	if err != nil || len(page.Entries) != 1 || !page.HasMore || page.CurrentResultID != secondID || page.Entries[0].Result.ID != firstID {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page, err = s.TaskResultHistory(t.Context(), taskID, page.NextAfter, 1)
	if err != nil || len(page.Entries) != 1 || page.HasMore || page.Entries[0].Result.ID != secondID {
		t.Fatalf("second page: %+v %v", page, err)
	}
}

func mustSnapshot(t *testing.T, s *Store) Snapshot {
	t.Helper()
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func mustEvents(t *testing.T, s *Store) []Event {
	t.Helper()
	events, err := s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func resultCode(err error, code string) bool {
	var public *domain.Error
	return errors.As(err, &public) && public.Code == code
}

func requireResultCode(t *testing.T, err error, code string) {
	t.Helper()
	if !resultCode(err, code) {
		t.Fatalf("error code=%q, want %q: %v", resultErrorCode(err), code, err)
	}
}

func resultErrorCode(err error) string {
	var public *domain.Error
	if errors.As(err, &public) {
		return public.Code
	}
	return "internal"
}

func addResultTask(t *testing.T, s *Store, projectID string) domain.Task {
	t.Helper()
	task := domain.Task{ID: uuid.NewString(), ProjectID: projectID, Title: "second", State: domain.Backlog, Version: 1}
	_, err := s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
		snap.Tasks = append(snap.Tasks, task)
		return Result{Status: 201, Body: []byte(`{}`)}, &Event{Action: "seed", ProjectID: projectID, TaskID: task.ID, Timestamp: "2026-11-20T00:00:00Z", Changes: []Change{{Object: "task", ID: task.ID}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestReviewBusinessKeyAuthorityAndLateHistory(t *testing.T) {
	s := openTest(t)
	project, firstTask := seed(t, s)
	secondTask := addResultTask(t, s, project.ID)
	first := mustSubmitResult(t, s, firstTask.ID, resultMutation("/first"), submitInput(resultRepoA, resultHeadA, ""))
	otherRepo := mustSubmitResult(t, s, secondTask.ID, resultMutation("/other-repo"), submitInput(resultRepoB, resultHeadA, ""))
	otherTask := mustSubmitResult(t, s, secondTask.ID, resultMutation("/other-task"), submitInput(resultRepoA, resultHeadA, otherRepo.ID))
	mutation := resultMutation("/review")
	input := reviewInput(first, "run-1")
	response, err := s.AttachReview(t.Context(), firstTask.ID, first.ID, mutation, input)
	if err != nil {
		t.Fatal(err)
	}
	review := mustAttachReview(t, s, firstTask.ID, first.ID, mutation, input)
	var original TaskReview
	if err := json.Unmarshal(response.Body, &original); err != nil || original.ID != review.ID {
		t.Fatal("request replay changed review")
	}
	events := mustEvents(t, s)
	duplicate, err := s.AttachReview(t.Context(), firstTask.ID, first.ID, resultMutation("/review-duplicate"), input)
	if err != nil || duplicate.Status != 200 {
		t.Fatalf("business replay: %+v %v", duplicate, err)
	}
	var repeated TaskReview
	if err := json.Unmarshal(duplicate.Body, &repeated); err != nil || repeated.ID != review.ID || !reflect.DeepEqual(events, mustEvents(t, s)) {
		t.Fatal("business replay duplicated review/event")
	}
	changed := input
	changed.ReportRef = "/reports/changed"
	_, err = s.AttachReview(t.Context(), firstTask.ID, first.ID, resultMutation("/changed"), changed)
	requireResultCode(t, err, "conflict")
	_, err = s.AttachReview(t.Context(), secondTask.ID, otherTask.ID, resultMutation("/transfer"), reviewInput(otherTask, "run-1"))
	requireResultCode(t, err, "conflict")
	otherReview := mustAttachReview(t, s, secondTask.ID, otherRepo.ID, resultMutation("/repo-b"), reviewInput(otherRepo, "run-1"))
	if otherReview.Repo != resultRepoB {
		t.Fatal("same task/run was not isolated by repo")
	}
	_, err = s.AttachReview(t.Context(), secondTask.ID, first.ID, resultMutation("/foreign"), reviewInput(first, "foreign"))
	requireResultCode(t, err, "not_found")
	for _, mutate := range []func(*AttachReviewInput){
		func(in *AttachReviewInput) { in.Repo = resultRepoB },
		func(in *AttachReviewInput) { in.Base = resultHeadB },
		func(in *AttachReviewInput) { in.Head = resultHeadB },
	} {
		forged := reviewInput(first, uuid.NewString())
		mutate(&forged)
		_, err := s.AttachReview(t.Context(), firstTask.ID, first.ID, resultMutation("/forged"), forged)
		requireResultCode(t, err, "conflict")
	}
	newer := mustSubmitResult(t, s, firstTask.ID, resultMutation("/newer"), submitInput(resultRepoA, resultHeadB, first.ID))
	late := mustAttachReview(t, s, firstTask.ID, first.ID, resultMutation("/late"), reviewInput(first, "run-late"))
	page, err := s.TaskResultHistory(t.Context(), firstTask.ID, 0, 100)
	if err != nil || page.CurrentResultID != newer.ID || len(page.Entries) != 4 {
		t.Fatalf("late history: %+v %v", page, err)
	}
	last := page.Entries[len(page.Entries)-1]
	if last.Kind != "review" || last.Review.ID != late.ID || last.Review.ResultID != first.ID || last.Review.Head != resultHeadA {
		t.Fatalf("late review moved to current candidate: %+v", last)
	}
}

func requireCurrentReviewPage(t *testing.T, page CurrentTaskReviewsPage, resultID, reviewID string, more bool) {
	t.Helper()
	if page.ResultID != resultID || len(page.Reviews) != 1 || page.HasMore != more ||
		page.Reviews[0].ID != reviewID || page.NextAfter != reviewID || !page.Reviews[0].EvidenceConsistent {
		t.Fatalf("unexpected current review page: %+v", page)
	}
}

func requireCurrentResultCandidate(t *testing.T, current CurrentTaskResult, resultID string) {
	t.Helper()
	if current.Result == nil || current.Result.ID != resultID || current.Reviews == nil || len(current.Reviews) != 0 {
		t.Fatalf("bounded current candidate view: %+v", current)
	}
}

func TestCurrentTaskResultSeparatesDecisionEvidenceFromHistory(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	empty, err := s.CurrentTaskResult(t.Context(), task.ID)
	if err != nil || empty.Result != nil || empty.Reviews == nil || len(empty.Reviews) != 0 {
		t.Fatalf("confirmed empty current view: %+v %v", empty, err)
	}
	first := mustSubmitResult(t, s, task.ID, resultMutation("/first-current"), submitInput(resultRepoA, resultHeadA, ""))
	second := mustSubmitResult(t, s, task.ID, resultMutation("/second-current"), submitInput(resultRepoA, resultHeadB, first.ID))
	oldReview := mustAttachReview(t, s, task.ID, first.ID, resultMutation("/old-current-review"), reviewInput(first, "old-current"))
	currentOne := mustAttachReview(t, s, task.ID, second.ID, resultMutation("/current-one"), reviewInput(second, "current-one"))
	currentTwo := mustAttachReview(t, s, task.ID, second.ID, resultMutation("/current-two"), reviewInput(second, "current-two"))
	current, err := s.CurrentTaskResult(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireCurrentResultCandidate(t, current, second.ID)
	firstPage, err := s.CurrentTaskReviews(t.Context(), task.ID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	requireCurrentReviewPage(t, firstPage, second.ID, currentOne.ID, true)
	secondPage, err := s.CurrentTaskReviews(t.Context(), task.ID, firstPage.NextAfter, 1)
	if err != nil {
		t.Fatal(err)
	}
	requireCurrentReviewPage(t, secondPage, second.ID, currentTwo.ID, false)
	for _, review := range append(firstPage.Reviews, secondPage.Reviews...) {
		if review.ResultID != second.ID || review.ID == oldReview.ID {
			t.Fatalf("superseded review entered current pages: %+v", review)
		}
	}
	if _, err := s.CurrentTaskReviews(t.Context(), task.ID, oldReview.ID, 1); !resultCode(err, "invalid_request") {
		t.Fatalf("superseded review cursor accepted: %v", err)
	}
	history, err := s.TaskResultHistory(t.Context(), task.ID, 0, 1)
	if err != nil || len(history.Entries) != 1 || !history.HasMore || history.Entries[0].Result.ID != first.ID {
		t.Fatalf("bounded history changed while reading current evidence: %+v %v", history, err)
	}
}

func TestReviewCompletenessRequiresExactRespondingPairs(t *testing.T) {
	result := TaskResult{ID: uuid.NewString(), TaskID: uuid.NewString(), Repo: resultRepoA, Base: resultBase, Head: resultHeadA}
	valid := reviewInput(result, "complete")
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), valid); err != nil {
		t.Fatalf("responding required pair rejected: %v", err)
	}
	validIncomplete := valid
	validIncomplete.Complete = false
	validIncomplete.IncompleteReasons = []string{"operator marked evidence incomplete"}
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), validIncomplete); err == nil {
		t.Fatal("complete required pair accepted an incomplete summary")
	}
	tests := []struct {
		name   string
		mutate func(*AttachReviewInput)
	}{
		{name: "empty requirements cannot be complete", mutate: func(in *AttachReviewInput) {
			in.RequiredSources = []ReviewRequirement{}
			in.Sources[0].Required = false
		}},
		{name: "required pair missing", mutate: func(in *AttachReviewInput) { in.Sources = []ReviewSource{} }},
		{name: "required executor mismatch", mutate: func(in *AttachReviewInput) {
			in.Sources[0].Executor = "pi"
		}},
		{name: "required source degraded", mutate: func(in *AttachReviewInput) {
			in.Sources[0].Responded = false
			in.Sources[0].Degraded = true
		}},
		{name: "responded and degraded", mutate: func(in *AttachReviewInput) { in.Sources[0].Degraded = true }},
		{name: "neither responded nor degraded", mutate: func(in *AttachReviewInput) { in.Sources[0].Responded = false }},
		{name: "required flag omitted", mutate: func(in *AttachReviewInput) { in.Sources[0].Required = false }},
		{name: "optional source marked required", mutate: func(in *AttachReviewInput) {
			in.Sources = append(in.Sources, ReviewSource{Name: "optional", Executor: "pi", Required: true, Responded: true})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := reviewInput(result, "invalid")
			test.mutate(&input)
			if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), input); err == nil {
				t.Fatal("contradictory source state accepted")
			}
		})
	}
	empty := reviewInput(result, "empty")
	empty.Complete = false
	empty.RequiredSources = []ReviewRequirement{}
	empty.Sources[0].Required = false
	empty.IncompleteReasons = []string{"required source list is empty"}
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), empty); err != nil {
		t.Fatalf("explicit empty-requirement incomplete review rejected: %v", err)
	}
	degraded := reviewInput(result, "degraded")
	degraded.Complete = false
	degraded.Sources[0].Responded = false
	degraded.Sources[0].Degraded = true
	degraded.IncompleteReasons = []string{"required source degraded"}
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), degraded); err != nil {
		t.Fatalf("valid degraded incomplete review rejected: %v", err)
	}
	degraded.Sources[0].Findings = 1
	degraded.Findings = 1
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), degraded); err != nil {
		t.Fatalf("degraded reporter's consistent counts were rejected: %v", err)
	}
	degraded.Findings = 0
	if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), degraded); err == nil {
		t.Fatal("per-source findings exceeded the review total")
	}
}

func TestReviewSourceCountsCannotExceedTotals(t *testing.T) {
	result := TaskResult{ID: uuid.NewString(), TaskID: uuid.NewString(), Repo: resultRepoA, Base: resultBase, Head: resultHeadA}
	for _, test := range []struct {
		name  string
		apply func(*AttachReviewInput)
	}{
		{name: "findings", apply: func(in *AttachReviewInput) { in.Sources[0].Findings = 1 }},
		{name: "pre-existing", apply: func(in *AttachReviewInput) { in.Sources[0].PreExisting = 1 }},
		{name: "open questions", apply: func(in *AttachReviewInput) { in.Sources[0].OpenQuestions = 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := reviewInput(result, "counts")
			test.apply(&input)
			if err := validateAttachReview(result.TaskID, result.ID, uuid.NewString(), input); err == nil {
				t.Fatal("source count greater than its total passed validation")
			}
		})
	}
}

func TestReviewTotalsRequireCoverageButAllowSharedItems(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/coverage-submit"), submitInput(resultRepoA, resultHeadA, ""))
	for _, test := range []struct {
		name string
		set  func(*AttachReviewInput, int, int, int)
	}{
		{"findings", func(in *AttachReviewInput, total, a, b int) {
			in.Findings, in.Sources[0].Findings, in.Sources[1].Findings = total, a, b
		}},
		{"pre-existing", func(in *AttachReviewInput, total, a, b int) {
			in.PreExisting, in.Sources[0].PreExisting, in.Sources[1].PreExisting = total, a, b
		}},
		{"open questions", func(in *AttachReviewInput, total, a, b int) {
			in.OpenQuestions, in.Sources[0].OpenQuestions, in.Sources[1].OpenQuestions = total, a, b
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := reviewInput(result, test.name)
			input.Sources = append(input.Sources, ReviewSource{Name: "optional", Executor: "pi", Responded: true})
			mutation := resultMutation("/" + test.name)
			for _, attributed := range []int{0, 1} {
				test.set(&input, 2, attributed, 0)
				_, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, input)
				requireResultCode(t, err, "invalid_request")
			}
			legacy, receipt := seedLegacyReviewReceipt(t, s, task.ID, result, mutation, input)
			page, err := s.CurrentTaskReviews(t.Context(), task.ID, "", 20)
			if err != nil || len(page.Reviews) == 0 {
				t.Fatalf("legacy attribution page: %+v %v", page, err)
			}
			projected := page.Reviews[len(page.Reviews)-1]
			if projected.ID != legacy.ID || projected.EvidenceConsistent {
				t.Fatalf("legacy under-attribution not marked inconsistent: %+v", projected)
			}
			replay, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, input)
			if err != nil || !bytes.Equal(replay.Body, receipt.Body) {
				t.Fatalf("legacy under-attribution receipt changed: %+v %v", replay, err)
			}
			for _, attributed := range []int{1, 2} {
				input.Run = test.name + "/" + strconv.Itoa(attributed)
				test.set(&input, 2, attributed, 1)
				if review := mustAttachReview(t, s, task.ID, result.ID, resultMutation("/shared"), input); !review.EvidenceConsistent {
					t.Fatalf("consistent multi-source attribution rejected: %+v", review)
				}
			}
		})
	}
}

func TestLegacyOutOfRangeCountersRemainReadableAndInconsistent(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/legacy-counter-submit"), submitInput(resultRepoA, resultHeadA, ""))
	legacy := reviewInput(result, "legacy-counter")
	legacy.Findings = MaxReviewCounter + 1
	legacy.PreExisting = MaxReviewCounter + 1
	legacy.OpenQuestions = MaxReviewCounter + 1
	legacy.Sources[0].Findings = MaxReviewCounter + 1
	legacy.Sources[0].PreExisting = MaxReviewCounter + 1
	legacy.Sources[0].OpenQuestions = MaxReviewCounter + 1
	mutation := resultMutation("/legacy-counter-review")
	legacyReview, receipt := seedLegacyReviewReceipt(t, s, task.ID, result, mutation, legacy)
	page, err := s.CurrentTaskReviews(t.Context(), task.ID, "", 20)
	if err != nil || len(page.Reviews) != 1 {
		t.Fatalf("legacy counter page was lost: %+v %v", page, err)
	}
	projected := page.Reviews[0]
	if projected.EvidenceConsistent || projected.Findings != MaxReviewCounter || projected.PreExisting != MaxReviewCounter ||
		projected.OpenQuestions != MaxReviewCounter || projected.Sources[0].Findings != MaxReviewCounter ||
		projected.Sources[0].PreExisting != MaxReviewCounter || projected.Sources[0].OpenQuestions != MaxReviewCounter {
		t.Fatalf("legacy counters were not saturated and marked inconsistent: %+v", projected)
	}
	replayed, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, legacy)
	if err != nil || !bytes.Equal(replayed.Body, receipt.Body) {
		t.Fatalf("legacy out-of-range exact receipt did not replay: %+v %v", replayed, err)
	}
	var stored string
	if err := s.db.QueryRowContext(t.Context(), "SELECT record FROM task_reviews WHERE id=?", legacyReview.ID).Scan(&stored); err != nil || stored != string(receipt.Body) {
		t.Fatalf("counter projection rewrote legacy storage: %v", err)
	}
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/fresh-counter"), legacy)
	requireResultCode(t, err, "invalid_request")
}

func TestReviewEvidenceConsistencyChecksRequiredPairsAndFlags(t *testing.T) {
	input := reviewInput(TaskResult{Repo: resultRepoA, Base: resultBase, Head: resultHeadA}, "projection")
	valid := TaskReview{Complete: input.Complete, RequiredSources: input.RequiredSources, Sources: input.Sources}
	for _, test := range []struct {
		name   string
		mutate func(*TaskReview)
	}{
		{name: "missing required pair", mutate: func(review *TaskReview) { review.Sources = []ReviewSource{} }},
		{name: "executor mismatch", mutate: func(review *TaskReview) { review.Sources[0].Executor = "pi" }},
		{name: "required pair degraded", mutate: func(review *TaskReview) {
			review.Sources[0].Responded = false
			review.Sources[0].Degraded = true
		}},
		{name: "required flag mismatch", mutate: func(review *TaskReview) { review.Sources[0].Required = false }},
		{name: "responded and degraded", mutate: func(review *TaskReview) { review.Sources[0].Degraded = true }},
		{name: "neither responded nor degraded", mutate: func(review *TaskReview) { review.Sources[0].Responded = false }},
		{name: "optional marked required", mutate: func(review *TaskReview) {
			review.Sources = append(review.Sources, ReviewSource{Name: "optional", Executor: "pi", Required: true, Responded: true})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			review := valid
			review.RequiredSources = append([]ReviewRequirement{}, valid.RequiredSources...)
			review.Sources = append([]ReviewSource{}, valid.Sources...)
			test.mutate(&review)
			if reviewEvidenceConsistent(review) {
				t.Fatal("contradictory legacy evidence projected as consistent")
			}
		})
	}
	if !reviewEvidenceConsistent(valid) {
		t.Fatal("valid responding required pair projected as inconsistent")
	}
}

func TestLegacyReviewReceiptReplaysBeforeNewCompleteness(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/legacy-submit"), submitInput(resultRepoA, resultHeadA, ""))
	legacy := reviewInput(result, "legacy-complete-missing")
	legacy.Sources = []ReviewSource{}
	mutation := resultMutation("/legacy-review")
	legacyReview, response := seedLegacyReviewReceipt(t, s, task.ID, result, mutation, legacy)
	if bytes.Contains(response.Body, []byte("evidence_consistent")) {
		t.Fatal("legacy fixture unexpectedly uses the current receipt schema")
	}
	if err := validateReviewCompleteness(legacy); err == nil {
		t.Fatal("legacy fixture unexpectedly satisfies current completeness rules")
	}
	replayed, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, legacy)
	if err != nil || replayed.Status != 201 || !bytes.Equal(replayed.Body, response.Body) {
		t.Fatalf("legacy exact receipt did not replay: %+v %v", replayed, err)
	}
	page, err := s.CurrentTaskReviews(t.Context(), task.ID, "", 20)
	if err != nil || len(page.Reviews) != 1 || page.Reviews[0].EvidenceConsistent {
		t.Fatalf("legacy complete review was not flagged inconsistent: %+v %v", page, err)
	}
	var stored string
	if err := s.db.QueryRowContext(t.Context(), "SELECT record FROM task_reviews WHERE id=?", legacyReview.ID).Scan(&stored); err != nil || stored != string(response.Body) {
		t.Fatalf("read projection mutated legacy record: %v", err)
	}
	changed := legacy
	changed.ReportRef = "/reports/changed-legacy"
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, mutation, changed)
	requireResultCode(t, err, "conflict")
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/fresh-legacy-shape"), legacy)
	requireResultCode(t, err, "invalid_request")
}

func TestLegacyMixedRosterReceiptReplayRequiresExactHashAndKey(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/legacy-normalized-submit"), submitInput(resultRepoA, resultHeadA, ""))
	legacy := reviewInput(result, "legacy-normalized")
	legacy.Complete = false
	legacy.RequiredSources = append(legacy.RequiredSources,
		ReviewRequirement{Name: "required-degraded", Executor: "codex"},
		ReviewRequirement{Name: "required-missing", Executor: "pi"})
	legacy.Sources = append(legacy.Sources,
		ReviewSource{Name: "optional-reported", Executor: "pi", Degraded: true},
		ReviewSource{Name: "required-degraded", Executor: "codex", Required: true, Degraded: true})
	legacy.IncompleteReasons = []string{
		"required source degraded: required-degraded (codex)",
		"required source missing: required-missing (pi)",
	}
	current := legacy
	current.Sources = []ReviewSource{
		{Name: "optional-manifest", Executor: "codex", Degraded: true},
		legacy.Sources[0], legacy.Sources[1], legacy.Sources[2],
		{Name: "required-missing", Executor: "pi", Required: true, Degraded: true},
	}
	current.IncompleteReasons = []string{
		"required source degraded: required-degraded (codex)",
		"required source degraded: required-missing (pi)",
	}
	mutation := resultMutation("/legacy-normalized-review")
	_, original := seedLegacyReviewReceipt(t, s, task.ID, result, mutation, legacy)
	events := mustEvents(t, s)
	replayed, err := s.ReplayLegacyReview(t.Context(), task.ID, result.ID, mutation, legacy)
	if err != nil || replayed.Status != 201 || !bytes.Equal(replayed.Body, original.Body) {
		t.Fatalf("old mixed-roster receipt did not recover with exact payload: %+v %v", replayed, err)
	}
	for _, shape := range []AttachReviewInput{legacy, current} {
		fresh := resultMutation(mutation.Path)
		_, err = s.ReplayLegacyReview(t.Context(), task.ID, result.ID, fresh, shape)
		requireResultCode(t, err, "not_found")
		var receipts int
		if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM mutation_receipts WHERE key=?", fresh.Key).Scan(&receipts); err != nil || receipts != 0 {
			t.Fatalf("legacy lookup wrote a receipt: %d %v", receipts, err)
		}
	}
	for _, shape := range []AttachReviewInput{legacy, current} {
		for _, change := range []struct {
			name   string
			mutate func(*AttachReviewInput)
		}{
			{"required flag", func(in *AttachReviewInput) { in.Sources[len(in.Sources)-1].Required = false }},
			{"source flags", func(in *AttachReviewInput) {
				last := &in.Sources[len(in.Sources)-1]
				last.Responded, last.Degraded = true, false
			}},
			{"reasons", func(in *AttachReviewInput) { in.IncompleteReasons = []string{"unrelated reason"} }},
		} {
			t.Run(change.name, func(t *testing.T) {
				changed := shape
				changed.Sources = append([]ReviewSource{}, shape.Sources...)
				change.mutate(&changed)
				_, err := s.ReplayLegacyReview(t.Context(), task.ID, result.ID, mutation, changed)
				requireResultCode(t, err, "conflict")
				_, err = s.AttachReview(t.Context(), task.ID, result.ID, mutation, changed)
				requireResultCode(t, err, "conflict")
			})
		}
	}
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, mutation, current)
	requireResultCode(t, err, "conflict")
	wrongPath := mutation
	wrongPath.Path += "/other"
	_, err = s.ReplayLegacyReview(t.Context(), task.ID, result.ID, wrongPath, legacy)
	requireResultCode(t, err, "conflict")
	if !reflect.DeepEqual(events, mustEvents(t, s)) {
		t.Fatal("legacy lookup wrote events")
	}
}

func TestCurrentReceiptRejectsChangedOptionalDegradedRoster(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/current-normalized-submit"), submitInput(resultRepoA, resultHeadA, ""))
	original := reviewInput(result, "current-normalized")
	mutation := resultMutation("/current-normalized-review")
	response, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, original)
	if err != nil || !bytes.Contains(response.Body, []byte("evidence_consistent")) {
		t.Fatalf("current receipt fixture: %+v %v", response, err)
	}
	_, err = s.ReplayLegacyReview(t.Context(), task.ID, result.ID, mutation, original)
	requireResultCode(t, err, "not_found")
	replayed, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, original)
	if err != nil || replayed.Status != response.Status || !bytes.Equal(replayed.Body, response.Body) {
		t.Fatalf("current exact receipt did not replay: %+v %v", replayed, err)
	}
	changed := original
	changed.Sources = append(changed.Sources, ReviewSource{Name: "optional", Executor: "pi", Degraded: true})
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, mutation, changed)
	requireResultCode(t, err, "conflict")
}

func TestArchivedProjectAllowsReceiptReplayButRejectsFreshResultWrites(t *testing.T) {
	s := openTest(t)
	project, task := seed(t, s)
	submitMutation := resultMutation("/archived-submit")
	submit := submitInput(resultRepoA, resultHeadA, "")
	first := mustSubmitResult(t, s, task.ID, submitMutation, submit)
	reviewMutation := resultMutation("/archived-review")
	review := reviewInput(first, "archived-review")
	mustAttachReview(t, s, task.ID, first.ID, reviewMutation, review)
	if _, err := s.db.ExecContext(t.Context(), "UPDATE projects SET archived=1 WHERE id=?", project.ID); err != nil {
		t.Fatal(err)
	}
	beforeEvents := mustEvents(t, s)
	if replay, err := s.SubmitResult(t.Context(), task.ID, submitMutation, submit); err != nil || replay.Status != 201 {
		t.Fatalf("archived submit receipt did not replay: %+v %v", replay, err)
	}
	if replay, err := s.AttachReview(t.Context(), task.ID, first.ID, reviewMutation, review); err != nil || replay.Status != 201 {
		t.Fatalf("archived review receipt did not replay: %+v %v", replay, err)
	}
	_, err := s.SubmitResult(t.Context(), task.ID, resultMutation("/archived-fresh"), submitInput(resultRepoA, resultHeadB, first.ID))
	requireResultCode(t, err, "conflict")
	_, err = s.AttachReview(t.Context(), task.ID, first.ID, resultMutation("/archived-fresh-review"), reviewInput(first, "fresh-review"))
	requireResultCode(t, err, "conflict")
	_, err = s.AttachReview(t.Context(), task.ID, first.ID, resultMutation("/archived-business-replay"), review)
	requireResultCode(t, err, "conflict")
	current, ok := mustSnapshot(t, s).CurrentResultID(task.ID)
	if !ok || current != first.ID || countRows(t, s, "task_results") != 1 || countRows(t, s, "task_reviews") != 1 || !reflect.DeepEqual(beforeEvents, mustEvents(t, s)) {
		t.Fatal("archived project accepted a fresh result/review mutation")
	}
}

func TestResultValidationAndAtomicReceiptRollback(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	beforeEvents := mustEvents(t, s)
	invalid := submitInput("relative/.git", resultHeadA, "")
	_, err := s.SubmitResult(t.Context(), task.ID, resultMutation("/invalid"), invalid)
	requireResultCode(t, err, "invalid_request")
	for _, sha := range []string{"abc", strings.Repeat("A", 40), strings.Repeat("g", 40)} {
		invalid = submitInput(resultRepoA, sha, "")
		_, err := s.SubmitResult(t.Context(), task.ID, resultMutation("/invalid"), invalid)
		requireResultCode(t, err, "invalid_request")
	}
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER fail_result_receipt BEFORE INSERT ON mutation_receipts
		WHEN NEW.path='/result-rollback' BEGIN SELECT RAISE(ABORT,'injected result receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitResult(t.Context(), task.ID, resultMutation("/result-rollback"), submitInput(resultRepoA, resultHeadA, "")); err == nil {
		t.Fatal("injected result receipt failure succeeded")
	}
	if countRows(t, s, "task_results") != 0 || countRows(t, s, "current_task_results") != 0 || !reflect.DeepEqual(beforeEvents, mustEvents(t, s)) {
		t.Fatal("result receipt failure left partial state")
	}
	result := mustSubmitResult(t, s, task.ID, resultMutation("/valid"), submitInput(resultRepoA, resultHeadA, ""))
	invalidReview := reviewInput(result, "invalid")
	invalidReview.RangeSource = "artifact"
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/invalid-review"), invalidReview)
	requireResultCode(t, err, "invalid_request")
	invalidReview = reviewInput(result, "invalid")
	invalidReview.RequiredSources = append(invalidReview.RequiredSources, invalidReview.RequiredSources[0])
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/invalid-review"), invalidReview)
	requireResultCode(t, err, "invalid_request")
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER fail_review_receipt BEFORE INSERT ON mutation_receipts
		WHEN NEW.path='/review-rollback' BEGIN SELECT RAISE(ABORT,'injected review receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/review-rollback"), reviewInput(result, "rollback")); err == nil {
		t.Fatal("injected review receipt failure succeeded")
	}
	if countRows(t, s, "task_reviews") != 0 {
		t.Fatal("review receipt failure left partial state")
	}
}
