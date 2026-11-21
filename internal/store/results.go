package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

const (
	TaskResultSubmitted  = "task.result.submit"
	TaskReviewAttached   = "task.review.attach"
	MaxRequiredSources   = 32
	MaxReviewCounter     = 9007199254740991
	resultHistoryMax     = 100
	currentReviewPageMax = 50
)

// TaskResult is an immutable owner assertion about one exact Git candidate.
// Repo is the canonical Git common-dir identity supplied by the CLI; the daemon
// never runs Git or substitutes a project root for it.
type TaskResult struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Repo      string `json:"repo"`
	Base      string `json:"base"`
	Head      string `json:"head"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
}

type ReviewRequirement struct {
	Name     string `json:"name"`
	Executor string `json:"executor"`
}

// ReviewSource is normalized evidence of a response, a degraded report, or a
// known non-response. A responded source may have zero findings; that is
// distinct from a missing one.
type ReviewSource struct {
	Name          string `json:"name"`
	Executor      string `json:"executor"`
	Required      bool   `json:"required"`
	Responded     bool   `json:"responded"`
	Degraded      bool   `json:"degraded"`
	Findings      int    `json:"findings"`
	PreExisting   int    `json:"pre_existing"`
	OpenQuestions int    `json:"open_questions"`
}

// TaskReview contains only normalized review evidence, never raw findings.
type TaskReview struct {
	ID                string              `json:"id"`
	TaskID            string              `json:"task_id"`
	ResultID          string              `json:"result_id"`
	Repo              string              `json:"repo"`
	Base              string              `json:"base"`
	Head              string              `json:"head"`
	RevmuxTask        string              `json:"revmux_task"`
	Run               string              `json:"run"`
	RangeSource       string              `json:"range_source"`
	ReportRef         string              `json:"report_ref"`
	Complete          bool                `json:"complete"`
	RequiredSources   []ReviewRequirement `json:"required_sources"`
	Sources           []ReviewSource      `json:"sources"`
	IncompleteReasons []string            `json:"incomplete_reasons"`
	Findings          int                 `json:"findings"`
	PreExisting       int                 `json:"pre_existing"`
	OpenQuestions     int                 `json:"open_questions"`
	CreatedAt         string              `json:"created_at"`
	// EvidenceConsistent is derived on reads using current evidence rules. It
	// never rewrites the immutable receipt.
	EvidenceConsistent bool `json:"evidence_consistent"`
}

type SubmitResultInput struct {
	Repo             string `json:"repo"`
	Base             string `json:"base"`
	Head             string `json:"head"`
	Summary          string `json:"summary,omitempty"`
	ExpectedResultID string `json:"expected_result_id"`
}

type AttachReviewInput struct {
	Repo              string              `json:"repo"`
	Base              string              `json:"base"`
	Head              string              `json:"head"`
	RevmuxTask        string              `json:"revmux_task"`
	Run               string              `json:"run"`
	RangeSource       string              `json:"range_source"`
	ReportRef         string              `json:"report_ref"`
	Complete          bool                `json:"complete"`
	RequiredSources   []ReviewRequirement `json:"required_sources"`
	Sources           []ReviewSource      `json:"sources"`
	IncompleteReasons []string            `json:"incomplete_reasons"`
	Findings          int                 `json:"findings"`
	PreExisting       int                 `json:"pre_existing"`
	OpenQuestions     int                 `json:"open_questions"`
}

type TaskResultHistoryEntry struct {
	Seq    int64       `json:"seq"`
	Kind   string      `json:"kind"`
	Result *TaskResult `json:"result,omitempty"`
	Review *TaskReview `json:"review,omitempty"`
}

// TaskResultHistoryPage is bounded over both submissions and late attachments.
// CurrentResultID is empty only when the task has no submitted result.
type TaskResultHistoryPage struct {
	CurrentResultID string                   `json:"current_result_id"`
	Entries         []TaskResultHistoryEntry `json:"entries"`
	NextAfter       int64                    `json:"next_after"`
	HasMore         bool                     `json:"has_more"`
}

// CurrentTaskResult keeps the candidate readable independently of its bounded
// review pages. Reviews remains an empty compatibility field; callers use
// CurrentTaskReviews to obtain the complete evidence before making a decision.
type CurrentTaskResult struct {
	Result  *TaskResult  `json:"result"`
	Reviews []TaskReview `json:"reviews"`
}

// CurrentTaskReviewsPage is a bounded insertion-order page for the candidate
// that was current when this read transaction started. ResultID lets the UI
// reject pages from different candidates.
type CurrentTaskReviewsPage struct {
	ResultID  string       `json:"result_id"`
	Reviews   []TaskReview `json:"reviews"`
	NextAfter string       `json:"next_after"`
	HasMore   bool         `json:"has_more"`
}

func validResultText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= max && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}

func validResultRepo(repo string) bool {
	return validResultText(repo, 4096, true) && filepath.IsAbs(repo) && filepath.Clean(repo) == repo
}

func validResultSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validateSubmitResult(taskID string, key string, in SubmitResultInput) error {
	if !observerUUID(taskID) || !observerUUID(key) || !validResultRepo(in.Repo) ||
		!validResultSHA(in.Base) || !validResultSHA(in.Head) || !validResultText(in.Summary, 4096, false) ||
		(in.ExpectedResultID != "" && !observerUUID(in.ExpectedResultID)) {
		return domain.Invalid("invalid task result fields")
	}
	return nil
}

func validateReviewRequirements(sources []ReviewRequirement) error {
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		key := source.Name + "\x00" + source.Executor
		if !validResultText(source.Name, 200, true) || !validResultText(source.Executor, 120, true) || seen[key] {
			return domain.Invalid("invalid required review source")
		}
		seen[key] = true
	}
	return nil
}

func validateReviewSources(sources []ReviewSource) error {
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		key := source.Name + "\x00" + source.Executor
		if !validResultText(source.Name, 200, true) || !validResultText(source.Executor, 120, true) || seen[key] ||
			source.Findings < 0 || source.PreExisting < 0 || source.OpenQuestions < 0 {
			return domain.Invalid("invalid review source")
		}
		seen[key] = true
	}
	return nil
}

func validateReviewMetadata(in AttachReviewInput) error {
	if !validResultRepo(in.Repo) || !validResultSHA(in.Base) || !validResultSHA(in.Head) {
		return domain.Invalid("invalid review result identity")
	}
	if !validResultText(in.RevmuxTask, 200, true) || !validResultText(in.Run, 200, true) ||
		in.RangeSource != "operator" || !validResultText(in.ReportRef, 4096, true) {
		return domain.Invalid("invalid review fields")
	}
	return nil
}

func validateReviewSummary(in AttachReviewInput) error {
	if (in.Complete && len(in.IncompleteReasons) != 0) || (!in.Complete && len(in.IncompleteReasons) == 0) ||
		in.Findings < 0 || in.PreExisting < 0 || in.OpenQuestions < 0 {
		return domain.Invalid("invalid review summary")
	}
	return nil
}

func validateReviewSourceTotals(in AttachReviewInput) error {
	if in.Findings > MaxReviewCounter || in.PreExisting > MaxReviewCounter || in.OpenQuestions > MaxReviewCounter {
		return domain.Invalid("review totals exceed the safe integer limit")
	}
	totals := []struct {
		total  int
		counts func(ReviewSource) int
	}{
		{in.Findings, func(source ReviewSource) int { return source.Findings }},
		{in.PreExisting, func(source ReviewSource) int { return source.PreExisting }},
		{in.OpenQuestions, func(source ReviewSource) int { return source.OpenQuestions }},
	}
	for _, total := range totals {
		unattributed := total.total
		for _, source := range in.Sources {
			count := total.counts(source)
			if count > MaxReviewCounter || count > total.total {
				return domain.Invalid("review source counts exceed review totals or the safe integer limit")
			}
			unattributed -= min(unattributed, count)
		}
		// Sources may share items. Saturate the remaining count at zero so
		// overlap is allowed without overflowing the sum.
		if unattributed != 0 {
			return domain.Invalid("review total exceeds source attribution")
		}
	}
	return nil
}

func validateReviewCompleteness(in AttachReviewInput) error {
	required := make(map[string]bool, len(in.RequiredSources))
	for _, requirement := range in.RequiredSources {
		required[requirement.Name+"\x00"+requirement.Executor] = true
	}
	actual := make(map[string]ReviewSource, len(in.Sources))
	for _, source := range in.Sources {
		key := source.Name + "\x00" + source.Executor
		// A source is either responding evidence or explicitly degraded/missing
		// roster evidence. Required is derived from the exact pair, not asserted
		// independently by the client.
		if source.Responded == source.Degraded || source.Required != required[key] {
			return domain.Invalid("invalid review source state")
		}
		actual[key] = source
	}
	complete := len(in.RequiredSources) != 0
	for _, requirement := range in.RequiredSources {
		source, ok := actual[requirement.Name+"\x00"+requirement.Executor]
		if !ok || !source.Responded || source.Degraded {
			complete = false
		}
	}
	if in.Complete != complete {
		return domain.Invalid("review completeness contradicts required sources")
	}
	return nil
}

func validateAttachReviewIdentity(taskID, resultID, key string, in AttachReviewInput) error {
	if !observerUUID(taskID) || !observerUUID(resultID) || !observerUUID(key) {
		return domain.Invalid("invalid review identity")
	}
	if err := validateReviewMetadata(in); err != nil {
		return err
	}
	if in.RequiredSources == nil || len(in.RequiredSources) > MaxRequiredSources || in.Sources == nil || len(in.Sources) > 128 ||
		in.IncompleteReasons == nil || len(in.IncompleteReasons) > 32 {
		return domain.Invalid("invalid review source limits")
	}
	if err := validateReviewSummary(in); err != nil {
		return err
	}
	if err := validateReviewRequirements(in.RequiredSources); err != nil {
		return err
	}
	if err := validateReviewSources(in.Sources); err != nil {
		return err
	}
	for _, reason := range in.IncompleteReasons {
		if !validResultText(reason, 500, true) {
			return domain.Invalid("invalid incomplete review reason")
		}
	}
	return nil
}

func validateAttachReview(taskID, resultID, key string, in AttachReviewInput) error {
	if err := validateAttachReviewIdentity(taskID, resultID, key, in); err != nil {
		return err
	}
	if err := validateReviewSourceTotals(in); err != nil {
		return err
	}
	return validateReviewCompleteness(in)
}

func resultReplay(ctx context.Context, tx *sql.Tx, m Mutation, legacyOnly bool) (Result, bool, error) {
	var method, path, hash string
	var result Result
	err := tx.QueryRowContext(ctx, "SELECT method,path,payload_hash,status,body FROM mutation_receipts WHERE key=?", m.Key).Scan(&method, &path, &hash, &result.Status, &result.Body)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if method != m.Method || path != m.Path {
		return Result{}, false, domain.Conflict("idempotency key reused for a different request")
	}
	if legacyOnly {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(result.Body, &fields); err != nil {
			return Result{}, false, err
		}
		if _, current := fields["evidence_consistent"]; current {
			return Result{}, false, nil
		}
	}
	if hash != m.Hash {
		return Result{}, false, domain.Conflict("idempotency key reused for a different request")
	}
	return result, true, nil
}

func resultReceipt(ctx context.Context, tx *sql.Tx, m Mutation, result Result) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO mutation_receipts(key,method,path,payload_hash,status,body) VALUES(?,?,?,?,?,?)",
		m.Key, m.Method, m.Path, m.Hash, result.Status, result.Body)
	return err
}

func resultChangeFields(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	data, _ := json.Marshal(value)
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	return fields
}

func insertResultEvent(ctx context.Context, tx *sql.Tx, action, projectID, taskID, timestamp string, changes []Change) error {
	encoded, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(action,project_id,task_id,source,timestamp,changes) VALUES(?,?,?,'owner_manual',?,?)",
		action, projectID, taskID, timestamp, string(encoded))
	return err
}

func resultResponse(status int, value any) (Result, error) {
	data, err := json.Marshal(value)
	return Result{Status: status, Body: data}, err
}

// SubmitResult atomically CASes the current pointer, appends immutable history,
// emits SSE/audit and stores the request receipt. Replay precedes the CAS so an
// uncertain original response remains recoverable after later submissions.
func (s *Store) SubmitResult(ctx context.Context, taskID string, m Mutation, in SubmitResultInput) (Result, error) {
	if err := validateSubmitResult(taskID, m.Key, in); err != nil {
		return Result{}, err
	}
	m.Hash = observerHash(in)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, matched, err := resultReplay(ctx, tx, m, false); err != nil || matched {
		if err != nil {
			return Result{}, err
		}
		return replay, tx.Commit()
	}
	var projectID string
	var archived bool
	if err := tx.QueryRowContext(ctx, `SELECT t.project_id,p.archived FROM tasks t
		JOIN projects p ON p.id=t.project_id WHERE t.id=?`, taskID).Scan(&projectID, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, &domain.Error{Code: "not_found", Message: "task not found"}
		}
		return Result{}, err
	}
	if archived {
		return Result{}, domain.Conflict("project is archived")
	}
	var current string
	err = tx.QueryRowContext(ctx, "SELECT result_id FROM current_task_results WHERE task_id=?", taskID).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	if current != in.ExpectedResultID {
		return Result{}, domain.Conflict("current task result changed")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result := TaskResult{ID: uuid.NewString(), TaskID: taskID, Repo: in.Repo, Base: in.Base, Head: in.Head, Summary: in.Summary, CreatedAt: now}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO task_results(id,task_id,repo,base_sha,head_sha,record) VALUES(?,?,?,?,?,?)",
		result.ID, taskID, result.Repo, result.Base, result.Head, string(encoded)); err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO current_task_results(task_id,result_id) VALUES(?,?)
		ON CONFLICT(task_id) DO UPDATE SET result_id=excluded.result_id`, taskID, result.ID); err != nil {
		return Result{}, err
	}
	before := map[string]any{"result_id": nil}
	if current != "" {
		before["result_id"] = current
	}
	changes := []Change{
		{Object: "task_result", ID: result.ID, Before: map[string]any{}, After: resultChangeFields(result)},
		{Object: "task_result_current", ID: taskID, Before: before, After: map[string]any{"result_id": result.ID}},
	}
	if err := insertResultEvent(ctx, tx, TaskResultSubmitted, projectID, taskID, now, changes); err != nil {
		return Result{}, err
	}
	response, err := resultResponse(201, result)
	if err != nil {
		return Result{}, err
	}
	if err := resultReceipt(ctx, tx, m, response); err != nil {
		return Result{}, err
	}
	return response, tx.Commit()
}

