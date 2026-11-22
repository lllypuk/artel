package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

type questionEnv struct {
	deliveryEnv
	writer string
}

func mustQuestionEnv(t *testing.T, s *Store) questionEnv {
	t.Helper()
	p, task := seedAt(t, s, "/questions/"+uuid.NewString())
	observer, verifier, owner := mustObserver(t, s)
	runtime := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, observer, "bind", owner.Version, runtime.RuntimeID, p.ID)
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := s.MutateDeliveryBinding(t.Context(), DeliveryOwnerMutation{
		ObserverID: observer, Key: uuid.NewString(), Action: "enable", ExpectedVer: owner.Version,
		BindingID: uuid.NewString(), JournalID: uuid.NewString(), Verifier: observerHash(uuid.NewString()),
		RuntimeID: runtime.RuntimeID, ProjectID: p.ID, PiSessionID: runtime.PiSessionID,
		InstanceID: snap.InstanceID, AllowOwnerQuestions: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := uuid.NewString()
	if err := s.DeliveryAttach(t.Context(), binding.ID, snap.InstanceID, writer, "", binding.JournalID, runtime.RuntimeID, runtime.PiSessionID, DeliveryOwnerQuestionsCapability); err != nil {
		t.Fatal(err)
	}
	return questionEnv{deliveryEnv: deliveryEnv{project: p, task: task, observer: observer,
		observerVerifier: verifier, runtime: runtime, binding: binding, instance: snap.InstanceID}, writer: writer}
}

func (e questionEnv) mustReceipt(t *testing.T, s *Store, key string, receipt deliveryReceiptBodyT) {
	t.Helper()
	if _, err := s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writer, key, receipt); err != nil {
		t.Fatal(err)
	}
}

func (e questionEnv) create(t *testing.T, s *Store, key, taskID, writer, text string) (OwnerQuestionCreateResult, error) {
	t.Helper()
	return s.CreateOwnerQuestion(t.Context(), e.binding.ID, OwnerQuestionCreate{
		Key: key, InstanceID: e.instance, TaskID: taskID, WriterID: writer, Text: text,
	})
}

func (e questionEnv) mustCreate(t *testing.T, s *Store, key, text string) OwnerQuestionCreateResult {
	t.Helper()
	result, err := e.create(t, s, key, e.task.ID, e.writer, text)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "stored" || !observerUUID(result.ID) {
		t.Fatalf("question result: %+v", result)
	}
	return result
}

func (e questionEnv) answer(t *testing.T, s *Store, questionID, key, text string) (OwnerQuestionAnswerResult, error) {
	t.Helper()
	return s.AnswerOwnerQuestion(t.Context(), e.task.ID, questionID, OwnerQuestionAnswer{Key: key, InstanceID: e.instance, Text: text})
}

func (e questionEnv) mustAnswer(t *testing.T, s *Store, questionID, key, text string) OwnerQuestionAnswerResult {
	t.Helper()
	result, err := e.answer(t, s, questionID, key, text)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "stored" || result.QuestionID != questionID || !observerUUID(result.MessageID) {
		t.Fatalf("answer result: %+v", result)
	}
	return result
}

func (e questionEnv) answerReceipt(attempt DeliveryAttemptResult, questionID string) deliveryReceiptBodyT {
	receipt := e.receiptFor(attempt)
	receipt.Details["custom_type"] = "artel-owner-answer"
	receipt.Details["question_id"] = questionID
	return receipt
}

func questionByID(t *testing.T, s *Store, taskID, instanceID, questionID string) OwnerQuestion {
	t.Helper()
	page, err := s.ListOwnerQuestions(t.Context(), taskID, instanceID, 0, 0, []string{questionID})
	questions := page.Questions
	if err != nil || len(questions) != 1 {
		t.Fatalf("question refresh: %+v %v", questions, err)
	}
	return questions[0]
}

