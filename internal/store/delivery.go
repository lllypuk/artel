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
	deliveryMaxText  = 8192
	deliveryMaxPage  = 50
	deliveryReadyTTL = 10 * time.Second
)

// DeliveryBinding is the immutable owner-granted assignment for one Pi session.
type DeliveryBinding struct {
	ID          string `json:"delivery_binding_id"`
	ObserverID  string `json:"observer_id"`
	RuntimeID   string `json:"runtime_id"`
	ProjectID   string `json:"project_id"`
	PiSessionID string `json:"pi_session_id"`
	JournalID   string `json:"journal_id"`
	Generation  int64  `json:"generation"`
	Status      string `json:"status"` // pending, active, revoked
	WriterID    string `json:"writer_id,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// DeliveryMessage is one owner commission. Status: stored, unknown, observed.
// An unobserved commission reads as unknown once its delivery can no longer
// be confirmed (blocked, or a spent/verified attempt without a receipt).
type DeliveryMessage struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	BindingID   string `json:"delivery_binding_id"`
	Text        string `json:"text"`
	Status      string `json:"status"`
	Blocked     bool   `json:"blocked"`
	BlockReason string `json:"block_reason,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// DeliveryOwnerMutation is owner-only enable/revoke with idempotent receipts.
type DeliveryOwnerMutation struct {
	ObserverID  string
	Key         string
	Action      string // enable, revoke
	ExpectedVer int64
	BindingID   string
	JournalID   string
	Verifier    string
	RuntimeID   string
	ProjectID   string
	PiSessionID string
	InstanceID  string
}

// DeliveryAppend is one owner POST /v1/tasks/{id}/messages request.
type DeliveryAppend struct {
	Key        string
	Text       string
	BindingID  string
	InstanceID string
}

// DeliveryAttemptResult reports the spend/verify state without ever re-issuing a right.
type DeliveryAttemptResult struct {
	MessageID string `json:"message_id"`
	AttemptID string `json:"attempt_id"`
	State     string `json:"state"` // spent, verified, observed
	Right     bool   `json:"right"`
	Verified  bool   `json:"verified"`
}

func deliveryNewID() string { return uuid.NewString() }

func deliveryLoad(ctx context.Context, tx *sql.Tx, id string) (DeliveryBinding, error) {
	var data string
	var b DeliveryBinding
	err := tx.QueryRowContext(ctx, "SELECT record FROM delivery_bindings WHERE id=?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return b, observerError("not_found")
	}
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal([]byte(data), &b)
}

func deliverySave(ctx context.Context, tx *sql.Tx, b DeliveryBinding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE delivery_bindings SET record=? WHERE id=?", string(data), b.ID)
	return err
}

func (s *Store) deliveryAuth(ctx context.Context, tx *sql.Tx, verifier string) (DeliveryBinding, error) {
	var b DeliveryBinding
	if !observerVerifier(verifier) {
		return b, observerError("unauthorized")
	}
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM delivery_bindings WHERE verifier=?", verifier).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return b, observerError("unauthorized")
	}
	if err != nil {
		return b, err
	}
	b, err = deliveryLoad(ctx, tx, id)
	if err == nil && b.Status == "revoked" {
		return b, observerError("unauthorized")
	}
	return b, err
}

// AuthenticateDeliveryBinding checks the delivery-only verifier before body
// parsing and returns the immutable binding ID for request scoping. It runs
// inside one immediate transaction so concurrent revoke fences new calls.
func (s *Store) AuthenticateDeliveryBinding(ctx context.Context, verifier string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := s.deliveryAuth(ctx, tx, verifier)
	if err != nil {
		return "", err
	}
	return b.ID, tx.Commit()
}

func (s *Store) deliveryInvalidate(bindingID string) {
	s.deliveryReadyMu.Lock()
	delete(s.deliveryReady, bindingID)
	s.deliveryReadyMu.Unlock()
}

