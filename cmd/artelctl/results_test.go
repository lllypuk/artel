package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/revmuximport"
	"github.com/lllypuk/artel/internal/store"
)

type cliResultHarness struct {
	root      string
	home      string
	endpoint  string
	tokenPath string
	project   domain.Project
	task      domain.Task
}

func newCLIResultHarness(t *testing.T) *cliResultHarness {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	root := t.TempDir()
	binary := filepath.Join(root, "arteld")
	// #nosec G204 -- Builds the daemon from this source into a test-private path.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../arteld")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build daemon: %v: %s", err, output)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "private", "owner.token")
	// #nosec G204 -- Executes only the test-built daemon with an ephemeral loopback listener.
	daemon := exec.CommandContext(ctx, binary, "--listen", "127.0.0.1:0", "--data-dir", filepath.Join(root, "data"), "--token-file", tokenPath)
	daemon.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_DATA_HOME="+filepath.Join(home, "data"))
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	daemon.Stderr = &stderr
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("daemon did not announce its address: %s", stderr.String())
	}
	address := strings.TrimPrefix(scanner.Text(), "arteld: listening ")
	h := &cliResultHarness{root: root, home: home, endpoint: "http://" + address, tokenPath: tokenPath}
	projectRoot := filepath.Join(root, "project")
	if err := os.Mkdir(projectRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	projectBody, err := json.Marshal(map[string]string{"name": "Fictional results", "root_path": projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	h.ownerRequest(t, http.MethodPost, "/v1/projects", uuid.NewString(), projectBody, http.StatusCreated, &h.project)
	taskBody := []byte(`{"title":"Fictional candidate"}`)
	h.ownerRequest(t, http.MethodPost, "/v1/projects/"+h.project.ID+"/tasks", uuid.NewString(), taskBody, http.StatusCreated, &h.task)
	return h
}

func (h *cliResultHarness) ownerRequest(t *testing.T, method, path, key string, body []byte, want int, target any) []byte {
	t.Helper()
	token, err := local.ReadToken(h.tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, h.endpoint+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	// #nosec G704 -- Destination is this test's ephemeral loopback daemon.
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil || response.StatusCode != want {
		t.Fatalf("%s %s status=%d body=%s err=%v", method, path, response.StatusCode, data, err)
	}
	if target != nil && json.Unmarshal(data, target) != nil {
		t.Fatalf("invalid response: %s", data)
	}
	return data
}

func (h *cliResultHarness) call(t *testing.T, want int, args ...string) (string, string) {
	t.Helper()
	args = append(args, "--endpoint", h.endpoint, "--token-file", h.tokenPath)
	var out, diagnostic bytes.Buffer
	if code := run(args, &out, &diagnostic); code != want {
		t.Fatalf("exit=%d want=%d stderr=%s", code, want, diagnostic.String())
	}
	return out.String(), diagnostic.String()
}

func (h *cliResultHarness) git(t *testing.T, directory string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", directory}, args...)
	// #nosec G204 -- Fixed Git executable and test-owned repositories, without a shell.
	command := exec.CommandContext(t.Context(), "git", commandArgs...)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + h.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Fictional Owner", "GIT_AUTHOR_EMAIL=owner@example.invalid",
		"GIT_COMMITTER_NAME=Fictional Owner", "GIT_COMMITTER_EMAIL=owner@example.invalid",
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func (h *cliResultHarness) repository(t *testing.T) (repo, linked, base, head string) {
	t.Helper()
	repo = filepath.Join(h.root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	h.git(t, repo, "init", "-q")
	h.git(t, repo, "config", "user.name", "Fictional Owner")
	h.git(t, repo, "config", "user.email", "owner@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "candidate.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.git(t, repo, "add", "candidate.txt")
	h.git(t, repo, "commit", "-q", "-m", "Fictional base")
	base = h.git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "candidate.txt"), []byte("head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.git(t, repo, "commit", "-q", "-am", "Fictional head")
	head = h.git(t, repo, "rev-parse", "HEAD")
	linked = filepath.Join(h.root, "linked")
	h.git(t, repo, "worktree", "add", "-q", "--detach", linked, head)
	return repo, linked, base, head
}

func writeCLIReport(t *testing.T, root, task, run string) string {
	t.Helper()
	directory := filepath.Join(root, "report")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	agent := func(name, executor string) map[string]any {
		return map[string]any{"name": name, "lenses": []string{"bugs"}, "executor": executor,
			"requested_model": "fictional", "actual_model": "fictional", "effort": "high",
			"tokens": 1, "raised": 0, "degraded": false}
	}
	manifest := map[string]any{
		"task": task, "run": run, "profile": "pi-codex", "scope_path": "fictional-scope",
		"started_at": "2026-11-20T11:00:00Z", "finished_at": "2026-11-20T12:00:00Z",
		"duration_ms": 1, "tokens": 2, "agents": []any{agent("reviewer", "pi"), agent("optional", "codex")},
		"degraded": nil, "prompts": []any{}, "stages": []any{},
	}
	findings := map[string]any{
		"scope":   map[string]any{"task": task, "run": run, "scope_path": "fictional-scope"},
		"sources": map[string]any{"expected": 2, "reported": 2, "degraded": []string{}, "agents": []any{agent("reviewer", "pi"), agent("optional", "codex")}},
		"findings": []any{map[string]any{"id": "fictional-1", "file": "fictional.go", "line": 1, "end_line": 0,
			"severity": "minor", "confidence": 100, "title": "Synthetic raw title must not cross API", "body": "Synthetic raw body must not cross API",
			"fix": "Fictional fix", "sources": []string{"reviewer"}, "lenses": []string{"bugs"}, "verdict": "confirmed"}},
		"open_questions": []any{map[string]any{"id": "fictional-question", "sources": []string{"optional"}}},
		"pre_existing":   []any{}, "immaterial": []any{},
		"stats": map[string]any{"started_at": "2026-11-20T11:00:00Z", "finished_at": "2026-11-20T12:00:00Z", "duration_ms": 1, "tokens": 2, "stages": []any{}},
	}
	for name, value := range map[string]any{"manifest.json": manifest, "findings.json": findings} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func (h *cliResultHarness) submitArgs(repo, base, head, binding, run, expected, requestID string) []string {
	return []string{"results", "submit", "--task-id", h.task.ID, "--repo", repo, "--base", base, "--head", head,
		"--summary", "Fictional owner summary", "--expected-result-id", expected, "--binding", binding,
		"--revmux-task", "fictional-task", "--run", run, "--request-id", requestID}
}

func (h *cliResultHarness) attachArgs(resultID, binding, report, requestID string) []string {
	return []string{"reviews", "attach", "--task-id", h.task.ID, "--result-id", resultID,
		"--binding", binding, "--report-dir", report, "--required-source", "reviewer=pi", "--request-id", requestID}
}

func assertCLIReview(t *testing.T, output string) store.TaskReview {
	t.Helper()
	if strings.Contains(output, "Synthetic raw") {
		t.Fatal("raw finding text appeared in CLI response")
	}
	var review store.TaskReview
	if err := json.Unmarshal([]byte(output), &review); err != nil {
		t.Fatal(err)
	}
	if !review.Complete || review.RangeSource != "operator" || review.Findings != 1 || review.OpenQuestions != 1 || len(review.Sources) != 2 {
		t.Fatalf("unexpected attached review: %+v", review)
	}
	for _, source := range review.Sources {
		if !source.Responded {
			t.Fatalf("reported zero-finding source treated as missing: %+v", source)
		}
		if source.Name == "optional" && source.Findings != 0 {
			t.Fatalf("unexpected optional source findings: %+v", source)
		}
	}
	return review
}

func assertCLIBinding(t *testing.T, path string, result store.TaskResult, base, head string) revmuximport.Binding {
	t.Helper()
	binding, err := revmuximport.LoadBinding(path)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Repo != result.Repo || binding.Base != base || binding.Head != head || binding.RevmuxTask != "fictional-task" ||
		binding.Run != "01-review" || binding.RangeSource != "operator" {
		t.Fatalf("unexpected pre-run binding: %+v", binding)
	}
	return binding
}

func TestResultsAndReviewsCLIWithTemporaryDaemon(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, linked, base, head := h.repository(t)
	bindingPath := filepath.Join(h.root, "bindings", "first.json")

	// Literal commits and object types are checked locally before owner HTTP.
	invalidSHA := strings.Repeat("f", 40)
	blobSHA := h.git(t, repo, "hash-object", filepath.Join(repo, "candidate.txt"))
	h.git(t, repo, "tag", "-a", "-m", "Fictional annotated tag", "fictional-tag", head)
	tagSHA := h.git(t, repo, "rev-parse", "fictional-tag")
	for _, args := range [][]string{
		h.submitArgs(repo, "HEAD", head, bindingPath, "invalid-ref", "", uuid.NewString()),
		h.submitArgs(repo, invalidSHA, head, bindingPath, "missing-commit", "", uuid.NewString()),
		h.submitArgs(repo, blobSHA, head, bindingPath, "blob-not-commit", "", uuid.NewString()),
		h.submitArgs(repo, tagSHA, head, bindingPath, "tag-not-commit", "", uuid.NewString()),
	} {
		h.call(t, 2, args...)
	}

	firstArgs := h.submitArgs(repo, base, head, bindingPath, "01-review", "", uuid.NewString())
	firstOutput, _ := h.call(t, 0, firstArgs...)
	if replay, _ := h.call(t, 0, firstArgs...); replay != firstOutput {
		t.Fatal("submission request-id replay changed the response")
	}
	var first store.TaskResult
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil {
		t.Fatal(err)
	}
	binding := assertCLIBinding(t, bindingPath, first, base, head)
	linkedRepo, err := validateGitCandidate(linked, base, head)
	if err != nil || linkedRepo != binding.Repo {
		t.Fatalf("linked worktree identity=%q binding=%q err=%v", linkedRepo, binding.Repo, err)
	}

	report := writeCLIReport(t, h.root, "fictional-task", "01-review")
	attachArgs := h.attachArgs(first.ID, bindingPath, report, uuid.NewString())
	reviewOutput, _ := h.call(t, 0, attachArgs...)
	assertCLIReview(t, reviewOutput)
	replayOutput, _ := h.call(t, 0, attachArgs...)
	if replayOutput != reviewOutput {
		t.Fatal("same request-id did not return original response")
	}
	businessReplay, _ := h.call(t, 0, h.attachArgs(first.ID, bindingPath, report, uuid.NewString())...)
	if businessReplay != reviewOutput {
		t.Fatal("business replay did not return original review")
	}
	var history store.TaskResultHistoryPage
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if history.CurrentResultID != first.ID || len(history.Entries) != 2 {
		t.Fatalf("repeat duplicated result history: %+v", history)
	}

	// A linked worktree has the same common-dir identity and can submit a later
	// candidate without making the first review current for the new submission.
	secondBinding := filepath.Join(h.root, "bindings", "second.json")
	secondOutput, _ := h.call(t, 0, h.submitArgs(linked, base, head, secondBinding, "02-review", first.ID, uuid.NewString())...)
	var second store.TaskResult
	if json.Unmarshal([]byte(secondOutput), &second) != nil || second.Repo != first.Repo {
		t.Fatalf("linked submission changed repo identity: %s", secondOutput)
	}
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if history.CurrentResultID != second.ID || len(history.Entries) != 3 || history.Entries[1].Review.ResultID != first.ID {
		t.Fatalf("late history lost original review: %+v", history)
	}
}

func TestGitCandidateEnvironmentIsolation(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, _, base, head := h.repository(t)
	for _, variable := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_INDEX_FILE", "GIT_NAMESPACE",
		"GIT_CEILING_DIRECTORIES", "GIT_DISCOVERY_ACROSS_FILESYSTEM", "GIT_CONFIG_PARAMETERS",
	} {
		t.Setenv(variable, filepath.Join(h.root, "inherited-override"))
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.repositoryFormatVersion")
	t.Setenv("GIT_CONFIG_VALUE_0", "invalid")
	canonical, err := validateGitCandidate(repo, base, head)
	if err != nil || canonical != filepath.Join(repo, ".git") {
		t.Fatalf("inherited Git environment changed selected repository: %q %v", canonical, err)
	}
	environment := isolatedGitEnvironment()
	if !strings.Contains(strings.Join(environment, "\n"), "GIT_NO_LAZY_FETCH=1") {
		t.Fatal("Git validation did not disable lazy remote fetching")
	}
	for _, entry := range environment {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_OBJECT_DIRECTORY=") || strings.HasPrefix(entry, "GIT_CONFIG_KEY_") {
			t.Fatalf("repository override survived isolation: %q", entry)
		}
	}
}

func TestBindingDestinationCheckedBeforeSubmitAndFailureReportsCommittedID(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, _, base, head := h.repository(t)
	bindingPath := filepath.Join(h.root, "bindings", "first.json")
	conflicting := revmuximport.Binding{Version: 1, Repo: filepath.Join(repo, ".git"), RevmuxTask: "fictional-task",
		Run: "other-run", Base: base, Head: head, RangeSource: "operator"}
	if err := revmuximport.SaveBinding(bindingPath, conflicting); err != nil {
		t.Fatal(err)
	}
	h.call(t, 1, h.submitArgs(repo, base, head, bindingPath, "01-review", "", uuid.NewString())...)
	var history store.TaskResultHistoryPage
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if len(history.Entries) != 0 || history.CurrentResultID != "" {
		t.Fatalf("conflicting binding destination did not block submission: %+v", history)
	}

	requestID := uuid.NewString()
	_, diagnostic := h.call(t, 1, h.submitArgs(repo, base, head, "/proc/artel-review-binding.json", "01-review", "", requestID)...)
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if len(history.Entries) != 1 || history.CurrentResultID == "" || !strings.Contains(diagnostic, "result committed with ID "+history.CurrentResultID) {
		t.Fatalf("post-commit binding failure did not identify result: history=%+v diagnostic=%q", history, diagnostic)
	}
	retryBinding := filepath.Join(h.root, "bindings", "recovered.json")
	output, _ := h.call(t, 0, h.submitArgs(repo, base, head, retryBinding, "01-review", "", requestID)...)
	var result store.TaskResult
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.ID != history.CurrentResultID {
		t.Fatalf("same-request recovery changed committed result: %s %v", output, err)
	}
	assertCLIBinding(t, retryBinding, result, base, head)
}

func TestReviewsAttachEmptyRequirementsIsSavedIncomplete(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, _, base, head := h.repository(t)
	binding := filepath.Join(h.root, "bindings", "first.json")
	output, _ := h.call(t, 0, h.submitArgs(repo, base, head, binding, "01-review", "", uuid.NewString())...)
	var result store.TaskResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	report := writeCLIReport(t, h.root, "fictional-task", "01-review")
	args := []string{"reviews", "attach", "--task-id", h.task.ID, "--result-id", result.ID,
		"--binding", binding, "--report-dir", report, "--request-id", uuid.NewString()}
	reviewOutput, _ := h.call(t, 0, args...)
	var review store.TaskReview
	if err := json.Unmarshal([]byte(reviewOutput), &review); err != nil || review.Complete || len(review.IncompleteReasons) != 1 {
		t.Fatalf("empty requirements were not saved as incomplete: %s %v", reviewOutput, err)
	}
	if !strings.Contains(reviewOutput, `"required_sources":[]`) {
		t.Fatalf("empty requirements were not normalized as an array: %s", reviewOutput)
	}
}

func TestReviewsAttachUsesSelectedHistoricalSubmission(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, _, base, head := h.repository(t)
	firstBinding := filepath.Join(h.root, "bindings", "first.json")
	firstOutput, _ := h.call(t, 0, h.submitArgs(repo, base, head, firstBinding, "01-review", "", uuid.NewString())...)
	var first store.TaskResult
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil {
		t.Fatal(err)
	}
	secondBinding := filepath.Join(h.root, "bindings", "second.json")
	h.call(t, 0, h.submitArgs(repo, base, head, secondBinding, "02-review", first.ID, uuid.NewString())...)
	report := writeCLIReport(t, h.root, "fictional-task", "01-review")
	output, _ := h.call(t, 0, h.attachArgs(first.ID, firstBinding, report, uuid.NewString())...)
	var review store.TaskReview
	if err := json.Unmarshal([]byte(output), &review); err != nil || review.ResultID != first.ID {
		t.Fatalf("late review attached to wrong submission: %s %v", output, err)
	}
	var history store.TaskResultHistoryPage
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if history.CurrentResultID == first.ID || len(history.Entries) != 3 || history.Entries[2].Review.ResultID != first.ID {
		t.Fatalf("late review did not retain selected historical result: %+v", history)
	}
}

func TestReviewsAttachRejectsBindingMismatchWithoutMutation(t *testing.T) {
	h := newCLIResultHarness(t)
	repo, _, base, head := h.repository(t)
	bindingPath := filepath.Join(h.root, "binding", "original.json")
	output, _ := h.call(t, 0, h.submitArgs(repo, base, head, bindingPath, "01-review", "", uuid.NewString())...)
	var result store.TaskResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatal(err)
	}
	original, err := revmuximport.LoadBinding(bindingPath)
	if err != nil {
		t.Fatal(err)
	}
	report := writeCLIReport(t, h.root, original.RevmuxTask, original.Run)
	tests := []struct {
		name   string
		mutate func(*revmuximport.Binding)
	}{
		{name: "other repo", mutate: func(binding *revmuximport.Binding) { binding.Repo = filepath.Join(h.root, "other", ".git") }},
		{name: "other task", mutate: func(binding *revmuximport.Binding) { binding.RevmuxTask = "other-task" }},
		{name: "other run", mutate: func(binding *revmuximport.Binding) { binding.Run = "other-run" }},
		{name: "other base", mutate: func(binding *revmuximport.Binding) { binding.Base = strings.Repeat("b", 40) }},
		{name: "other head", mutate: func(binding *revmuximport.Binding) { binding.Head = original.Base }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := original
			test.mutate(&changed)
			path := filepath.Join(h.root, "binding", fmt.Sprintf("changed-%d.json", index))
			if err := revmuximport.SaveBinding(path, changed); err != nil {
				t.Fatal(err)
			}
			h.call(t, 1, h.attachArgs(result.ID, path, report, uuid.NewString())...)
		})
	}
	var history store.TaskResultHistoryPage
	h.ownerRequest(t, http.MethodGet, "/v1/tasks/"+h.task.ID+"/results?after=0&limit=100", "", nil, http.StatusOK, &history)
	if len(history.Entries) != 1 {
		t.Fatalf("rejected binding attached a review: %+v", history)
	}
}

func TestSelectedResultUsesSingleEntryPagesWithinResponseLimit(t *testing.T) {
	target := store.TaskResult{ID: uuid.NewString(), TaskID: uuid.NewString(), Repo: "/tmp/result/.git",
		Base: strings.Repeat("1", 40), Head: strings.Repeat("a", 40), Summary: "selected", CreatedAt: "2026-11-20T12:00:00Z"}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("limit") != "1" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 4*1024*1024+1))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(store.TaskResultHistoryPage{CurrentResultID: target.ID,
			Entries: []store.TaskResultHistoryEntry{{Seq: 1, Kind: "result", Result: &target}}, NextAfter: 1})
	}))
	defer server.Close()
	client := resultOwnerClient{endpoint: server.URL, token: "test", client: server.Client()}
	selected, err := selectedResult(client, target.TaskID, target.ID)
	if err != nil || selected.ID != target.ID || requests != 1 {
		t.Fatalf("selected=%+v requests=%d err=%v", selected, requests, err)
	}
}

