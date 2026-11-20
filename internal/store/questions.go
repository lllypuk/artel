package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

const (
	questionMaxText = 4096
	questionMaxPage = 5
	// DeliveryOwnerQuestionsCapability is fixed to a writer at attach time.
	DeliveryOwnerQuestionsCapability = "owner_questions_v1"
)

// OwnerQuestion is immutable Pi-authored context plus at most one owner answer.
// AnswerStatus is derived from the ordinary delivery queue on every read.
type OwnerQuestion struct {
	ID                string `json:"id"`
	TaskID            string `json:"task_id"`
	BindingID         string `json:"delivery_binding_id"`
	PiSessionID       string `json:"pi_session_id"`
	Text              string `json:"text"`
	Status            string `json:"status"`
	AnswerMessageID   string `json:"answer_message_id,omitempty"`
	AnswerText        string `json:"answer_text,omitempty"`
	AnswerStatus      string `json:"answer_status,omitempty"`
	AnswerBlockReason string `json:"answer_block_reason,omitempty"`
	CreatedAt         string `json:"created_at"`
	AnsweredAt        string `json:"answered_at,omitempty"`
}

type OwnerQuestionCreate struct {
	Key        string
	InstanceID string
	TaskID     string
	WriterID   string
	Text       string
}

type OwnerQuestionCreateResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type OwnerQuestionAnswer struct {
	Key        string
	InstanceID string
	Text       string
}

type OwnerQuestionAnswerResult struct {
	QuestionID string `json:"question_id"`
	MessageID  string `json:"message_id"`
	Status     string `json:"status"`
}

func questionTextValid(text string) bool {
	if text == "" || len(text) > questionMaxText || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if r == 0 {
			return false
		}
	}
	return true
}

func ownerQuestionsCapable(b DeliveryBinding) bool {
	return b.AllowOwnerQuestions && b.OwnerQuestionsCapability == DeliveryOwnerQuestionsCapability
}

// ownerQuestionWriterGate proves the exact current writer and the capability
// fixed at attach; a field added to a later request cannot confer permission.
func ownerQuestionWriterGate(ctx context.Context, tx *sql.Tx, bindingID, writerID string) (DeliveryBinding, error) {
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return b, err
	}
	if err := ownerQuestionAssignmentGate(ctx, tx, b, writerID); err != nil {
		return b, err
	}
	return b, nil
}

func ownerQuestionAssignmentGate(ctx context.Context, tx *sql.Tx, b DeliveryBinding, writerID string) error {
	if err := deliveryAssignmentCheck(ctx, tx, b); err != nil {
		return err
	}
	if b.WriterID != writerID {
		return observerError("owner_conflict")
	}
	if !ownerQuestionsCapable(b) {
		return observerError("capability_required")
	}
	return nil
}

func questionTaskOpen(ctx context.Context, tx *sql.Tx, taskID, projectID string) error {
	var state string
	var archived int
	if err := tx.QueryRowContext(ctx, `SELECT t.state,p.archived FROM tasks t
		JOIN projects p ON p.id=t.project_id WHERE t.id=? AND t.project_id=?`, taskID, projectID).Scan(&state, &archived); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return observerError("not_found")
		}
		return err
	}
	if archived != 0 || state == string(domain.Done) || state == string(domain.Cancelled) {
		return observerError("closed")
	}
	return nil
}

func validateOwnerQuestionCreate(bindingID string, in OwnerQuestionCreate) error {
	if !observerUUID(bindingID) || !observerUUID(in.Key) || !observerUUID(in.InstanceID) ||
		!observerUUID(in.TaskID) || !observerUUID(in.WriterID) || !questionTextValid(in.Text) {
		return observerError("invalid_request")
	}
	return nil
}