func TestOwnerQuestionCreateReplayAndScope(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	key := uuid.NewString()
	first := e.mustCreate(t, s, key, "line one\r\n\x01")
	replayed, err := e.create(t, s, key, e.task.ID, e.writer, "line one\r\n\x01")
	if err != nil || replayed != first {
		t.Fatalf("question replay: %+v %+v %v", first, replayed, err)
	}
	if _, err := e.create(t, s, key, e.task.ID, e.writer, "changed"); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("changed replay: %v", err)
	}
	// Writer identity is authenticated on every call but is not part of the
	// canonical payload. A replacement writer can recover the lost ACK.
	replacement := uuid.NewString()
	if err := s.DeliveryAttach(t.Context(), e.binding.ID, e.instance, replacement, e.writer, e.binding.JournalID, e.runtime.RuntimeID, e.runtime.PiSessionID, DeliveryOwnerQuestionsCapability); err != nil {
		t.Fatal(err)
	}
	replayed, err = e.create(t, s, key, e.task.ID, replacement, "line one\r\n\x01")
	if err != nil || replayed != first {
		t.Fatalf("replacement replay: %+v %v", replayed, err)
	}
	if _, err := e.create(t, s, uuid.NewString(), e.task.ID, e.writer, "stale writer"); !observerIs(err, "owner_conflict") {
		t.Fatalf("stale writer: %v", err)
	}
	foreign := OwnerQuestionCreate{Key: key, InstanceID: uuid.NewString(), TaskID: e.task.ID, WriterID: replacement, Text: "line one\r\n\x01"}
	if _, err := s.CreateOwnerQuestion(t.Context(), e.binding.ID, foreign); !observerIs(err, "instance_mismatch") {
		t.Fatalf("foreign instance replay: %v", err)
	}
	_, foreignTask := seedAt(t, s, "/questions/foreign/"+uuid.NewString())
	if _, err := e.create(t, s, key, foreignTask.ID, replacement, "foreign project"); !observerIs(err, "assignment_mismatch") {
		t.Fatalf("foreign project: %v", err)
	}
	if _, err := e.create(t, s, uuid.NewString(), uuid.NewString(), replacement, "missing"); !observerIs(err, "not_found") {
		t.Fatalf("missing task: %v", err)
	}
	for _, text := range []string{"", "nul\x00", string([]byte{0xff}), strings.Repeat("x", questionMaxText+1)} {
		if _, err := e.create(t, s, uuid.NewString(), e.task.ID, replacement, text); !observerIs(err, "invalid_request") {
			t.Fatalf("invalid question text: %v", err)
		}
	}
	if got := countRows(t, s, "owner_questions"); got != 1 {
		t.Fatalf("rejected creates left rows: %d", got)
	}
	// No readiness heartbeat is needed for owner answers, and no question or
	// answer text enters domain events or observer telemetry.
	answerKey := uuid.NewString()
	answer := e.mustAnswer(t, s, first.ID, answerKey, "answer without ready")
	if answer.MessageID == "" {
		t.Fatal("empty answer message")
	}
	events, err := s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	encodedEvents, _ := json.Marshal(events)
	if strings.Contains(string(encodedEvents), "line one") || strings.Contains(string(encodedEvents), "answer without") {
		t.Fatal("question text leaked into domain events")
	}
}

func TestOwnerQuestionRequiresOptInAndAttachedCapability(t *testing.T) {
	t.Run("plain grant", func(t *testing.T) {
		s := openTest(t)
		plain := mustDeliveryEnv(t, s)
		writer := uuid.NewString()
		plain.mustAttachReady(t, s, writer, "")
		_, err := s.CreateOwnerQuestion(t.Context(), plain.binding.ID, OwnerQuestionCreate{
			Key: uuid.NewString(), InstanceID: plain.instance, TaskID: plain.task.ID, WriterID: writer, Text: "not opted in",
		})
		if !observerIs(err, "capability_required") {
			t.Fatalf("plain grant: %v", err)
		}
		if got := countRows(t, s, "owner_questions"); got != 0 {
			t.Fatalf("unauthorized question rows: %d", got)
		}
	})
	t.Run("fixed attach capability", func(t *testing.T) {
		s := openTest(t)
		e := mustQuestionEnv(t, s)
		// A late capability field cannot replace the fixed attach capability.
		if err := s.DeliveryAttach(t.Context(), e.binding.ID, e.instance, e.writer, "", e.binding.JournalID, e.runtime.RuntimeID, e.runtime.PiSessionID, ""); !observerIs(err, "capability_required") {
			t.Fatalf("attach downgraded capability: %v", err)
		}
	})
}

func checkOwnerQuestionAnswerReplay(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	question := e.mustCreate(t, s, uuid.NewString(), "immutable question")
	key := uuid.NewString()
	first := e.mustAnswer(t, s, question.ID, key, "exact answer")
	replayed, err := e.answer(t, s, question.ID, key, "exact answer")
	if err != nil || replayed != first {
		t.Fatalf("answer replay: %+v %+v %v", first, replayed, err)
	}
	if _, err := e.answer(t, s, question.ID, key, "different answer"); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("changed answer replay: %v", err)
	}
	if _, err := e.answer(t, s, question.ID, uuid.NewString(), "second answer"); !observerIs(err, "closed") {
		t.Fatalf("second answer: %v", err)
	}
	stored := questionByID(t, s, e.task.ID, e.instance, question.ID)
	if stored.Text != "immutable question" || stored.AnswerText != "exact answer" || stored.Status != "answered" || stored.AnswerMessageID != first.MessageID {
		t.Fatalf("immutable pair: %+v", stored)
	}
	if got := countRows(t, s, "delivery_messages"); got != 1 {
		t.Fatalf("answer queue rows: %d", got)
	}
	// Keep the status and answer message ID intact so only the terminal-row
	// trigger can reject this otherwise valid update.
	if _, err := s.db.ExecContext(t.Context(), `UPDATE owner_questions SET record=json_set(record,'$.answer_text','rewritten') WHERE id=?`, question.ID); err == nil {
		t.Fatal("SQLite allowed an answered question's text to be rewritten")
	}
	if got := questionByID(t, s, e.task.ID, e.instance, question.ID); got.AnswerText != stored.AnswerText {
		t.Fatalf("saved answer changed: %+v", got)
	}
}