func TestResultCLIArgumentValidation(t *testing.T) {
	for _, args := range [][]string{
		{"results"}, {"results", "unknown"}, {"reviews"}, {"reviews", "unknown"},
		{"results", "submit", "--task-id", "not-a-uuid"},
		{"results", "submit", "--task-id", uuid.NewString()},
		{"reviews", "attach", "--task-id", uuid.NewString(), "--result-id", uuid.NewString(), "--binding", "x", "--report-dir", "y", "--required-source", "missing-executor"},
	} {
		var out, diagnostic bytes.Buffer
		if code := run(args, &out, &diagnostic); code != 2 || out.Len() != 0 || diagnostic.Len() == 0 {
			t.Fatalf("args=%v code/output mismatch", args)
		}
	}
	tooMany := []string{"reviews", "attach", "--task-id", uuid.NewString(), "--result-id", uuid.NewString(), "--binding", "x", "--report-dir", "y"}
	for index := 0; index <= store.MaxRequiredSources; index++ {
		tooMany = append(tooMany, "--required-source", fmt.Sprintf("source-%d=codex", index))
	}
	var out, diagnostic bytes.Buffer
	if code := run(tooMany, &out, &diagnostic); code != 2 || out.Len() != 0 || diagnostic.Len() == 0 {
		t.Fatal("33 required sources passed CLI argument validation")
	}
}
