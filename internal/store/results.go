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
	TaskResultSubmitted = "task.result.submit"
	TaskReviewAttached  = "task.review.attach"
	resultHistoryMax    = 100
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

// ReviewSource is normalized evidence about an actually reported source. A
// responded source may have zero findings; that is distinct from a missing one.
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

func validateAttachReview(taskID, resultID, key string, in AttachReviewInput) error {
	if !observerUUID(taskID) || !observerUUID(resultID) || !observerUUID(key) {
		return domain.Invalid("invalid review identity")
	}
	if err := validateReviewMetadata(in); err != nil {
		return err
	}
	if in.RequiredSources == nil || len(in.RequiredSources) > 64 || in.Sources == nil || len(in.Sources) > 128 ||
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

func resultReplay(ctx context.Context, tx *sql.Tx, m Mutation) (Result, bool, error) {
	var method, path, hash string
	var result Result
	err := tx.QueryRowContext(ctx, "SELECT method,path,payload_hash,status,body FROM mutation_receipts WHERE key=?", m.Key).Scan(&method, &path, &hash, &result.Status, &result.Body)
	if err == nil {
		if method != m.Method || path != m.Path || hash != m.Hash {
			return Result{}, true, domain.Conflict("idempotency key reused for a different request")
		}
		return result, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	return Result{}, false, err
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
	if replay, matched, err := resultReplay(ctx, tx, m); err != nil || matched {
		if err != nil {
			return Result{}, err
		}
		return replay, tx.Commit()
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM tasks WHERE id=?", taskID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, &domain.Error{Code: "not_found", Message: "task not found"}
		}
		return Result{}, err
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

// AttachReview uses the stored result as authority for task/repo/base/head.
// The request values are checked, never copied into the persisted identity.
func (s *Store) AttachReview(ctx context.Context, taskID, resultID string, m Mutation, in AttachReviewInput) (Result, error) {
	if err := validateAttachReview(taskID, resultID, m.Key, in); err != nil {
		return Result{}, err
	}
	m.Hash = observerHash(in)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, matched, err := resultReplay(ctx, tx, m); err != nil || matched {
		if err != nil {
			return Result{}, err
		}
		return replay, tx.Commit()
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
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM tasks WHERE id=?", taskID).Scan(&projectID); err != nil {
		return Result{}, err
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

func loadTaskReview(ctx context.Context, tx *sql.Tx, id string) (TaskReview, error) {
	var review TaskReview
	var data string
	err := tx.QueryRowContext(ctx, "SELECT record FROM task_reviews WHERE id=?", id).Scan(&data)
	if err != nil {
		return review, err
	}
	return review, json.Unmarshal([]byte(data), &review)
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