func checkOwnerQuestionAnswerRollback(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	question := e.mustCreate(t, s, uuid.NewString(), "rollback question")
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER fail_question_answer_receipt BEFORE INSERT ON delivery_receipts
			WHEN NEW.principal LIKE 'question.answer:%' BEGIN SELECT RAISE(ABORT,'injected answer receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	rollbackKey := uuid.NewString()
	if _, err := e.answer(t, s, question.ID, rollbackKey, "rollback answer"); err == nil {
		t.Fatal("injected answer failure succeeded")
	}
	if got := countRows(t, s, "delivery_messages"); got != 0 {
		t.Fatal("failed answer committed queue row")
	}
	if got := questionByID(t, s, e.task.ID, e.instance, question.ID); got.Status != "open" || got.AnswerText != "" || got.AnswerMessageID != "" {
		t.Fatalf("failed answer committed question: %+v", got)
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER fail_question_answer_receipt"); err != nil {
		t.Fatal(err)
	}
	retried := e.mustAnswer(t, s, question.ID, rollbackKey, "rollback answer")
	if got := questionByID(t, s, e.task.ID, e.instance, question.ID); got.AnswerMessageID != retried.MessageID {
		t.Fatalf("retry did not commit one pair: %+v", got)
	}
}

func checkOwnerQuestionAnswerTextValidation(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	for _, text := range []string{"", "nul\x00", string([]byte{0xff}), strings.Repeat("x", questionMaxText+1)} {
		question := e.mustCreate(t, s, uuid.NewString(), "text validation")
		if _, err := e.answer(t, s, question.ID, uuid.NewString(), text); !observerIs(err, "invalid_request") {
			t.Fatalf("invalid answer text: %v", err)
		}
	}
}

func TestOwnerQuestionAnswerReplayAndAtomicRollback(t *testing.T) {
	t.Run("exact replay and immutable history", checkOwnerQuestionAnswerReplay)
	t.Run("answer and queue roll back together", checkOwnerQuestionAnswerRollback)
	t.Run("invalid answer text leaves no answer", checkOwnerQuestionAnswerTextValidation)
}

func TestOwnerQuestionConcurrentAnswersAndAssignmentRace(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	question := e.mustCreate(t, s, uuid.NewString(), "one answer only")
	second, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, target := range []*Store{s, second} {
		wg.Go(func() {
			_, err := target.AnswerOwnerQuestion(t.Context(), e.task.ID, question.ID, OwnerQuestionAnswer{
				Key: uuid.NewString(), InstanceID: e.instance, Text: "competing answer",
			})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !observerIs(err, "closed") {
			t.Fatalf("concurrent answer: %v", err)
		}
	}
	if successes != 1 || countRows(t, s, "delivery_messages") != 1 {
		t.Fatalf("concurrent answers: successes=%d rows=%d", successes, countRows(t, s, "delivery_messages"))
	}
	// Rebinding is serialized by SQLite with answer creation and permanently
	// revokes the old grant. No answer can be created under the stale assignment.
	other := e.mustCreate(t, s, uuid.NewString(), "revoke race")
	runtime := mustRegister(t, s, e.observerVerifier)
	mustOwner(t, s, e.observer, "bind", observerView(t, s).Version, runtime.RuntimeID, e.project.ID)
	if _, err := e.answer(t, s, other.ID, uuid.NewString(), "stale assignment"); !observerIs(err, "closed") {
		t.Fatalf("answer after assignment change: %v", err)
	}
	if got := questionByID(t, s, e.task.ID, e.instance, other.ID); got.Status != "closed" {
		t.Fatalf("assignment change left question open: %+v", got)
	}
}

func seedMixedQuestionQueue(t *testing.T, s *Store, e questionEnv) (questionIDs, commissionIDs []string) {
	t.Helper()
	if err := s.DeliveryReady(t.Context(), e.binding.ID, e.instance, e.writer); err != nil {
		t.Fatal(err)
	}
	for i := range 7 {
		q := e.mustCreate(t, s, uuid.NewString(), "question-"+string(rune('a'+i)))
		questionIDs = append(questionIDs, q.ID)
		commissionIDs = append(commissionIDs, e.mustAppend(t, s, "commission-"+uuid.NewString()).ID)
		if i%2 == 0 {
			e.mustAnswer(t, s, q.ID, uuid.NewString(), "answer-"+uuid.NewString())
		}
	}
	return questionIDs, commissionIDs
}

func walkOwnerQuestionPages(t *testing.T, s *Store, e questionEnv) []OwnerQuestion {
	t.Helper()
	var pages []OwnerQuestion
	after := int64(0)
	for {
		page, err := s.ListOwnerQuestions(t.Context(), e.task.ID, e.instance, after, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, page.Questions...)
		if !page.HasMore {
			return pages
		}
		if page.NextAfter <= after {
			t.Fatal("question cursor did not advance")
		}
		after = page.NextAfter
	}
}

func TestOwnerQuestionPaginationAndLegacyMessageFiltering(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	questionIDs, commissionIDs := seedMixedQuestionQueue(t, s, e)
	pages := walkOwnerQuestionPages(t, s, e)
	if len(pages) != len(questionIDs) {
		t.Fatalf("question pages: %d", len(pages))
	}
	for i := range pages {
		if pages[i].ID != questionIDs[i] {
			t.Fatalf("question order at %d", i)
		}
	}
	page, err := s.ListOwnerQuestions(t.Context(), e.task.ID, e.instance, 999, 0, []string{questionIDs[6], questionIDs[0]})
	refresh := page.Questions
	if err != nil || len(refresh) != 2 || refresh[0].ID != questionIDs[0] || refresh[1].ID != questionIDs[6] {
		t.Fatalf("question ID refresh: %+v %v", refresh, err)
	}
	if _, err := s.ListOwnerQuestions(t.Context(), e.task.ID, uuid.NewString(), 0, 0, []string{questionIDs[0]}); !observerIs(err, "instance_mismatch") {
		t.Fatalf("foreign database refresh: %v", err)
	}
	messages := walkDeliveryPages(t, s, e.task.ID, 2)
	if len(messages) != len(commissionIDs) {
		t.Fatalf("legacy cursor included answers: %d", len(messages))
	}
	for i := range messages {
		if messages[i].ID != commissionIDs[i] || messages[i].Kind == "answer" {
			t.Fatalf("legacy page row %d: %+v", i, messages[i])
		}
	}
	answer := questionByID(t, s, e.task.ID, e.instance, questionIDs[0])
	mixed, _, err := s.ListDeliveryMessages(t.Context(), e.task.ID, 0, 50, []string{commissionIDs[0], answer.AnswerMessageID, commissionIDs[1]})
	if err != nil || len(mixed) != 2 || mixed[0].ID != commissionIDs[0] || mixed[1].ID != commissionIDs[1] {
		t.Fatalf("legacy mixed IDs included answer: %+v %v", mixed, err)
	}
	assertMixedQuestionPickup(t, s, e, commissionIDs[0], answer)
}

func assertMixedQuestionPickup(t *testing.T, s *Store, e questionEnv, commissionID string, answer OwnerQuestion) {
	t.Helper()
	first, firstKey, err := s.DeliveryNext(t.Context(), e.binding.ID, e.instance, e.writer)
	if err != nil || first.ID != commissionID || first.Kind == "answer" || firstKey == "" {
		t.Fatalf("first pickup: %+v %v", first, err)
	}
	e.mustSpend(t, s, e.writer, first)
	second, secondKey, err := s.DeliveryNext(t.Context(), e.binding.ID, e.instance, e.writer)
	if err != nil || second.ID != answer.AnswerMessageID || second.Kind != "answer" ||
		second.QuestionID != answer.ID || second.QuestionText != answer.Text || second.Text != answer.AnswerText || secondKey == "" {
		t.Fatalf("second pickup: %+v %v", second, err)
	}
}

type questionLifecycleState struct {
	pendingQuestion, spentQuestion, verifiedQuestion, observedQuestion, openQuestion OwnerQuestionCreateResult
	pendingAnswer, spentAnswer, verifiedAnswer, observedAnswer                       OwnerQuestionAnswerResult
	spentAttempt, verifiedAttempt, observedAttempt                                   DeliveryAttemptResult
}

func prepareQuestionLifecycle(t *testing.T, s *Store, e questionEnv) questionLifecycleState {
	t.Helper()
	if err := s.DeliveryReady(t.Context(), e.binding.ID, e.instance, e.writer); err != nil {
		t.Fatal(err)
	}
	state := questionLifecycleState{
		pendingQuestion:  e.mustCreate(t, s, uuid.NewString(), "pending"),
		spentQuestion:    e.mustCreate(t, s, uuid.NewString(), "spent but unverified"),
		verifiedQuestion: e.mustCreate(t, s, uuid.NewString(), "verified"),
		observedQuestion: e.mustCreate(t, s, uuid.NewString(), "observed"),
		openQuestion:     e.mustCreate(t, s, uuid.NewString(), "unanswered"),
	}
	state.pendingAnswer = e.mustAnswer(t, s, state.pendingQuestion.ID, uuid.NewString(), "pending answer")
	state.spentAnswer = e.mustAnswer(t, s, state.spentQuestion.ID, uuid.NewString(), "unspent answer")
	state.verifiedAnswer = e.mustAnswer(t, s, state.verifiedQuestion.ID, uuid.NewString(), "verified answer")
	state.observedAnswer = e.mustAnswer(t, s, state.observedQuestion.ID, uuid.NewString(), "observed answer")
	state.spentAttempt = e.mustSpend(t, s, e.writer, DeliveryMessage{ID: state.spentAnswer.MessageID})
	state.verifiedAttempt = e.mustSpend(t, s, e.writer, DeliveryMessage{ID: state.verifiedAnswer.MessageID})
	e.mustVerify(t, s, e.writer, state.verifiedAttempt)
	state.observedAttempt = e.mustSpend(t, s, e.writer, DeliveryMessage{ID: state.observedAnswer.MessageID})
	e.mustVerify(t, s, e.writer, state.observedAttempt)
	e.mustReceipt(t, s, e.receiptKey(t, s, state.observedAnswer.MessageID), e.answerReceipt(state.observedAttempt, state.observedQuestion.ID))
	return state
}

func applyQuestionLifecycle(t *testing.T, s *Store, e questionEnv, mode string, reopen bool) {
	t.Helper()
	switch mode {
	case "close":
		state := domain.Done
		if reopen {
			state = domain.Backlog
		}
		transitionTask(t, s, e.task.ID, e.project.ID, state)
	case "archive":
		archiveProject(t, s, e.project.ID, !reopen)
	case "revoke":
		if !reopen {
			e.revoke(t, s)
		}
	}
}

func assertQuestionLifecycleHistory(t *testing.T, s *Store, e questionEnv, mode string, state questionLifecycleState) {
	t.Helper()
	for _, id := range []string{state.pendingQuestion.ID, state.spentQuestion.ID, state.verifiedQuestion.ID, state.observedQuestion.ID} {
		if got := questionByID(t, s, e.task.ID, e.instance, id); got.Status != "answered" || got.AnswerText == "" {
			t.Fatalf("saved answer lost: %+v", got)
		}
	}
	if got := questionByID(t, s, e.task.ID, e.instance, state.openQuestion.ID); got.Status != "closed" {
		t.Fatalf("unanswered question not closed: %+v", got)
	}
	wantReason := map[string]string{"close": "task_closed", "archive": "project_archived", "revoke": "delivery_revoked"}[mode]
	for _, id := range []string{state.pendingQuestion.ID, state.spentQuestion.ID} {
		if got := questionByID(t, s, e.task.ID, e.instance, id); got.AnswerStatus != "unknown" || got.AnswerBlockReason != wantReason {
			t.Fatalf("undelivered status after %s: %+v", mode, got)
		}
	}
	if got := questionByID(t, s, e.task.ID, e.instance, state.observedQuestion.ID); got.AnswerStatus != "observed" || got.AnswerBlockReason != "" {
		t.Fatalf("observed status: %+v", got)
	}
	if _, err := e.answer(t, s, state.openQuestion.ID, uuid.NewString(), "too late"); !observerIs(err, "closed") {
		t.Fatalf("answer after %s: %v", mode, err)
	}
}

func assertQuestionLifecycleDeliveryFence(t *testing.T, s *Store, e questionEnv, mode string, state questionLifecycleState) {
	t.Helper()
	if _, err := s.DeliverySpend(t.Context(), e.binding.ID, e.instance, e.writer, state.pendingAnswer.MessageID); err == nil {
		t.Fatal("pending answer spent after lifecycle closure")
	}
	if _, err := s.DeliveryVerify(t.Context(), e.binding.ID, e.instance, e.writer, state.spentAttempt.AttemptID); err == nil {
		t.Fatal("unspent answer verified after lifecycle closure")
	}
	receipt := e.answerReceipt(state.verifiedAttempt, state.verifiedQuestion.ID)
	key := e.receiptKey(t, s, state.verifiedAnswer.MessageID)
	observedKey := e.receiptKey(t, s, state.observedAnswer.MessageID)
	observedReceipt := e.answerReceipt(state.observedAttempt, state.observedQuestion.ID)
	var blocked int
	if err := s.db.QueryRowContext(t.Context(), "SELECT blocked FROM delivery_messages WHERE id=?", state.observedAnswer.MessageID).Scan(&blocked); err != nil || blocked != 0 {
		t.Fatalf("observed row was blocked: %d %v", blocked, err)
	}

	if mode == "revoke" {
		if _, err := s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writer, observedKey, observedReceipt); !observerIs(err, "revoked") {
			t.Fatalf("matching answer receipt replay after revoke: %v", err)
		}
		if _, err := s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writer, key, receipt); !observerIs(err, "revoked") {
			t.Fatalf("first receipt after revoke: %v", err)
		}
		return
	}
	e.mustReceipt(t, s, observedKey, observedReceipt)
	// A call already verified on the unchanged grant may publish after close/archive.
	published, err := s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writer, key, receipt)
	if err != nil || published.Status != "observed" {
		t.Fatalf("verified receipt after %s: %+v %v", mode, published, err)
	}
	if got := questionByID(t, s, e.task.ID, e.instance, state.verifiedQuestion.ID); got.AnswerStatus != "observed" || got.AnswerBlockReason != "" {
		t.Fatalf("late allowed receipt projected a block: %+v", got)
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT blocked FROM delivery_messages WHERE id=?", state.verifiedAnswer.MessageID).Scan(&blocked); err != nil || blocked != 0 {
		t.Fatalf("late allowed receipt retained block: %d %v", blocked, err)
	}
}

