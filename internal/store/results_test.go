package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
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

func TestLegacyReviewReceiptReplaysBeforeNewCompleteness(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/legacy-submit"), submitInput(resultRepoA, resultHeadA, ""))
	legacy := reviewInput(result, "legacy-complete-empty")
	legacy.RequiredSources = []ReviewRequirement{}
	legacy.Sources = []ReviewSource{}
	legacyReview := TaskReview{ID: uuid.NewString(), TaskID: task.ID, ResultID: result.ID,
		Repo: result.Repo, Base: result.Base, Head: result.Head, RevmuxTask: legacy.RevmuxTask, Run: legacy.Run,
		RangeSource: "operator", ReportRef: legacy.ReportRef, Complete: true,
		RequiredSources: []ReviewRequirement{}, Sources: []ReviewSource{}, IncompleteReasons: []string{}, CreatedAt: "2026-11-20T12:00:00Z"}
	encoded, err := json.Marshal(legacyReview)
	if err != nil {
		t.Fatal(err)
	}
	mutation := resultMutation("/legacy-review")
	mutation.Hash = observerHash(legacy)
	response, err := resultResponse(201, legacyReview)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO task_reviews(id,task_id,result_id,repo,revmux_task,run,payload_hash,record)
		VALUES(?,?,?,?,?,?,?,?)`, legacyReview.ID, task.ID, result.ID, result.Repo, legacy.RevmuxTask, legacy.Run, observerHash(legacy), string(encoded)); err != nil {
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
	if err := s.db.QueryRowContext(t.Context(), "SELECT record FROM task_reviews WHERE id=?", legacyReview.ID).Scan(&stored); err != nil || stored != string(encoded) {
		t.Fatalf("read projection mutated legacy record: %v", err)
	}
	changed := legacy
	changed.ReportRef = "/reports/changed-legacy"
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, mutation, changed)
	requireResultCode(t, err, "conflict")
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/fresh-legacy-shape"), legacy)
	requireResultCode(t, err, "invalid_request")
}

func TestLegacyOptionalDegradedReceiptReplayRequiresExactHashAndKey(t *testing.T) {
	s := openTest(t)
	_, task := seed(t, s)
	result := mustSubmitResult(t, s, task.ID, resultMutation("/legacy-normalized-submit"), submitInput(resultRepoA, resultHeadA, ""))
	legacy := reviewInput(result, "legacy-normalized")
	current := legacy
	current.Sources = []ReviewSource{
		{Name: "optional", Executor: "pi", Responded: false, Degraded: true},
		legacy.Sources[0],
	}
	if hash, ok := legacyNormalizedReviewHash(current); !ok || hash != observerHash(legacy) {
		t.Fatal("old normalized payload was not reconstructed exactly")
	}
	mutation := resultMutation("/legacy-normalized-review")
	original, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, legacy)
	if err != nil {
		t.Fatal(err)
	}
	events := mustEvents(t, s)
	replayed, err := s.AttachReview(t.Context(), task.ID, result.ID, mutation, current)
	if err != nil || replayed.Status != 201 || !bytes.Equal(replayed.Body, original.Body) || !reflect.DeepEqual(events, mustEvents(t, s)) {
		t.Fatalf("old receipt did not recover through new normalization: %+v %v", replayed, err)
	}
	_, err = s.AttachReview(t.Context(), task.ID, result.ID, resultMutation("/fresh-new-normalization"), current)
	requireResultCode(t, err, "conflict")
	changed := current
	changed.ReportRef = "/reports/not-the-original"
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