func (s *Store) deliveryReadyNow(bindingID string) bool {
	s.deliveryReadyMu.Lock()
	defer s.deliveryReadyMu.Unlock()
	until, ok := s.deliveryReady[bindingID]
	return ok && time.Now().Before(until)
}

func validateDeliveryOwnerMutation(m DeliveryOwnerMutation) error {
	if !observerUUID(m.ObserverID) || !observerUUID(m.Key) {
		return observerError("invalid_request")
	}
	switch m.Action {
	case "enable":
		if !observerUUID(m.BindingID) || !observerUUID(m.JournalID) || !observerVerifier(m.Verifier) ||
			!observerUUID(m.RuntimeID) || !observerUUID(m.ProjectID) || !observerUUID(m.PiSessionID) || !observerUUID(m.InstanceID) {
			return observerError("invalid_request")
		}
	case "revoke":
		if !observerUUID(m.BindingID) {
			return observerError("invalid_request")
		}
	default:
		return observerError("invalid_request")
	}
	return nil
}

// MutateDeliveryBinding is owner-only enable/revoke with idempotent receipts.
// Enable stays pending until a writer with the exact assignment attaches.
func (s *Store) MutateDeliveryBinding(ctx context.Context, m DeliveryOwnerMutation) (DeliveryBinding, error) {
	var result DeliveryBinding
	if err := validateDeliveryOwnerMutation(m); err != nil {
		return result, err
	}
	requestHash := observerHash(m)
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	principal := "observer:" + m.ObserverID
	matched, err := deliveryReplayLookup(ctx, tx, principal, m.Key, requestHash, &result)
	if err != nil || matched {
		return result, err
	}
	instance, err := observerDatabase(ctx, tx)
	if err != nil {
		return result, err
	}
	if m.Action == "enable" && instance != m.InstanceID {
		return result, observerError("instance_mismatch")
	}
	o, err := observerLoad(ctx, tx, m.ObserverID)
	if err != nil {
		return result, err
	}
	// Enable CASes on the owner-confirmed observer version; revoke names only
	// the binding ID, so a stale observer version or an already revoked observer
	// must not block or delay revoking its delivery binding.
	if m.Action == "enable" && (o.Revoked || o.Version != m.ExpectedVer) {
		return result, observerError("version_conflict")
	}
	if m.Action == "enable" {
		result, err = s.deliveryEnableApply(ctx, tx, o, m)
	} else {
		result, err = s.deliveryRevokeApply(ctx, tx, o, m)
	}
	if err != nil {
		return result, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", principal, m.Key, requestHash, string(encoded)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// deliveryReplayLookup returns the stored result for a matched request.
func deliveryReplayLookup(ctx context.Context, tx *sql.Tx, principal, key, requestHash string, out any) (bool, error) {
	var hash, data string
	err := tx.QueryRowContext(ctx, "SELECT request_hash,result FROM delivery_receipts WHERE principal=? AND key=?", principal, key).Scan(&hash, &data)
	if err == nil {
		if hash != requestHash {
			return true, observerError("idempotency_conflict")
		}
		return true, json.Unmarshal([]byte(data), out)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func (s *Store) deliveryEnableApply(ctx context.Context, tx *sql.Tx, o observerRecord, m DeliveryOwnerMutation) (DeliveryBinding, error) {
	var result DeliveryBinding
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM delivery_bindings WHERE json_extract(record,'$.status') != 'revoked'").Scan(&count); err != nil {
		return result, err
	}
	if count >= 1 {
		return result, observerError("slot_occupied")
	}
	var existing int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM delivery_bindings WHERE verifier=?", m.Verifier).Scan(&existing); err != nil {
		return result, err
	}
	if existing != 0 {
		return result, observerError("idempotency_conflict")
	}
	// One credential, one channel: an observer verifier must never double as
	// a delivery credential, or the same token would pass both auths.
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM observers WHERE verifier=?", m.Verifier).Scan(&existing); err != nil {
		return result, err
	}
	if existing != 0 {
		return result, observerError("idempotency_conflict")
	}
	if err := deliveryObserverAssignment(ctx, tx, o.ID, m.RuntimeID, m.ProjectID, m.PiSessionID, o.Generation); err != nil {
		return result, err
	}
	result = DeliveryBinding{ID: m.BindingID, ObserverID: o.ID, RuntimeID: m.RuntimeID, ProjectID: m.ProjectID,
		PiSessionID: m.PiSessionID, JournalID: m.JournalID, Generation: o.Generation, Status: "pending",
		CreatedAt: s.observerClock().UTC().Format(time.RFC3339Nano)}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO delivery_bindings(id,verifier,record) VALUES(?,?,?)", result.ID, m.Verifier, string(encoded))
	return result, err
}

func (s *Store) deliveryRevokeApply(ctx context.Context, tx *sql.Tx, o observerRecord, m DeliveryOwnerMutation) (DeliveryBinding, error) {
	var result DeliveryBinding
	b, err := deliveryLoad(ctx, tx, m.BindingID)
	if err != nil {
		return result, err
	}
	if b.ObserverID != o.ID {
		return result, observerError("not_found")
	}
	b.Status = "revoked"
	b.WriterID = ""
	if err := deliverySave(ctx, tx, b); err != nil {
		return result, err
	}
	s.deliveryInvalidate(b.ID)
	return b, nil
}

// DeliveryBindingStatus is the owner/CLI verification read; never exposes secrets.
func (s *Store) DeliveryBindingStatus(ctx context.Context, observerID, bindingID string) (DeliveryBinding, error) {
	var result DeliveryBinding
	if !observerUUID(observerID) || !observerUUID(bindingID) {
		return result, observerError("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return result, err
	}
	if b.ObserverID != observerID {
		return result, observerError("not_found")
	}
	b.WriterID = "" // Ownership markers are writer-private.
	return b, tx.Commit()
}

func deliveryTextValid(text string) bool {
	if text == "" || len(text) > deliveryMaxText || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if r == 0 {
			return false
		}
	}
	return true
}

// AppendDeliveryMessage pins the database instance first, then serves a matched
// replay even after revoke/close. Freshness gates apply only to new records.
func (s *Store) AppendDeliveryMessage(ctx context.Context, taskID string, m DeliveryAppend) (DeliveryMessage, error) {
	var result DeliveryMessage
	if !observerUUID(taskID) || !observerUUID(m.Key) || !observerUUID(m.BindingID) || !observerUUID(m.InstanceID) || !deliveryTextValid(m.Text) {
		return result, observerError("invalid_request")
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
	if instance != m.InstanceID {
		return result, observerError("instance_mismatch")
	}
	principal := "task:" + taskID
	requestHash := observerHash(struct {
		Task, Key, Text, Binding string
	}{taskID, m.Key, m.Text, m.BindingID})
	matched, err := deliveryReplayLookup(ctx, tx, principal, m.Key, requestHash, &result)
	if err != nil || matched {
		return result, err
	}
	b, err := s.deliveryAppendGate(ctx, tx, taskID, m.BindingID)
	if err != nil {
		return result, err
	}
	result = DeliveryMessage{ID: deliveryNewID(), TaskID: taskID, BindingID: b.ID, Text: m.Text,
		Status: "stored", CreatedAt: s.observerClock().UTC().Format(time.RFC3339Nano)}
	receiptKey := deliveryNewID()
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_messages(id,task_id,binding_id,receipt_key,record) VALUES(?,?,?,?,?)",
		result.ID, taskID, b.ID, receiptKey, string(encoded)); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", principal, m.Key, requestHash, string(encoded)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// deliveryAppendGate applies the freshness gates for a new commission record.
func (s *Store) deliveryAppendGate(ctx context.Context, tx *sql.Tx, taskID, bindingID string) (DeliveryBinding, error) {
	var b DeliveryBinding
	var projectID, state string
	if err := tx.QueryRowContext(ctx, "SELECT project_id,state FROM tasks WHERE id=?", taskID).Scan(&projectID, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return b, observerError("not_found")
		}
		return b, err
	}
	var archived int
	if err := tx.QueryRowContext(ctx, "SELECT archived FROM projects WHERE id=?", projectID).Scan(&archived); err != nil {
		return b, err
	}
	if archived != 0 || state == string(domain.Done) || state == string(domain.Cancelled) {
		return b, observerError("closed")
	}
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return b, err
	}
	if b.Status != "active" || !s.deliveryReadyNow(b.ID) {
		return b, observerError("not_ready")
	}
	if err := deliveryAssignmentCheck(ctx, tx, b); err != nil {
		return b, err
	}
	if projectID != b.ProjectID {
		return b, observerError("assignment_mismatch")
	}
	return b, nil
}