func assertQuestionReopenKeepsQueueBlocked(t *testing.T, s *Store, e questionEnv, mode string, state questionLifecycleState) {
	t.Helper()
	if mode == "revoke" {
		return
	}
	if message, key, err := s.DeliveryNext(t.Context(), e.binding.ID, e.instance, e.writer); err != nil || message != (DeliveryMessage{}) || key != "" {
		t.Fatalf("reopen resumed pickup: %+v %q %v", message, key, err)
	}
	for _, id := range []string{state.pendingAnswer.MessageID, state.spentAnswer.MessageID} {
		if _, err := s.DeliverySpend(t.Context(), e.binding.ID, e.instance, e.writer, id); !observerIs(err, "closed") {
			t.Fatalf("reopen resumed spend of %s: %v", id, err)
		}
	}
}

func TestOwnerQuestionCloseArchiveRevokeAndReopen(t *testing.T) {
	for _, mode := range []string{"close", "archive", "revoke"} {
		t.Run(mode, func(t *testing.T) {
			s := openTest(t)
			e := mustQuestionEnv(t, s)
			state := prepareQuestionLifecycle(t, s, e)
			applyQuestionLifecycle(t, s, e, mode, false)
			assertQuestionLifecycleHistory(t, s, e, mode, state)
			assertQuestionLifecycleDeliveryFence(t, s, e, mode, state)
			applyQuestionLifecycle(t, s, e, mode, true)
			assertQuestionReopenKeepsQueueBlocked(t, s, e, mode, state)
		})
	}
}

