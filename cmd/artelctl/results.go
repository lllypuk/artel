package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/revmuximport"
	"github.com/lllypuk/artel/internal/store"
)

type resultOwnerClient struct {
	endpoint, token string
	client          *http.Client
}

// request accepts the result API's 201 creation response as well as its 200
// replay/history response. Other owner commands retain their existing client.
func (c resultOwnerClient) request(method, path, key string, body, result any, raw ...*[]byte) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	// #nosec G704 -- Numeric loopback endpoint validated before constructing client; no proxy or redirects.
	response, err := c.client.Do(req)
	if err != nil {
		if key != "" {
			return fmt.Errorf("local API unavailable; outcome unknown; retry with --request-id %s", key)
		}
		return errors.New("local API unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	validStatus := response.StatusCode == http.StatusOK || method == http.MethodPost && response.StatusCode == http.StatusCreated
	invalidResponse := func() error {
		if key != "" && validStatus {
			return fmt.Errorf("invalid API response; outcome unknown; retry with --request-id %s", key)
		}
		return errors.New("invalid API response")
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return invalidResponse()
	}
	if !validStatus {
		return errors.New("API operation rejected")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil || decoder.Decode(new(any)) != io.EOF {
		return invalidResponse()
	}
	if len(raw) == 1 {
		*raw[0] = bytes.Clone(data)
	}
	return nil
}

func resultFail(errOut io.Writer, command string, code int, message string) int {
	// #nosec G705 -- Fixed local diagnostic; never carries credentials or raw findings.
	_, _ = fmt.Fprintln(errOut, "artelctl "+command+":", message)
	return code
}

func validResultArgumentText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= max && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}

func validFullSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func resultKey(value string) (string, error) {
	if value == "" {
		value = uuid.NewString()
	}
	if !validCanonicalID(value) {
		return "", errors.New("invalid request-id")
	}
	return value, nil
}

func newResultClient(endpoint, tokenPath string) (resultOwnerClient, error) {
	paths, err := local.Resolve("", tokenPath)
	if err != nil {
		return resultOwnerClient{}, errors.New("local paths unavailable")
	}
	token, err := local.ReadToken(paths.TokenFile)
	if err != nil {
		return resultOwnerClient{}, errors.New("owner credential unavailable or unsafe")
	}
	transport := &http.Transport{Proxy: nil}
	client := resultOwnerClient{endpoint: endpoint, token: token, client: &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	return client, nil
}

func closeResultClient(client resultOwnerClient) {
	if transport, ok := client.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func isolatedGitEnvironment() []string {
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
		"GIT_INDEX_FILE": true, "GIT_NAMESPACE": true, "GIT_CEILING_DIRECTORIES": true,
		"GIT_DISCOVERY_ACROSS_FILESYSTEM": true, "GIT_CONFIG_PARAMETERS": true, "GIT_CONFIG_COUNT": true,
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if blocked[name] || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_") {
			continue
		}
		environment = append(environment, entry)
	}
	// Disable both prompts and promisor-remote lazy fetching: candidate
	// validation is strictly local and replacement refs must not change it.
	return append(environment, "GIT_TERMINAL_PROMPT=0", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1")
}

func gitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repo}, args...)
	// #nosec G204 G702 -- Fixed local Git executable and argument vector; no shell or report-provided command.
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Env = isolatedGitEnvironment()
	output, err := command.Output()
	if err != nil {
		return "", errors.New("local Git validation failed")
	}
	// Git diagnostics belong to stderr and must not alter parsed stdout. Strip
	// exactly one line terminator so valid paths ending in spaces survive.
	return strings.TrimSuffix(string(output), "\n"), nil
}

func hasParentPathComponent(path string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(path), "/"), "..")
}