// ListDeliveryMessages returns one task page in record order with statuses.
func (s *Store) ListDeliveryMessages(ctx context.Context, taskID string, after int64, limit int, ids []string) ([]DeliveryMessage, int64, error) {
	if !observerUUID(taskID) {
		return nil, 0, observerError("invalid_request")
	}
	if limit <= 0 || limit > deliveryMaxPage {
		limit = deliveryMaxPage
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	next := after
	if len(ids) != 0 {
		if len(ids) > deliveryMaxPage {
			return nil, 0, observerError("invalid_request")
		}
		for _, id := range ids {
			if !observerUUID(id) {
				return nil, 0, observerError("invalid_request")
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT m.seq,m.blocked,m.block_reason,m.record,
			coalesce((SELECT a.state FROM delivery_attempts a WHERE a.message_seq=m.seq),'') FROM delivery_messages m
			WHERE m.task_id=? AND m.id IN (SELECT value FROM json_each(?)) ORDER BY m.seq`,
			taskID, mustJSON(ids))
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = rows.Close() }()
		messages, _, err := deliveryScan(rows)
		if err != nil {
			return nil, 0, err
		}
		return messages, next, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.seq,m.blocked,m.block_reason,m.record,
		coalesce((SELECT a.state FROM delivery_attempts a WHERE a.message_seq=m.seq),'') FROM delivery_messages m
		WHERE m.task_id=? AND m.seq>? ORDER BY m.seq LIMIT ?`,
		taskID, after, limit+1)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	all, seqs, err := deliveryScan(rows)
	if err != nil {
		return nil, 0, err
	}
	if len(all) > limit {
		all = all[:limit]
		seqs = seqs[:limit]
	}
	if len(all) != 0 {
		next = seqs[len(seqs)-1]
	}
	return all, next, tx.Commit()
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// deliveryScan derives the owner-visible status: observed stands; an unobserved
// commission whose confirmation became impossible reads as unknown.
func deliveryScan(rows *sql.Rows) ([]DeliveryMessage, []int64, error) {
	messages := []DeliveryMessage{}
	seqs := []int64{}
	for rows.Next() {
		var seq int64
		var blocked int
		var reason, attemptState, data string
		var m DeliveryMessage
		var rawReason any
		if err := rows.Scan(&seq, &blocked, &rawReason, &data, &attemptState); err != nil {
			return nil, nil, err
		}
		if rawReason != nil {
			reason, _ = rawReason.(string)
		}
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			return nil, nil, err
		}
		m.Blocked = blocked != 0
		m.BlockReason = reason
		if m.Status != "observed" && (m.Blocked || attemptState != "") {
			m.Status = "unknown"
		}
		messages = append(messages, m)
		seqs = append(seqs, seq)
	}
	return messages, seqs, rows.Err()
}

// DeliveryTaskProjection powers the task-screen assignment line without secrets.
func (s *Store) DeliveryTaskProjection(ctx context.Context, taskID string) (map[string]any, error) {
	if !observerUUID(taskID) {
		return nil, observerError("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var projectID string
	if err := tx.QueryRowContext(ctx, "SELECT project_id FROM tasks WHERE id=?", taskID).Scan(&projectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, observerError("not_found")
		}
		return nil, err
	}
	projection := map[string]any{"task_id": taskID, "project_id": projectID, "binding": nil}
	row := tx.QueryRowContext(ctx, "SELECT record FROM delivery_bindings WHERE json_extract(record,'$.status') != 'revoked' LIMIT 1")
	var data string
	if err := row.Scan(&data); err == nil {
		var b DeliveryBinding
		if err := json.Unmarshal([]byte(data), &b); err != nil {
			return nil, err
		}
		b.WriterID = ""
		projection["binding"] = b
		projection["ready"] = s.deliveryReadyNow(b.ID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return projection, tx.Commit()
}

// DeliveryContext is the writer's metadata read; no message text is returned here.
func (s *Store) DeliveryContext(ctx context.Context, bindingID string) (map[string]any, error) {
	if !observerUUID(bindingID) {
		return nil, observerError("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return nil, err
	}
	instance, err := observerDatabase(ctx, tx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"database_instance_id": instance,
		"delivery_binding_id":  b.ID,
		"status":               b.Status,
		"observer_id":          b.ObserverID,
		"runtime_id":           b.RuntimeID,
		"project_id":           b.ProjectID,
		"pi_session_id":        b.PiSessionID,
		"journal_id":           b.JournalID,
		"generation":           b.Generation,
		"writer_id":            b.WriterID,
		"ready":                s.deliveryReadyNow(b.ID),
	}, tx.Commit()
}

// DeliveryAttach CAS-registers the writer; polling starts only after success.
// The writer proves its exact runtime and Pi session identity at attach time.
func (s *Store) DeliveryAttach(ctx context.Context, bindingID, writerID, expectedPrevious, journalID, runtimeID, piSessionID string) error {
	if !observerUUID(bindingID) || !observerUUID(writerID) || !observerUUID(journalID) || !observerUUID(runtimeID) || !observerUUID(piSessionID) {
		return observerError("invalid_request")
	}
	if expectedPrevious != "" && !observerUUID(expectedPrevious) {
		return observerError("invalid_request")
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return err
	}
	if b.Status == "revoked" {
		return observerError("revoked")
	}
	if b.JournalID != journalID {
		return observerError("assignment_mismatch")
	}
	// Activation requires the observer's current binding to match exactly, so a
	// writer in any other runtime can never activate this assignment.
	if err := deliveryAttachChecks(ctx, tx, b, runtimeID, piSessionID); err != nil {
		return err
	}
	switch b.WriterID {
	case writerID:
		// Re-confirm after a lost ACK; ownership is unchanged.
	case expectedPrevious:
		b.WriterID = writerID
		b.Status = "active"
		if err := deliverySave(ctx, tx, b); err != nil {
			return err
		}
		s.deliveryInvalidate(b.ID)
	default:
		return observerError("owner_conflict")
	}
	return tx.Commit()
}

// deliveryAttachChecks verifies the full immutable assignment for the writer.
func deliveryAttachChecks(ctx context.Context, tx *sql.Tx, b DeliveryBinding, runtimeID, piSessionID string) error {
	if b.RuntimeID != runtimeID || b.PiSessionID != piSessionID {
		return observerError("assignment_mismatch")
	}
	return deliveryAssignmentCurrent(ctx, tx, b)
}

// DeliveryReady is the writer's bounded liveness confirmation in daemon memory.
func (s *Store) DeliveryReady(ctx context.Context, bindingID, writerID string) error {
	if !observerUUID(bindingID) || !observerUUID(writerID) {
		return observerError("invalid_request")
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return err
	}
	if b.Status != "active" || b.WriterID != writerID {
		return observerError("owner_conflict")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.deliveryReadyMu.Lock()
	s.deliveryReady[bindingID] = time.Now().Add(deliveryReadyTTL)
	s.deliveryReadyMu.Unlock()
	return nil
}

// DeliveryNext returns the oldest unspent, unblocked commission with its receipt key.
func (s *Store) DeliveryNext(ctx context.Context, bindingID, writerID string) (DeliveryMessage, string, error) {
	var result DeliveryMessage
	var receiptKey string
	if !observerUUID(bindingID) || !observerUUID(writerID) {
		return result, "", observerError("invalid_request")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, "", err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return result, "", err
	}
	if err := deliveryAssignmentCheck(ctx, tx, b); err != nil {
		return result, "", err
	}
	if b.WriterID != writerID {
		return result, "", observerError("owner_conflict")
	}
	row := tx.QueryRowContext(ctx, `SELECT m.seq,m.blocked,m.receipt_key,m.record FROM delivery_messages m
		WHERE m.binding_id=? AND m.blocked=0 AND NOT EXISTS (SELECT 1 FROM delivery_attempts a WHERE a.message_seq=m.seq)
		ORDER BY m.seq LIMIT 1`, bindingID)
	var seq int64
	var blocked int
	var data string
	if err := row.Scan(&seq, &blocked, &receiptKey, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, "", tx.Commit()
		}
		return result, "", err
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return result, "", err
	}
	result.Blocked = blocked != 0
	return result, receiptKey, tx.Commit()
}

// DeliverySpend admits exactly one right per message. A repeated or concurrent
// spend reports the state without ever returning a right again.
func (s *Store) DeliverySpend(ctx context.Context, bindingID, writerID, messageID string) (DeliveryAttemptResult, error) {
	result := DeliveryAttemptResult{MessageID: messageID}
	if !observerUUID(bindingID) || !observerUUID(writerID) || !observerUUID(messageID) {
		return result, observerError("invalid_request")
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return result, err
	}
	if err := deliveryAssignmentCheck(ctx, tx, b); err != nil {
		return result, err
	}
	if b.WriterID != writerID {
		return result, observerError("owner_conflict")
	}
	var seq int64
	var blocked int
	if err := tx.QueryRowContext(ctx, "SELECT seq,blocked FROM delivery_messages WHERE id=? AND binding_id=?", messageID, bindingID).Scan(&seq, &blocked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, observerError("not_found")
		}
		return result, err
	}
	if blocked != 0 {
		return result, observerError("closed")
	}
	var attemptID, state, attemptWriter, record string
	err = tx.QueryRowContext(ctx, "SELECT attempt_id,state,writer_id,record FROM delivery_attempts WHERE message_seq=?", seq).
		Scan(&attemptID, &state, &attemptWriter, &record)
	if err == nil {
		var attempt DeliveryAttemptResult
		if err := json.Unmarshal([]byte(record), &attempt); err != nil {
			return result, err
		}
		attempt.Right = false
		return attempt, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	attemptID = deliveryNewID()
	result = DeliveryAttemptResult{MessageID: messageID, AttemptID: attemptID, State: "spent", Right: true}
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_attempts(attempt_id,message_seq,binding_id,writer_id,state,record) VALUES(?,?,?,?,'spent',?)",
		attemptID, seq, bindingID, writerID, string(encoded)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// DeliveryVerify confirms the right of the owning writer; it never grants one.
func (s *Store) DeliveryVerify(ctx context.Context, bindingID, writerID, attemptID string) (DeliveryAttemptResult, error) {
	result := DeliveryAttemptResult{AttemptID: attemptID}
	if !observerUUID(bindingID) || !observerUUID(writerID) || !observerUUID(attemptID) {
		return result, observerError("invalid_request")
	}
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return result, err
	}
	if b.Status == "revoked" {
		return result, observerError("revoked")
	}
	// A stale assignment (rebind, generation change, observer revoke) must not
	// confirm a call: verification is part of the exact assignment.
	if err := deliveryAssignmentCurrent(ctx, tx, b); err != nil {
		return result, err
	}
	if b.WriterID != writerID {
		return result, observerError("owner_conflict")
	}
	var seq int64
	var state, attemptWriter, data string
	if err := tx.QueryRowContext(ctx, "SELECT message_seq,state,writer_id,record FROM delivery_attempts WHERE attempt_id=? AND binding_id=?",
		attemptID, bindingID).Scan(&seq, &state, &attemptWriter, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, observerError("not_found")
		}
		return result, err
	}
	if attemptWriter != writerID {
		return result, observerError("owner_conflict")
	}
	if err := json.Unmarshal([]byte(data), &result); err != nil {
		return result, err
	}
	if state == "observed" {
		result.Verified = true
		result.Right = false
		return result, tx.Commit()
	}
	var blocked int
	if err := tx.QueryRowContext(ctx, "SELECT blocked FROM delivery_messages WHERE seq=?", seq).Scan(&blocked); err != nil {
		return result, err
	}
	if blocked != 0 {
		// Closing after spend forbids confirmation; the attempt stays unknown.
		return result, observerError("closed")
	}
	if state == "spent" {
		if err := deliveryAttemptState(ctx, tx, attemptID, "verified"); err != nil {
			return result, err
		}
		result.State = "verified"
	}
	result.Verified = true
	result.Right = false
	return result, tx.Commit()
}

// DeliveryReceipt publishes the observed start idempotently per binding key.
// A new writer with the unchanged assignment may republish a stored receipt,
// but never acquires the spent attempt's right.
func (s *Store) DeliveryReceipt(ctx context.Context, bindingID, key string, receipt struct {
	MessageID, AttemptID, Type, MessageType, PiSessionID string
	Details                                              map[string]any
}) (DeliveryMessage, error) {
	var result DeliveryMessage
	if !observerUUID(bindingID) || !observerUUID(key) {
		return result, observerError("invalid_request")
	}
	requestHash := observerHash(receipt)
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	principal := "binding:" + bindingID
	matched, err := deliveryReplayLookup(ctx, tx, principal, key, requestHash, &result)
	if err != nil || matched {
		return result, err
	}
	seq, attemptState, messageData, err := s.deliveryReceiptGate(ctx, tx, bindingID, key, receipt)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal([]byte(messageData), &result); err != nil {
		return result, err
	}
	result.Status = "observed"
	result.Blocked = false
	encoded, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE delivery_messages SET record=? WHERE seq=?", string(encoded), seq); err != nil {
		return result, err
	}
	if attemptState != "observed" {
		if err := deliveryAttemptState(ctx, tx, receipt.AttemptID, "observed"); err != nil {
			return result, err
		}
	}
	receiptOut, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO delivery_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", principal, key, requestHash, string(receiptOut)); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// deliveryReceiptGate validates form, identity, attempt and assignment, and
// returns the message row position, attempt state and record for publishing.
func (s *Store) deliveryReceiptGate(ctx context.Context, tx *sql.Tx, bindingID, receiptKey string, receipt struct {
	MessageID, AttemptID, Type, MessageType, PiSessionID string
	Details                                              map[string]any
}) (int64, string, string, error) {
	b, err := deliveryLoad(ctx, tx, bindingID)
	if err != nil {
		return 0, "", "", err
	}
	if b.Status == "revoked" {
		return 0, "", "", observerError("revoked")
	}
	// A first receipt under a stale assignment (observer revoke, rebind,
	// generation change) is always rejected; only the exact current assignment
	// may publish, and only the key handed out with the message is accepted.
	if err := deliveryAssignmentCurrent(ctx, tx, b); err != nil {
		return 0, "", "", err
	}
	if receipt.Type != "before_agent_start" || receipt.MessageType != "custom" || receipt.PiSessionID != b.PiSessionID ||
		receipt.Details == nil || receipt.Details["message_id"] != receipt.MessageID || receipt.Details["attempt_id"] != receipt.AttemptID {
		return 0, "", "", observerError("invalid_request")
	}
	var seq int64
	var blocked int
	var storedKey, messageData string
	if err := tx.QueryRowContext(ctx, "SELECT seq,blocked,receipt_key,record FROM delivery_messages WHERE id=? AND binding_id=?", receipt.MessageID, bindingID).
		Scan(&seq, &blocked, &storedKey, &messageData); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", "", observerError("not_found")
		}
		return 0, "", "", err
	}
	if storedKey != receiptKey {
		return 0, "", "", observerError("invalid_request")
	}
	var attemptState string
	if err := tx.QueryRowContext(ctx, "SELECT state FROM delivery_attempts WHERE attempt_id=? AND message_seq=? AND binding_id=?",
		receipt.AttemptID, seq, bindingID).Scan(&attemptState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, "", "", observerError("not_found")
		}
		return 0, "", "", err
	}
	if blocked != 0 && attemptState != "verified" {
		// Closing before the call was confirmed forbids publication; an
		// already verified turn of an unchanged assignment may still publish.
		return 0, "", "", observerError("closed")
	}
	if attemptState == "spent" {
		// A receipt can only follow a confirmed call; a merely spent attempt
		// was never cleared to transmit.
		return 0, "", "", observerError("invalid_request")
	}
	return seq, attemptState, messageData, nil
}

func deliveryAttemptState(ctx context.Context, tx *sql.Tx, attemptID, state string) error {
	var data string
	if err := tx.QueryRowContext(ctx, "SELECT record FROM delivery_attempts WHERE attempt_id=?", attemptID).Scan(&data); err != nil {
		return err
	}
	var attempt DeliveryAttemptResult
	if err := json.Unmarshal([]byte(data), &attempt); err != nil {
		return err
	}
	attempt.State = state
	encoded, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE delivery_attempts SET state=?,record=? WHERE attempt_id=?", state, string(encoded), attemptID)
	return err
}

// deliveryObserverAssignment verifies the observer side of the immutable
// assignment: current binding, generation, and runtime registration session.
func deliveryObserverAssignment(ctx context.Context, tx *sql.Tx, observerID, runtimeID, projectID, piSessionID string, generation int64) error {
	o, err := observerLoad(ctx, tx, observerID)
	if err != nil {
		return err
	}
	if o.Revoked || o.Binding == nil || o.Binding.RuntimeID != runtimeID || o.Binding.ProjectID != projectID || o.Generation != generation {
		return observerError("assignment_mismatch")
	}
	if piSessionID != "" {
		r, err := observerRuntimeLoad(ctx, tx, observerID, runtimeID)
		if err != nil {
			return err
		}
		if r.Registration.PiSessionID != piSessionID {
			return observerError("assignment_mismatch")
		}
	}
	return nil
}

// deliveryAssignmentCurrent verifies the observer side without a status gate.
func deliveryAssignmentCurrent(ctx context.Context, tx *sql.Tx, b DeliveryBinding) error {
	return deliveryObserverAssignment(ctx, tx, b.ObserverID, b.RuntimeID, b.ProjectID, b.PiSessionID, b.Generation)
}

// deliveryAssignmentCheck verifies the full immutable assignment in the spend transaction.
func deliveryAssignmentCheck(ctx context.Context, tx *sql.Tx, b DeliveryBinding) error {
	if b.Status == "revoked" {
		return observerError("revoked")
	}
	if b.Status != "active" {
		return observerError("not_ready")
	}
	return deliveryAssignmentCurrent(ctx, tx, b)
}