func TestOwnerQuestionRevokeThenNewGrantDoesNotReopenHistory(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	question := e.mustCreate(t, s, uuid.NewString(), "old grant")
	answerKey := uuid.NewString()
	answer := e.mustAnswer(t, s, question.ID, answerKey, "saved before revoke")
	e.revoke(t, s)
	// Answer replay is an ACK recovery operation and remains available to the
	// owner after revoke; the revoked delivery credential cannot replay create.
	replayed, err := e.answer(t, s, question.ID, answerKey, "saved before revoke")
	if err != nil || replayed != answer {
		t.Fatalf("answer replay after revoke: %+v %v", replayed, err)
	}
	if _, err := e.create(t, s, uuid.NewString(), e.task.ID, e.writer, "old credential"); !observerIs(err, "revoked") {
		t.Fatalf("old grant create: %v", err)
	}
	fresh := mustQuestionEnv(t, s)
	if fresh.binding.ID == e.binding.ID {
		t.Fatal("grant was changed in place")
	}
	newQuestion := fresh.mustCreate(t, s, uuid.NewString(), "new grant")
	if got := questionByID(t, s, e.task.ID, e.instance, question.ID); got.Status != "answered" || got.AnswerMessageID != answer.MessageID {
		t.Fatalf("old history changed: %+v", got)
	}
	if got := questionByID(t, s, fresh.task.ID, fresh.instance, newQuestion.ID); got.Status != "open" || got.BindingID != fresh.binding.ID {
		t.Fatalf("new grant question: %+v", got)
	}
}