func loadTaskResult(ctx context.Context, tx *sql.Tx, id string) (TaskResult, error) {
	var result TaskResult
	var data string
	err := tx.QueryRowContext(ctx, "SELECT record FROM task_results WHERE id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return result, &domain.Error{Code: "not_found", Message: "task result not found"}
	}
	if err != nil {
		return result, err
	}
	return result, json.Unmarshal([]byte(data), &result)
}

// ReplayLegacyReview only looks up an exact pre-normalization receipt. No
// receipt, or a current receipt, lets the caller attempt the normal attach.
func (s *Store) ReplayLegacyReview(ctx context.Context, taskID, resultID string, m Mutation, in AttachReviewInput) (Result, error) {
	if err := validateAttachReviewIdentity(taskID, resultID, m.Key, in); err != nil {
		return Result{}, err
	}
	m.Hash = observerHash(in)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	receipt, matched, err := resultReplay(ctx, tx, m, true)
	if err != nil {
		return Result{}, err
	}
	if !matched {
		return Result{}, &domain.Error{Code: "not_found", Message: "legacy review receipt not found"}
	}
	return receipt, tx.Commit()
}

// AttachReview uses the stored result as authority for task/repo/base/head.
// The request values are checked, never copied into the persisted identity.
func (s *Store) AttachReview(ctx context.Context, taskID, resultID string, m Mutation, in AttachReviewInput) (Result, error) {
	// Structural identity and the exact payload hash precede receipt lookup.
	// Newer semantic checks must not make a legacy exact receipt unreplayable.
	if err := validateAttachReviewIdentity(taskID, resultID, m.Key, in); err != nil {
		return Result{}, err
	}
	m.Hash = observerHash(in)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, matched, err := resultReplay(ctx, tx, m, false); err != nil || matched {
		if err != nil {
			return Result{}, err
		}
		return replay, tx.Commit()
	}
	if err := validateReviewSourceTotals(in); err != nil {
		return Result{}, err
	}
	if err := validateReviewCompleteness(in); err != nil {
		return Result{}, err
	}
	result, err := loadTaskResult(ctx, tx, resultID)
	if err != nil {
		return Result{}, err
	}
	if result.TaskID != taskID {
		return Result{}, &domain.Error{Code: "not_found", Message: "task result not found"}
	}
	if in.Repo != result.Repo || in.Base != result.Base || in.Head != result.Head {
		return Result{}, domain.Conflict("task result identity mismatch")
	}
	var projectID string
	var archived bool
	if err := tx.QueryRowContext(ctx, `SELECT t.project_id,p.archived FROM tasks t
		JOIN projects p ON p.id=t.project_id WHERE t.id=?`, taskID).Scan(&projectID, &archived); err != nil {
		return Result{}, err
	}
	if archived {
		return Result{}, domain.Conflict("project is archived")
	}
	response, matched, err := attachExistingReview(ctx, tx, taskID, result, m, in)
	if err != nil {
		return Result{}, err
	}
	if matched {
		return response, tx.Commit()
	}
	response, err = insertTaskReview(ctx, tx, taskID, projectID, result, in)
	if err != nil {
		return Result{}, err
	}
	if err := resultReceipt(ctx, tx, m, response); err != nil {
		return Result{}, err
	}
	return response, tx.Commit()
}