// CreateOwnerQuestion stores only the question and its replay receipt. The
// original Pi tool call completes without waiting for an owner answer.
func (s *Store) CreateOwnerQuestion(ctx context.Context, bindingID string, in OwnerQuestionCreate) (OwnerQuestionCreateResult, error) {
	var result OwnerQuestionCreateResult
	if err := validateOwnerQuestionCreate(bindingID, in); err != nil {
		return result, err
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	instance, err := observerDatabase(ctx, tx)
	if err != nil {
		return result, err
	}
	if instance != in.InstanceID {
		return result, observerError("instance_mismatch")
	}
	b, err := ownerQuestionWriterGate(ctx, tx, bindingID, in.WriterID)
	if err != nil {
		return result, err
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM tasks WHERE id=?", in.TaskID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, observerError("not_found")
		}
		return result, err
	}
	if projectID != b.ProjectID {
		return result, observerError("assignment_mismatch")
	}
	requestHash := observerHash(struct {
		Instance, Task, Text string
	}{in.InstanceID, in.TaskID, in.Text})
	principal := "question.create:" + bindingID
	matched, err := deliveryReplayLookup(ctx, tx, principal, in.Key, requestHash, &result)
	if err != nil || matched {
		return result, err
	}
	if err := questionTaskOpen(ctx, tx, in.TaskID, projectID); err != nil {
		return result, err
	}
	question := OwnerQuestion{ID: uuid.NewString(), TaskID: in.TaskID, BindingID: bindingID,
		PiSessionID: b.PiSessionID, Text: in.Text, Status: "open",
		CreatedAt: s.observerClock().UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(question)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO owner_questions(id,task_id,binding_id,status,record) VALUES(?,?,?,'open',?)",
		question.ID, question.TaskID, bindingID, string(encoded)); err != nil {
		return result, err
	}
	result = OwnerQuestionCreateResult{ID: question.ID, Status: "stored"}
	receipt, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)",
		principal, in.Key, requestHash, string(receipt)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func ownerQuestionLoad(ctx context.Context, tx *sql.Tx, taskID, questionID string) (OwnerQuestion, error) {
	var question OwnerQuestion
	var data string
	err := tx.QueryRowContext(ctx, "SELECT record FROM owner_questions WHERE id=? AND task_id=?", questionID, taskID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return question, observerError("not_found")
	}
	if err != nil {
		return question, err
	}
	return question, json.Unmarshal([]byte(data), &question)
}

func validateOwnerQuestionAnswer(taskID, questionID string, in OwnerQuestionAnswer) error {
	if !observerUUID(taskID) || !observerUUID(questionID) || !observerUUID(in.Key) ||
		!observerUUID(in.InstanceID) || !questionTextValid(in.Text) {
		return observerError("invalid_request")
	}
	return nil
}

func ownerAnswerGate(ctx context.Context, tx *sql.Tx, taskID string, question OwnerQuestion) (DeliveryBinding, error) {
	b, err := deliveryLoad(ctx, tx, question.BindingID)
	if err != nil {
		return b, err
	}
	if err := deliveryAssignmentCheck(ctx, tx, b); err != nil {
		return b, err
	}
	if !ownerQuestionsCapable(b) {
		return b, observerError("capability_required")
	}
	var projectID string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM tasks WHERE id=?", taskID).Scan(&projectID); err != nil {
		return b, err
	}
	if projectID != b.ProjectID {
		return b, observerError("assignment_mismatch")
	}
	return b, questionTaskOpen(ctx, tx, taskID, projectID)
}

// AnswerOwnerQuestion commits the immutable answer and its 4a queue row in one
// immediate transaction. Its replay confirms storage, never live delivery state.
func (s *Store) AnswerOwnerQuestion(ctx context.Context, taskID, questionID string, in OwnerQuestionAnswer) (OwnerQuestionAnswerResult, error) {
	var result OwnerQuestionAnswerResult
	if err := validateOwnerQuestionAnswer(taskID, questionID, in); err != nil {
		return result, err
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	instance, err := observerDatabase(ctx, tx)
	if err != nil {
		return result, err
	}
	if instance != in.InstanceID {
		return result, observerError("instance_mismatch")
	}
	question, err := ownerQuestionLoad(ctx, tx, taskID, questionID)
	if err != nil {
		return result, err
	}
	requestHash := observerHash(struct {
		Instance, Task, Question, Text string
	}{in.InstanceID, taskID, questionID, in.Text})
	principal := "question.answer:" + questionID
	matched, err := deliveryReplayLookup(ctx, tx, principal, in.Key, requestHash, &result)
	if err != nil || matched {
		return result, err
	}
	if question.Status != "open" {
		return result, observerError("closed")
	}
	b, err := ownerAnswerGate(ctx, tx, taskID, question)
	if err != nil {
		return result, err
	}
	message := DeliveryMessage{ID: uuid.NewString(), TaskID: taskID, BindingID: b.ID, Kind: "answer",
		QuestionID: question.ID, QuestionText: question.Text, Text: in.Text, Status: "stored",
		CreatedAt: s.observerClock().UTC().Format(time.RFC3339Nano)}
	encodedMessage, err := json.Marshal(message)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_messages(id,task_id,binding_id,receipt_key,record) VALUES(?,?,?,?,?)",
		message.ID, taskID, b.ID, uuid.NewString(), string(encodedMessage)); err != nil {
		return result, err
	}
	question.Status = "answered"
	question.AnswerMessageID = message.ID
	question.AnswerText = in.Text
	question.AnsweredAt = message.CreatedAt
	encodedQuestion, err := json.Marshal(question)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE owner_questions SET status='answered',answer_message_id=?,record=? WHERE id=?",
		message.ID, string(encodedQuestion), question.ID); err != nil {
		return result, err
	}
	result = OwnerQuestionAnswerResult{QuestionID: question.ID, MessageID: message.ID, Status: "stored"}
	receipt, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)",
		principal, in.Key, requestHash, string(receipt)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func validateOwnerQuestionList(taskID, instanceID string, after int64, ids []string) error {
	if !observerUUID(taskID) || !observerUUID(instanceID) || after < 0 || len(ids) > questionMaxPage {
		return observerError("invalid_request")
	}
	for _, id := range ids {
		if !observerUUID(id) {
			return observerError("invalid_request")
		}
	}
	return nil
}