// validateGitCandidate resolves common-dir identity for main and linked
// worktrees and proves both literal lowercase SHAs name commits in that repo.
func validateGitCandidate(repo, base, head string) (string, error) {
	if !validFullSHA(base) || !validFullSHA(head) {
		return "", errors.New("base and head must be full lowercase commit SHAs")
	}
	if hasParentPathComponent(repo) {
		return "", errors.New("repository path must not contain parent components")
	}
	resolved, err := filepath.EvalSymlinks(repo)
	if err != nil {
		return "", errors.New("invalid repository path")
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", errors.New("invalid repository path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	objectFormat, err := gitOutput(ctx, absolute, "rev-parse", "--show-object-format=storage")
	if err != nil || objectFormat != "sha1" {
		return "", errors.New("repository must use SHA-1 object storage for 40-character commit IDs")
	}
	commonDir, err := gitOutput(ctx, absolute, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !filepath.IsAbs(commonDir) || filepath.Clean(commonDir) != commonDir {
		return "", errors.New("repository common-dir unavailable")
	}
	commonDir, err = filepath.EvalSymlinks(commonDir)
	if err != nil || !filepath.IsAbs(commonDir) || filepath.Clean(commonDir) != commonDir {
		return "", errors.New("canonical repository common-dir unavailable")
	}
	for _, sha := range []string{base, head} {
		objectType, err := gitOutput(ctx, absolute, "cat-file", "-t", sha)
		if err != nil || objectType != "commit" {
			return "", errors.New("base and head must be literal commits in this repository")
		}
	}
	return commonDir, nil
}

type submitOptions struct {
	endpoint, tokenPath, taskID, repo, base, head, summary, expected string
	bindingPath, revmuxTask, run, key                                string
	hasBinding                                                       bool
}

func validSubmitOptions(options submitOptions) bool {
	return local.Endpoint(options.endpoint) == nil && validCanonicalID(options.taskID) && options.repo != "" &&
		(options.expected == "" || validCanonicalID(options.expected)) && validResultArgumentText(options.summary, 4096, false)
}

func parseSubmitOptions(args []string) (submitOptions, error) {
	var options submitOptions
	if len(args) == 0 || args[0] != "submit" {
		return options, errors.New("expected submit")
	}
	flags := flag.NewFlagSet("results submit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.endpoint, "endpoint", "http://127.0.0.1:7331", "owner API")
	flags.StringVar(&options.tokenPath, "token-file", "", "owner credential")
	_ = flags.Bool("json", false, "JSON output (always enabled)")
	flags.StringVar(&options.taskID, "task-id", "", "task UUID")
	flags.StringVar(&options.repo, "repo", "", "local repository or worktree")
	flags.StringVar(&options.base, "base", "", "literal base commit")
	flags.StringVar(&options.head, "head", "", "literal head commit")
	flags.StringVar(&options.summary, "summary", "", "owner summary")
	flags.StringVar(&options.expected, "expected-result-id", "", "current result UUID or empty")
	flags.StringVar(&options.bindingPath, "binding", "", "pre-run binding record")
	flags.StringVar(&options.revmuxTask, "revmux-task", "", "revmux task for pre-run binding")
	flags.StringVar(&options.run, "run", "", "revmux run for pre-run binding")
	requestID := flags.String("request-id", "", "UUID retry key")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || !validSubmitOptions(options) {
		return options, errors.New("invalid arguments")
	}
	noBinding := options.bindingPath == "" && options.revmuxTask == "" && options.run == ""
	options.hasBinding = options.bindingPath != "" && options.revmuxTask != "" && options.run != ""
	if !noBinding && (!options.hasBinding || !filepath.IsAbs(options.bindingPath) || filepath.Clean(options.bindingPath) != options.bindingPath ||
		!validResultArgumentText(options.revmuxTask, 200, true) || !validResultArgumentText(options.run, 200, true)) {
		return options, errors.New("binding requires absolute binding, revmux-task and run")
	}
	key, err := resultKey(*requestID)
	options.key = key
	return options, err
}

func submitResult(client resultOwnerClient, options submitOptions, canonicalRepo string) (store.TaskResult, error) {
	input := store.SubmitResultInput{Repo: canonicalRepo, Base: options.base, Head: options.head,
		Summary: options.summary, ExpectedResultID: options.expected}
	var result store.TaskResult
	if err := client.request(http.MethodPost, "/v1/tasks/"+options.taskID+"/results", options.key, input, &result); err != nil {
		return result, err
	}
	if !validCanonicalID(result.ID) || result.TaskID != options.taskID || result.Repo != canonicalRepo ||
		result.Base != options.base || result.Head != options.head || result.Summary != options.summary {
		return result, fmt.Errorf("submission response identity mismatch; outcome unknown; retry with --request-id %s", options.key)
	}
	return result, nil
}

func submissionBinding(options submitOptions, canonicalRepo string) revmuximport.Binding {
	return revmuximport.Binding{Version: 1, Repo: canonicalRepo, RevmuxTask: options.revmuxTask, Run: options.run,
		Base: options.base, Head: options.head, RangeSource: "operator"}
}

func results(args []string, out, errOut io.Writer) int {
	options, err := parseSubmitOptions(args)
	if err != nil {
		return resultFail(errOut, "results", 2, err.Error())
	}
	canonicalRepo, err := validateGitCandidate(options.repo, options.base, options.head)
	if err != nil {
		return resultFail(errOut, "results", 2, err.Error())
	}
	if options.hasBinding {
		if err := revmuximport.ValidateBindingDestination(options.bindingPath, submissionBinding(options, canonicalRepo)); err != nil {
			return resultFail(errOut, "results", 1, err.Error())
		}
	}
	client, err := newResultClient(options.endpoint, options.tokenPath)
	if err != nil {
		return resultFail(errOut, "results", 1, err.Error())
	}
	defer closeResultClient(client)
	result, err := submitResult(client, options, canonicalRepo)
	if err != nil {
		return resultFail(errOut, "results", 1, err.Error())
	}
	if options.hasBinding {
		if err := revmuximport.SaveBinding(options.bindingPath, submissionBinding(options, canonicalRepo)); err != nil {
			return resultFail(errOut, "results", 1, err.Error()+"; result committed with ID "+result.ID+
				"; fix the binding destination and retry with --request-id "+options.key)
		}
	}
	encoded, _ := json.Marshal(result)
	return printOutput(out, string(encoded))
}

func parseRequiredSource(value string) (store.ReviewRequirement, error) {
	name, executor, ok := strings.Cut(value, "=")
	requirement := store.ReviewRequirement{Name: name, Executor: executor}
	if !ok || !validResultArgumentText(name, 200, true) || !validResultArgumentText(executor, 120, true) {
		return store.ReviewRequirement{}, errors.New("required-source must be name=executor")
	}
	return requirement, nil
}

func canonicalReportDir(path string) (string, error) {
	if hasParentPathComponent(path) {
		return "", errors.New("report directory must not contain parent components")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("report directory unavailable")
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", errors.New("invalid report directory")
	}
	resolved = absolute
	// #nosec G703 -- The owner explicitly selects this canonical local report directory; no URL is opened.
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || !filepath.IsAbs(resolved) || filepath.Clean(resolved) != resolved {
		return "", errors.New("report directory is not a canonical local directory")
	}
	return resolved, nil
}

func selectedResult(client resultOwnerClient, taskID, resultID string) (store.TaskResult, error) {
	var selected store.TaskResult
	after := int64(0)
	for {
		var page store.TaskResultHistoryPage
		path := "/v1/tasks/" + taskID + "/results?after=" + strconv.FormatInt(after, 10) + "&limit=1"
		if err := client.request(http.MethodGet, path, "", nil, &page); err != nil {
			return selected, err
		}
		for _, entry := range page.Entries {
			if entry.Kind == "result" && entry.Result != nil && entry.Result.ID == resultID {
				return *entry.Result, nil
			}
		}
		if !page.HasMore {
			return selected, errors.New("selected submission not found in task history")
		}
		if page.NextAfter <= after {
			return selected, errors.New("invalid result history cursor")
		}
		after = page.NextAfter
	}
}

type attachOptions struct {
	endpoint, tokenPath, taskID, resultID, bindingPath, reportPath, key string
	required                                                            []store.ReviewRequirement
}

func parseAttachOptions(args []string) (attachOptions, error) {
	var options attachOptions
	if len(args) == 0 || args[0] != "attach" {
		return options, errors.New("expected attach")
	}
	flags := flag.NewFlagSet("reviews attach", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.endpoint, "endpoint", "http://127.0.0.1:7331", "owner API")
	flags.StringVar(&options.tokenPath, "token-file", "", "owner credential")
	_ = flags.Bool("json", false, "JSON output (always enabled)")
	flags.StringVar(&options.taskID, "task-id", "", "task UUID")
	flags.StringVar(&options.resultID, "result-id", "", "submission UUID")
	flags.StringVar(&options.bindingPath, "binding", "", "pre-run binding record")
	flags.StringVar(&options.reportPath, "report-dir", "", "local revmux report directory")
	requestID := flags.String("request-id", "", "UUID retry key")
	flags.Func("required-source", "required name=executor pair (repeatable)", func(value string) error {
		requirement, err := parseRequiredSource(value)
		if err == nil {
			options.required = append(options.required, requirement)
		}
		return err
	})
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || local.Endpoint(options.endpoint) != nil ||
		!validCanonicalID(options.taskID) || !validCanonicalID(options.resultID) || options.bindingPath == "" || options.reportPath == "" ||
		len(options.required) > store.MaxRequiredSources {
		return options, errors.New("invalid arguments")
	}
	key, err := resultKey(*requestID)
	options.key = key
	return options, err
}

func prepareReview(options attachOptions) (revmuximport.Binding, store.AttachReviewInput, error) {
	var input store.AttachReviewInput
	binding, err := revmuximport.LoadBinding(options.bindingPath)
	if err != nil {
		return binding, input, err
	}
	reportDir, err := canonicalReportDir(options.reportPath)
	if err != nil {
		return binding, input, err
	}
	input, err = revmuximport.Import(binding, reportDir, options.required)
	return binding, input, err
}

func attachReview(client resultOwnerClient, options attachOptions, binding revmuximport.Binding, input store.AttachReviewInput) (store.TaskReview, []byte, error) {
	var review store.TaskReview
	var raw []byte
	result, err := selectedResult(client, options.taskID, options.resultID)
	if err != nil {
		return review, nil, err
	}
	if result.TaskID != options.taskID || input.Repo != result.Repo || input.Base != result.Base || input.Head != result.Head {
		return review, nil, errors.New("binding does not match selected submission")
	}
	path := "/v1/tasks/" + options.taskID + "/results/" + options.resultID + "/reviews"
	if err := client.request(http.MethodPost, path, options.key, input, &review, &raw); err != nil {
		return review, nil, err
	}
	if !validCanonicalID(review.ID) || review.TaskID != options.taskID || review.ResultID != options.resultID ||
		review.Repo != result.Repo || review.Base != result.Base || review.Head != result.Head ||
		review.RevmuxTask != binding.RevmuxTask || review.Run != binding.Run || review.RangeSource != "operator" ||
		review.ReportRef != input.ReportRef {
		return review, nil, fmt.Errorf("review response identity mismatch; outcome unknown; retry with --request-id %s", options.key)
	}
	return review, raw, nil
}

func reviews(args []string, out, errOut io.Writer) int {
	options, err := parseAttachOptions(args)
	if err != nil {
		return resultFail(errOut, "reviews", 2, err.Error())
	}
	binding, input, err := prepareReview(options)
	if err != nil {
		return resultFail(errOut, "reviews", 1, err.Error())
	}
	client, err := newResultClient(options.endpoint, options.tokenPath)
	if err != nil {
		return resultFail(errOut, "reviews", 1, err.Error())
	}
	defer closeResultClient(client)
	_, raw, err := attachReview(client, options, binding, input)
	if err != nil {
		return resultFail(errOut, "reviews", 1, err.Error())
	}
	return printOutput(out, string(raw))
}