func attachExistingReview(ctx context.Context, tx *sql.Tx, taskID string, result TaskResult, m Mutation, in AttachReviewInput) (Result, bool, error) {
	payloadHash := observerHash(in)
	var existingID, existingHash string
	err := tx.QueryRowContext(ctx, "SELECT id,payload_hash FROM task_reviews WHERE repo=? AND revmux_task=? AND run=?",
		result.Repo, in.RevmuxTask, in.Run).Scan(&existingID, &existingHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	existing, err := loadTaskReview(ctx, tx, existingID)
	if err != nil {
		return Result{}, false, err
	}
	if existing.ResultID != result.ID || existing.TaskID != taskID || existingHash != payloadHash {
		return Result{}, false, domain.Conflict("review run is already attached")
	}
	response, err := resultResponse(200, existing)
	if err != nil {
		return Result{}, false, err
	}
	if err := resultReceipt(ctx, tx, m, response); err != nil {
		return Result{}, false, err
	}
	return response, true, nil
}

func insertTaskReview(ctx context.Context, tx *sql.Tx, taskID, projectID string, result TaskResult, in AttachReviewInput) (Result, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	review := TaskReview{
		ID: uuid.NewString(), TaskID: taskID, ResultID: result.ID,
		Repo: result.Repo, Base: result.Base, Head: result.Head,
		RevmuxTask: in.RevmuxTask, Run: in.Run, RangeSource: "operator", ReportRef: in.ReportRef,
		Complete: in.Complete, RequiredSources: in.RequiredSources, Sources: in.Sources,
		IncompleteReasons: in.IncompleteReasons, Findings: in.Findings,
		PreExisting: in.PreExisting, OpenQuestions: in.OpenQuestions, CreatedAt: now,
		EvidenceConsistent: true,
	}
	encoded, err := json.Marshal(review)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO task_reviews(id,task_id,result_id,repo,revmux_task,run,payload_hash,record)
		VALUES(?,?,?,?,?,?,?,?)`, review.ID, taskID, result.ID, result.Repo, in.RevmuxTask, in.Run, observerHash(in), string(encoded)); err != nil {
		return Result{}, err
	}
	changes := []Change{{Object: "task_review", ID: review.ID, Before: map[string]any{}, After: resultChangeFields(review)}}
	if err := insertResultEvent(ctx, tx, TaskReviewAttached, projectID, taskID, now, changes); err != nil {
		return Result{}, err
	}
	return resultResponse(201, review)
}

func reviewEvidenceConsistent(review TaskReview) bool {
	required := make(map[string]bool, len(review.RequiredSources))
	for _, requirement := range review.RequiredSources {
		key := requirement.Name + "\x00" + requirement.Executor
		if required[key] {
			return false
		}
		required[key] = true
	}
	actual := make(map[string]ReviewSource, len(review.Sources))
	for _, source := range review.Sources {
		key := source.Name + "\x00" + source.Executor
		if _, exists := actual[key]; exists || source.Responded == source.Degraded || source.Required != required[key] {
			return false
		}
		actual[key] = source
	}
	complete := len(required) != 0
	for _, requirement := range review.RequiredSources {
		source, ok := actual[requirement.Name+"\x00"+requirement.Executor]
		if !ok || !source.Responded || source.Degraded {
			complete = false
		}
	}
	return review.Complete == complete && validateReviewSourceTotals(AttachReviewInput{
		Sources: review.Sources, Findings: review.Findings, PreExisting: review.PreExisting, OpenQuestions: review.OpenQuestions,
	}) == nil
}

func projectReviewCounter(value *int) bool {
	if *value < 0 {
		*value = 0
		return false
	}
	if *value > MaxReviewCounter {
		*value = MaxReviewCounter
		return false
	}
	return true
}

func loadTaskReview(ctx context.Context, tx *sql.Tx, id string) (TaskReview, error) {
	var review TaskReview
	var data string
	err := tx.QueryRowContext(ctx, "SELECT record FROM task_reviews WHERE id=?", id).Scan(&data)
	if err != nil {
		return review, err
	}
	if err := json.Unmarshal([]byte(data), &review); err != nil {
		return review, err
	}
	countersConsistent := true
	counters := []*int{&review.Findings, &review.PreExisting, &review.OpenQuestions}
	for index := range review.Sources {
		source := &review.Sources[index]
		counters = append(counters, &source.Findings, &source.PreExisting, &source.OpenQuestions)
	}
	for _, counter := range counters {
		if !projectReviewCounter(counter) {
			countersConsistent = false
		}
	}
	review.EvidenceConsistent = countersConsistent && reviewEvidenceConsistent(review)
	return review, nil
}

// CurrentTaskResult reads the current candidate without walking review or
// submission history. Its bounded size does not grow with immutable reviews.
func (s *Store) CurrentTaskResult(ctx context.Context, taskID string) (CurrentTaskResult, error) {
	current := CurrentTaskResult{Reviews: []TaskReview{}}
	if !observerUUID(taskID) {
		return current, domain.Invalid("invalid task UUID")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return current, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return current, &domain.Error{Code: "not_found", Message: "task not found"}
		}
		return current, err
	}
	var resultID string
	err = tx.QueryRowContext(ctx, "SELECT result_id FROM current_task_results WHERE task_id=?", taskID).Scan(&resultID)
	if errors.Is(err, sql.ErrNoRows) {
		return current, tx.Commit()
	}
	if err != nil {
		return current, err
	}
	result, err := loadTaskResult(ctx, tx, resultID)
	if err != nil {
		return current, err
	}
	if result.TaskID != taskID {
		return current, errors.New("current task result identity mismatch")
	}
	current.Result = &result
	return current, tx.Commit()
}

func currentReviewCursorRow(ctx context.Context, tx *sql.Tx, taskID, resultID, after string) (int64, error) {
	if after == "" {
		return 0, nil
	}
	var row int64
	if err := tx.QueryRowContext(ctx, "SELECT rowid FROM task_reviews WHERE id=? AND task_id=? AND result_id=?", after, taskID, resultID).Scan(&row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, domain.Invalid("review cursor is not in the current candidate")
		}
		return 0, err
	}
	return row, nil
}

func currentReviewIDs(ctx context.Context, tx *sql.Tx, taskID, resultID string, afterRow int64, limit int) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM task_reviews
		WHERE task_id=? AND result_id=? AND rowid>? ORDER BY rowid LIMIT ?`, taskID, resultID, afterRow, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// CurrentTaskReviews returns a bounded page for exactly the candidate captured
// in this read transaction. A UUID cursor cannot cross into another candidate.
func (s *Store) CurrentTaskReviews(ctx context.Context, taskID, after string, limit int) (CurrentTaskReviewsPage, error) {
	page := CurrentTaskReviewsPage{Reviews: []TaskReview{}, NextAfter: after}
	if !observerUUID(taskID) || after != "" && !observerUUID(after) || limit < 1 || limit > currentReviewPageMax {
		return page, domain.Invalid("invalid current review query")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return page, &domain.Error{Code: "not_found", Message: "task not found"}
		}
		return page, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT result_id FROM current_task_results WHERE task_id=?", taskID).Scan(&page.ResultID); errors.Is(err, sql.ErrNoRows) {
		if after != "" {
			return CurrentTaskReviewsPage{Reviews: []TaskReview{}}, domain.Invalid("review cursor has no current candidate")
		}
		page.NextAfter = ""
		return page, tx.Commit()
	} else if err != nil {
		return page, err
	}
	afterRow, err := currentReviewCursorRow(ctx, tx, taskID, page.ResultID, after)
	if err != nil {
		return page, err
	}
	ids, err := currentReviewIDs(ctx, tx, taskID, page.ResultID, afterRow, limit)
	if err != nil {
		return page, err
	}
	page.HasMore = len(ids) > limit
	if page.HasMore {
		ids = ids[:limit]
	}
	for _, id := range ids {
		review, err := loadTaskReview(ctx, tx, id)
		if err != nil {
			return page, err
		}
		page.Reviews = append(page.Reviews, review)
		page.NextAfter = id
	}
	return page, tx.Commit()
}

type taskResultHistoryID struct {
	seq  int64
	kind string
	id   string
}

func resultHistoryIDs(ctx context.Context, tx *sql.Tx, taskID string, after int64, limit int) ([]taskResultHistoryID, error) {
	rows, err := tx.QueryContext(ctx, `SELECT seq,changes FROM events WHERE task_id=? AND seq>?
		AND action IN (?,?) ORDER BY seq LIMIT ?`, taskID, after, TaskResultSubmitted, TaskReviewAttached, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []taskResultHistoryID{}
	for rows.Next() {
		var seq int64
		var data string
		if err := rows.Scan(&seq, &data); err != nil {
			return nil, err
		}
		var changes []Change
		if err := json.Unmarshal([]byte(data), &changes); err != nil {
			return nil, err
		}
		for _, change := range changes {
			if change.Object == "task_result" || change.Object == "task_review" {
				kind := "result"
				if change.Object == "task_review" {
					kind = "review"
				}
				ids = append(ids, taskResultHistoryID{seq: seq, kind: kind, id: change.ID})
				break
			}
		}
	}
	return ids, rows.Err()
}

// TaskResultHistory returns one bounded task-scoped stream of submissions and
// attachments in event order, so late reviews remain associated with old heads.
func (s *Store) TaskResultHistory(ctx context.Context, taskID string, after int64, limit int) (TaskResultHistoryPage, error) {
	page := TaskResultHistoryPage{Entries: []TaskResultHistoryEntry{}, NextAfter: after}
	if !observerUUID(taskID) || after < 0 || limit < 1 || limit > resultHistoryMax {
		return page, domain.Invalid("invalid task result history query")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, "SELECT 1 FROM tasks WHERE id=?", taskID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return page, &domain.Error{Code: "not_found", Message: "task not found"}
		}
		return page, err
	}
	err = tx.QueryRowContext(ctx, "SELECT result_id FROM current_task_results WHERE task_id=?", taskID).Scan(&page.CurrentResultID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return page, err
	}
	ids, err := resultHistoryIDs(ctx, tx, taskID, after, limit)
	if err != nil {
		return page, err
	}
	page.HasMore = len(ids) > limit
	if page.HasMore {
		ids = ids[:limit]
	}
	for _, item := range ids {
		entry := TaskResultHistoryEntry{Seq: item.seq, Kind: item.kind}
		if item.kind == "result" {
			result, err := loadTaskResult(ctx, tx, item.id)
			if err != nil {
				return page, err
			}
			entry.Result = &result
		} else {
			review, err := loadTaskReview(ctx, tx, item.id)
			if err != nil {
				return page, err
			}
			entry.Review = &review
		}
		page.Entries = append(page.Entries, entry)
		page.NextAfter = item.seq
	}
	return page, tx.Commit()
}