func questionPageLimit(limit int) int {
	if limit <= 0 || limit > questionMaxPage {
		return questionMaxPage
	}
	return limit
}

// ListOwnerQuestions pins the database identity in the same read transaction.
// It supports the thin selected-task page and refresh of already loaded IDs.
func (s *Store) ListOwnerQuestions(ctx context.Context, taskID, instanceID string, after int64, limit int, ids []string) ([]OwnerQuestion, int64, error) {
	if err := validateOwnerQuestionList(taskID, instanceID, after, ids); err != nil {
		return nil, 0, err
	}
	limit = questionPageLimit(limit)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	instance, err := observerDatabase(ctx, tx)
	if err != nil {
		return nil, 0, err
	}
	if instance != instanceID {
		return nil, 0, observerError("instance_mismatch")
	}
	query := `SELECT q.seq,q.record,coalesce(m.record,''),coalesce(m.blocked,0),coalesce(m.block_reason,''),
		coalesce((SELECT a.state FROM delivery_attempts a WHERE a.message_seq=m.seq),'')
		FROM owner_questions q LEFT JOIN delivery_messages m ON m.id=q.answer_message_id `
	args := []any{taskID}
	if len(ids) != 0 {
		query += "WHERE q.task_id=? AND q.id IN (SELECT value FROM json_each(?)) ORDER BY q.seq"
		args = append(args, mustJSON(ids))
	} else {
		query += "WHERE q.task_id=? AND q.seq>? ORDER BY q.seq LIMIT ?"
		args = append(args, after, limit+1)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	questions := []OwnerQuestion{}
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		var data, messageData, blockReason, attemptState string
		var blocked int
		if err := rows.Scan(&seq, &data, &messageData, &blocked, &blockReason, &attemptState); err != nil {
			return nil, 0, err
		}
		var question OwnerQuestion
		if err := json.Unmarshal([]byte(data), &question); err != nil {
			return nil, 0, err
		}
		if messageData != "" {
			var message DeliveryMessage
			if err := json.Unmarshal([]byte(messageData), &message); err != nil {
				return nil, 0, err
			}
			question.AnswerStatus = deliveryStatus(message.Status, blocked != 0, attemptState)
			if message.Status != "observed" {
				question.AnswerBlockReason = blockReason
			}
		}
		questions = append(questions, question)
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	next := after
	if len(ids) == 0 && len(questions) > limit {
		questions = questions[:limit]
		seqs = seqs[:limit]
	}
	if len(seqs) != 0 {
		next = seqs[len(seqs)-1]
	}
	return questions, next, tx.Commit()
}

// deliveryBlockOwnerQuestions is shared by explicit and stale-assignment
// revoke. Saved answers stay readable; only unanswered questions close and
// undelivered answer rows become permanently blocked.
func deliveryBlockOwnerQuestions(ctx context.Context, tx *sql.Tx, bindingID string) error {
	var tables int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='owner_questions'").Scan(&tables); err != nil || tables == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE owner_questions SET status='closed',record=json_set(record,'$.status','closed')
		WHERE binding_id=? AND status='open'`, bindingID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE delivery_messages SET blocked=1,block_reason='delivery_revoked'
		WHERE binding_id=? AND blocked=0 AND json_extract(record,'$.kind')='answer' AND json_extract(record,'$.status')!='observed'`, bindingID)
	return err
}