func TestOwnerQuestionEpicFixtureRejectsFreshWritesButRecoversReceipts(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	open := e.mustCreate(t, s, uuid.NewString(), "open before Epic fixture")
	createKey := uuid.NewString()
	answered := e.mustCreate(t, s, createKey, "answered before Epic fixture")
	answerKey := uuid.NewString()
	answer := e.mustAnswer(t, s, answered.ID, answerKey, "stored answer")
	setTaskKindFixture(t, s, e.task.ID, domain.KindEpic)
	before := databaseDump(t, s.db)
	beforePage, err := s.ListOwnerQuestions(t.Context(), e.task.ID, e.instance, 0, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.create(t, s, uuid.NewString(), e.task.ID, e.writer, "stale writer question"); !observerIs(err, "invalid_request") {
		t.Fatalf("Epic question create: %v", err)
	}
	if _, err := e.answer(t, s, open.ID, uuid.NewString(), "stale owner answer"); !observerIs(err, "invalid_request") {
		t.Fatalf("Epic question answer: %v", err)
	}
	if replayed, err := e.create(t, s, createKey, e.task.ID, e.writer, "answered before Epic fixture"); err != nil || replayed != answered {
		t.Fatalf("Epic question create recovery: %+v %v", replayed, err)
	}
	if replayed, err := e.answer(t, s, answered.ID, answerKey, "stored answer"); err != nil || replayed != answer {
		t.Fatalf("Epic answer recovery: %+v %v", replayed, err)
	}
	afterPage, err := s.ListOwnerQuestions(t.Context(), e.task.ID, e.instance, 0, 5, nil)
	if err != nil || !reflect.DeepEqual(afterPage, beforePage) {
		t.Fatalf("Epic question history changed: before=%+v after=%+v err=%v", beforePage, afterPage, err)
	}
	if after := databaseDump(t, s.db); after != before {
		t.Fatal("rejected Epic question operations changed questions, messages, events or receipts")
	}
}

func TestOwnerQuestionBugAnswerFlowAndRecoveryAfterClose(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	setTaskKindFixture(t, s, e.task.ID, domain.KindBug)
	question := e.mustCreate(t, s, uuid.NewString(), "Bug question")
	answerKey := uuid.NewString()
	answer := e.mustAnswer(t, s, question.ID, answerKey, "Bug answer")
	attempt := e.mustSpend(t, s, e.writer, DeliveryMessage{ID: answer.MessageID})
	e.mustVerify(t, s, e.writer, attempt)
	receiptKey := e.receiptKey(t, s, answer.MessageID)
	receipt := e.answerReceipt(attempt, question.ID)
	e.mustReceipt(t, s, receiptKey, receipt)
	transitionTask(t, s, e.task.ID, e.project.ID, domain.Done)
	if replayed, err := e.answer(t, s, question.ID, answerKey, "Bug answer"); err != nil || replayed != answer {
		t.Fatalf("Bug answer recovery after close: %+v %v", replayed, err)
	}
	e.mustReceipt(t, s, receiptKey, receipt)
	if got := questionByID(t, s, e.task.ID, e.instance, question.ID); got.AnswerStatus != "observed" || got.AnswerText != "Bug answer" {
		t.Fatalf("Bug question history after close: %+v", got)
	}
}

// legacyDeliveryMutationHash is the v3 wire-independent hash input. The v4
// option bit must not change false/absent enable/revoke receipt hashes.
func legacyDeliveryMutationHash(m DeliveryOwnerMutation) string {
	return observerHash(struct {
		ObserverID  string
		Key         string
		Action      string
		ExpectedVer int64
		BindingID   string
		JournalID   string
		Verifier    string
		RuntimeID   string
		ProjectID   string
		PiSessionID string
		InstanceID  string
	}{m.ObserverID, m.Key, m.Action, m.ExpectedVer, m.BindingID, m.JournalID, m.Verifier, m.RuntimeID, m.ProjectID, m.PiSessionID, m.InstanceID})
}

type populatedV3State struct {
	snapshot         Snapshot
	events           []Event
	domainReceipts   []byte
	observerReceipts []byte
	enable           DeliveryOwnerMutation
	enableResult     DeliveryBinding
	revoke           DeliveryOwnerMutation
	revokeResult     DeliveryBinding
	messages         []DeliveryMessage
	attempts         int
}

func populatedV3(t *testing.T, path string) populatedV3State {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{v1Schema, observerSchema, deliverySchema} {
		if _, err := db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata(id,instance_id) VALUES(1,?)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db, observerClock: time.Now, observerSeen: make(map[string]time.Time), deliveryReady: make(map[string]time.Time)}
	p, task := seed(t, old)
	observer, verifier, owner := mustObserver(t, old)
	runtime := mustRegister(t, old, verifier)
	owner = mustOwner(t, old, observer, "bind", owner.Version, runtime.RuntimeID, p.ID)
	enable := DeliveryOwnerMutation{ObserverID: observer, Key: uuid.NewString(), Action: "enable", ExpectedVer: owner.Version,
		BindingID: uuid.NewString(), JournalID: uuid.NewString(), Verifier: observerHash(uuid.NewString()), RuntimeID: runtime.RuntimeID,
		ProjectID: p.ID, PiSessionID: runtime.PiSessionID, InstanceID: mustSnapshotInstance(t, old)}
	binding, err := old.MutateDeliveryBinding(t.Context(), enable)
	if err != nil {
		t.Fatal(err)
	}
	// Force the genuine v3 receipt bytes even when this test is compiled with v4.
	if _, err := db.ExecContext(t.Context(), "UPDATE delivery_receipts SET request_hash=? WHERE principal=? AND key=?", legacyDeliveryMutationHash(enable), "observer:"+observer, enable.Key); err != nil {
		t.Fatal(err)
	}
	writer := uuid.NewString()
	if err := old.DeliveryAttach(t.Context(), binding.ID, "", writer, "", binding.JournalID, runtime.RuntimeID, runtime.PiSessionID, ""); err != nil {
		t.Fatal(err)
	}
	if err := old.DeliveryReady(t.Context(), binding.ID, "", writer); err != nil {
		t.Fatal(err)
	}
	observed := mustAppendV3(t, old, task.ID, binding.ID, mustSnapshotInstance(t, old), "observed v3")
	attempt, err := old.DeliverySpend(t.Context(), binding.ID, "", writer, observed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.DeliveryVerify(t.Context(), binding.ID, "", writer, attempt.AttemptID); err != nil {
		t.Fatal(err)
	}
	receiptKey := ""
	if err := db.QueryRowContext(t.Context(), "SELECT receipt_key FROM delivery_messages WHERE id=?", observed.ID).Scan(&receiptKey); err != nil {
		t.Fatal(err)
	}
	if _, err := old.DeliveryReceipt(t.Context(), binding.ID, "", "", receiptKey, deliveryReceiptBodyT{MessageID: observed.ID, AttemptID: attempt.AttemptID,
		Type: "before_agent_start", MessageType: "custom", PiSessionID: runtime.PiSessionID,
		Details: map[string]any{"message_id": observed.ID, "attempt_id": attempt.AttemptID}}); err != nil {
		t.Fatal(err)
	}
	mustAppendV3(t, old, task.ID, binding.ID, mustSnapshotInstance(t, old), "pending v3")
	spent := mustAppendV3(t, old, task.ID, binding.ID, mustSnapshotInstance(t, old), "spent v3")
	if _, err := old.DeliverySpend(t.Context(), binding.ID, "", writer, spent.ID); err != nil {
		t.Fatal(err)
	}
	revoke := DeliveryOwnerMutation{ObserverID: observer, Key: uuid.NewString(), Action: "revoke", BindingID: binding.ID}
	revoked, err := old.MutateDeliveryBinding(t.Context(), revoke)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "UPDATE delivery_receipts SET request_hash=? WHERE principal=? AND key=?", legacyDeliveryMutationHash(revoke), "observer:"+observer, revoke.Key); err != nil {
		t.Fatal(err)
	}
	snapshot, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := old.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := old.ListDeliveryMessages(t.Context(), task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	return populatedV3State{snapshot: snapshot, events: events, domainReceipts: receiptDump(t, old), observerReceipts: observerReceiptDump(t, old),
		enable: enable, enableResult: binding, revoke: revoke, revokeResult: revoked, messages: messages, attempts: 2}
}

func mustSnapshotInstance(t *testing.T, s *Store) string {
	t.Helper()
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return snap.InstanceID
}

func mustAppendV3(t *testing.T, s *Store, taskID, bindingID, instanceID, text string) DeliveryMessage {
	t.Helper()
	m, err := s.AppendDeliveryMessage(t.Context(), taskID, DeliveryAppend{Key: uuid.NewString(), Text: text, BindingID: bindingID, InstanceID: instanceID})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assertV3StatePreserved(t *testing.T, s *Store, before populatedV3State) {
	t.Helper()
	after, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := s.ListDeliveryMessages(t.Context(), before.messages[0].TaskID, 0, 50, nil)
	if err != nil || !reflect.DeepEqual(after, expectedMigratedSnapshot(before.snapshot)) || !reflect.DeepEqual(events, before.events) ||
		string(receiptDump(t, s)) != string(before.domainReceipts) || string(observerReceiptDump(t, s)) != string(before.observerReceipts) ||
		!reflect.DeepEqual(messages, before.messages) || countRows(t, s, "delivery_attempts") != before.attempts || countRows(t, s, "owner_questions") != 0 {
		t.Fatal("v3 migration changed domain, delivery, attempts or receipts")
	}
	enableReplay, err := s.MutateDeliveryBinding(t.Context(), before.enable)
	if err != nil || enableReplay != before.enableResult {
		t.Fatalf("v3 enable lost-ACK replay: %+v %v", enableReplay, err)
	}
	revokeReplay, err := s.MutateDeliveryBinding(t.Context(), before.revoke)
	if err != nil || revokeReplay != before.revokeResult {
		t.Fatalf("v3 revoke lost-ACK replay: %+v %v", revokeReplay, err)
	}
	var version int
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestOwnerQuestionsV3UpgradePreservesDeliveryAndLegacyReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.sqlite")
	before := populatedV3(t, path)
	for range 2 {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		assertV3StatePreserved(t, s, before)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOwnerQuestionsMigrationFailureRollsBackToV3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3-rollback.sqlite")
	before := populatedV3(t, path)
	original := questionsSchema
	questionsSchema = original + "\nTHIS IS AN INJECTED UPGRADE ERROR;"
	s, err := Open(t.Context(), path)
	questionsSchema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("bad questions migration succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db, observerClock: time.Now, observerSeen: make(map[string]time.Time), deliveryReady: make(map[string]time.Time)}
	var version, tables int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name LIKE 'owner_questions%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	after, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	messages, _, err := old.ListDeliveryMessages(t.Context(), before.messages[0].TaskID, 0, 50, nil)
	if err != nil || version != 3 || tables != 0 || !reflect.DeepEqual(before.snapshot, after) ||
		!reflect.DeepEqual(before.messages, messages) || countRows(t, old, "delivery_attempts") != before.attempts {
		t.Fatal("partial v4 migration committed")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerQuestionCreateRollbackAndAnswerReceiptBinding(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	if _, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER fail_question_create_receipt BEFORE INSERT ON delivery_receipts
		WHEN NEW.principal LIKE 'question.create:%' BEGIN SELECT RAISE(ABORT,'injected question receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	if _, err := e.create(t, s, key, e.task.ID, e.writer, "rollback create"); err == nil {
		t.Fatal("injected create failure succeeded")
	}
	if got := countRows(t, s, "owner_questions"); got != 0 {
		t.Fatal("failed create committed question")
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER fail_question_create_receipt"); err != nil {
		t.Fatal(err)
	}
	question := e.mustCreate(t, s, key, "rollback create")
	answerKey := uuid.NewString()
	answer := e.mustAnswer(t, s, question.ID, answerKey, "answer")
	attempt := e.mustSpend(t, s, e.writer, DeliveryMessage{ID: answer.MessageID})
	e.mustVerify(t, s, e.writer, attempt)
	valid := e.answerReceipt(attempt, question.ID)
	for name, mutate := range map[string]func(*deliveryReceiptBodyT){
		"custom type":  func(r *deliveryReceiptBodyT) { r.Details["custom_type"] = "artel-task-message" },
		"question":     func(r *deliveryReceiptBodyT) { r.Details["question_id"] = uuid.NewString() },
		"missing kind": func(r *deliveryReceiptBodyT) { delete(r.Details, "custom_type") },
	} {
		t.Run(name, func(t *testing.T) {
			body := valid
			body.Details = map[string]any{"message_id": valid.MessageID, "attempt_id": valid.AttemptID,
				"custom_type": "artel-owner-answer", "question_id": question.ID}
			mutate(&body)
			if _, err := s.DeliveryReceipt(t.Context(), e.binding.ID, e.instance, e.writer, e.receiptKey(t, s, answer.MessageID), body); !observerIs(err, "invalid_request") {
				t.Fatalf("wrong answer receipt: %v", err)
			}
		})
	}
	e.mustReceipt(t, s, e.receiptKey(t, s, answer.MessageID), valid)
	// The answer POST replay remains a storage ACK even after observation.
	replayed, err := e.answer(t, s, question.ID, answerKey, "answer")
	if err != nil || replayed != answer || replayed.Status != "stored" {
		t.Fatalf("observed answer replay: %+v %v", replayed, err)
	}
}

func TestOwnerQuestionConcurrentAnswerAndAssignmentChange(t *testing.T) {
	s := openTest(t)
	e := mustQuestionEnv(t, s)
	question := e.mustCreate(t, s, uuid.NewString(), "race")
	second, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	runtime := mustRegister(t, s, e.observerVerifier)
	version := observerView(t, s).Version
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		_, err := e.answer(t, s, question.ID, uuid.NewString(), "racing answer")
		results <- err
	})
	wg.Go(func() {
		<-start
		_, err := second.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: e.observer, Key: uuid.NewString(),
			Action: "bind", ExpectedVersion: version, RuntimeID: runtime.RuntimeID, ProjectID: e.project.ID})
		results <- err
	})
	close(start)
	wg.Wait()
	close(results)
	var rebindErr error
	for err := range results {
		// Either linearization is valid: a committed answer retains blocked
		// history, while a winning rebind leaves the question closed.
		if err != nil && !observerIs(err, "closed") && !observerIs(err, "revoked") {
			t.Fatalf("race operation: %v", err)
		}
	}
	// Rebind is expected to commit. Repeat its read-side effect check rather
	// than relying on goroutine result ordering.
	if _, _, err := s.DeliveryNext(t.Context(), e.binding.ID, e.instance, e.writer); !observerIs(err, "revoked") {
		rebindErr = err
	}
	if rebindErr != nil {
		t.Fatalf("assignment did not change: %v", rebindErr)
	}
	stored := questionByID(t, s, e.task.ID, e.instance, question.ID)
	rows := countRows(t, s, "delivery_messages")
	if stored.Status == "answered" {
		if rows != 1 || stored.AnswerBlockReason != "delivery_revoked" || stored.AnswerStatus != "unknown" {
			t.Fatalf("answer won without atomic blocked queue: %+v rows=%d", stored, rows)
		}
	} else if stored.Status != "closed" || rows != 0 {
		t.Fatalf("revoke won but answer was recorded: %+v rows=%d", stored, rows)
	}
}
