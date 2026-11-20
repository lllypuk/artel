package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/domain"
)

// Task 2 storage-layer regression suite: v1/v2 -> v3 migration contract plus
// the success/error matrix for bindings, messages, rights and receipts.
// Every scenario uses private temporary databases and fictional identities.

// deliveryReceiptBodyT is the exact bridge receipt shape the store accepts.
type deliveryReceiptBodyT = struct {
	MessageID, AttemptID, Type, MessageType, PiSessionID string
	Details                                              map[string]any
}

type deliveryEnv struct {
	project          domain.Project
	task             domain.Task
	observer         string
	observerVerifier string
	runtime          ObserverRegistration
	binding          DeliveryBinding
	deliveryVerifier string
	instance         string
}

// seedAt seeds a project/task pair at a distinct root so multiple delivery
// environments can coexist in one database.
func seedAt(t *testing.T, s *Store, root string) (domain.Project, domain.Task) {
	t.Helper()
	p := domain.Project{ID: uuid.NewString(), RootPath: root, Name: "test", Version: 1, QueueVersion: 1}
	task := domain.Task{ID: uuid.NewString(), ProjectID: p.ID, Title: "test", State: domain.Backlog, Version: 1}
	_, err := s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
		snap.Projects = append(snap.Projects, p)
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[p.ID] = []string{}
		return Result{Status: 201, Body: []byte(`{}`)}, &Event{Action: "seed", ProjectID: p.ID, TaskID: task.ID, Timestamp: "2026-11-17T00:00:00Z", Changes: []Change{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, task
}

// mustDeliveryEnv provisions an observer, binds it to the seeded project and
// enables one pending delivery slot. The binding stays pending until attach.
func mustDeliveryEnv(t *testing.T, s *Store) deliveryEnv {
	t.Helper()
	p, task := seedAt(t, s, "/delivery/"+uuid.NewString())
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, r.RuntimeID, p.ID)
	snap, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	deliveryVerifier := observerHash(uuid.NewString())
	b, err := s.MutateDeliveryBinding(t.Context(), DeliveryOwnerMutation{
		ObserverID: id, Key: uuid.NewString(), Action: "enable",
		ExpectedVer: owner.Version, BindingID: uuid.NewString(), JournalID: uuid.NewString(),
		Verifier: deliveryVerifier, RuntimeID: r.RuntimeID,
		ProjectID: p.ID, PiSessionID: r.PiSessionID, InstanceID: snap.InstanceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "pending" || b.Generation != owner.Generation {
		t.Fatalf("fresh binding: %+v", b)
	}
	return deliveryEnv{project: p, task: task, observer: id, observerVerifier: verifier,
		runtime: r, binding: b, deliveryVerifier: deliveryVerifier, instance: snap.InstanceID}
}

// mustAttachReady activates the pending binding with one writer identity and
// confirms bounded readiness, exactly as a writer does after a proven CAS.
// freshEnable builds a new enable mutation against the env's current observer
// state for slot/admission tests.
func (e deliveryEnv) freshEnable(t *testing.T, s *Store) DeliveryOwnerMutation {
	t.Helper()
	return DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "enable",
		ExpectedVer: observerView(t, s).Version, BindingID: uuid.NewString(), JournalID: uuid.NewString(),
		Verifier: observerHash(uuid.NewString()), RuntimeID: e.runtime.RuntimeID, ProjectID: e.project.ID,
		PiSessionID: e.runtime.PiSessionID, InstanceID: e.instance,
	}
}

func (e deliveryEnv) mustAttachReady(t *testing.T, s *Store, writerID, expectedPrevious string) {
	t.Helper()
	if err := s.DeliveryAttach(t.Context(), e.binding.ID, writerID, expectedPrevious, e.binding.JournalID, e.runtime.RuntimeID, e.runtime.PiSessionID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeliveryReady(t.Context(), e.binding.ID, writerID); err != nil {
		t.Fatal(err)
	}
}

func (e deliveryEnv) append(t *testing.T, s *Store, key, text string) (DeliveryMessage, error) {
	t.Helper()
	return s.AppendDeliveryMessage(t.Context(), e.task.ID, DeliveryAppend{Key: key, Text: text, BindingID: e.binding.ID, InstanceID: e.instance})
}

func (e deliveryEnv) mustAppend(t *testing.T, s *Store, text string) DeliveryMessage {
	t.Helper()
	m, err := e.append(t, s, uuid.NewString(), text)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "stored" || m.TaskID != e.task.ID || m.Blocked {
		t.Fatalf("fresh message: %+v", m)
	}
	return m
}

func (e deliveryEnv) mustSpend(t *testing.T, s *Store, writerID string, m DeliveryMessage) DeliveryAttemptResult {
	t.Helper()
	r, err := s.DeliverySpend(t.Context(), e.binding.ID, writerID, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e deliveryEnv) mustVerify(t *testing.T, s *Store, writerID string, a DeliveryAttemptResult) DeliveryAttemptResult {
	t.Helper()
	r, err := s.DeliveryVerify(t.Context(), e.binding.ID, writerID, a.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e deliveryEnv) revoke(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.MutateDeliveryBinding(t.Context(), DeliveryOwnerMutation{
		ObserverID: e.observer, Key: uuid.NewString(), Action: "revoke",
		ExpectedVer: observerView(t, s).Version, BindingID: e.binding.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (e deliveryEnv) receiptKey(t *testing.T, s *Store, messageID string) string {
	t.Helper()
	var key string
	if err := s.db.QueryRowContext(t.Context(), "SELECT receipt_key FROM delivery_messages WHERE id=?", messageID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	return key
}

func (e deliveryEnv) receiptFor(a DeliveryAttemptResult) deliveryReceiptBodyT {
	messageID := a.MessageID
	return deliveryReceiptBodyT{
		MessageID: messageID, AttemptID: a.AttemptID, Type: "before_agent_start", MessageType: "custom",
		PiSessionID: e.runtime.PiSessionID, Details: map[string]any{"message_id": messageID, "attempt_id": a.AttemptID},
	}
}

func (e deliveryEnv) mustReceipt(t *testing.T, s *Store, key string, body deliveryReceiptBodyT) DeliveryMessage {
	t.Helper()
	m, err := s.DeliveryReceipt(t.Context(), e.binding.ID, key, body)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "observed" {
		t.Fatalf("receipt did not publish: %+v", m)
	}
	return m
}

// transitionTask moves a task to a new state through the store mutation path,
// which is the same path that blocks commissions in persist().
func transitionTask(t *testing.T, s *Store, taskID, projectID string, state domain.State) {
	t.Helper()
	_, err := s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
		for i := range snap.Tasks {
			if snap.Tasks[i].ID == taskID {
				snap.Tasks[i].State = state
			}
		}
		return Result{Status: 200, Body: []byte(`{}`)},
			&Event{Action: "task.transition", ProjectID: projectID, TaskID: taskID, Timestamp: "2026-11-19T00:00:00Z", Changes: []Change{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// archiveProject flips the archived flag through the same mutation path.
func archiveProject(t *testing.T, s *Store, projectID string, archived bool) {
	t.Helper()
	_, err := s.Apply(t.Context(), Mutation{Key: uuid.NewString()}, func(snap *Snapshot) (Result, *Event, error) {
		for i := range snap.Projects {
			if snap.Projects[i].ID == projectID {
				snap.Projects[i].Archived = archived
			}
		}
		return Result{Status: 200, Body: []byte(`{}`)},
			&Event{Action: "project.edit", ProjectID: projectID, Timestamp: "2026-11-19T00:00:00Z", Changes: []Change{}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func observerReceiptDump(t *testing.T, s *Store) []byte {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), "SELECT principal,key,request_hash,result FROM observer_receipts ORDER BY principal,key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var all []any
	for rows.Next() {
		var principal, key, hash, result string
		if err := rows.Scan(&principal, &key, &hash, &result); err != nil {
			t.Fatal(err)
		}
		all = append(all, []any{principal, key, hash, result})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func deliveryReceiptCount(t *testing.T, s *Store, bindingID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM delivery_receipts WHERE principal=?", "binding:"+bindingID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func attemptState(t *testing.T, s *Store, attemptID string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRowContext(t.Context(), "SELECT state FROM delivery_attempts WHERE attempt_id=?", attemptID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func observerVersion(t *testing.T, s *Store, id string) int64 {
	t.Helper()
	registry, err := s.Observers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range registry.Observers {
		if o.ObserverID == id {
			return o.Version
		}
	}
	t.Fatal("observer missing")
	return 0
}

func maxDeliverySeq(t *testing.T, s *Store, taskID string) int64 {
	t.Helper()
	var seq int64
	if err := s.db.QueryRowContext(t.Context(), "SELECT coalesce(max(seq),0) FROM delivery_messages WHERE task_id=?", taskID).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	return seq
}

// populatedV2 builds a genuine schema-v2 database (domain v1 plus observer
// schema) with fictional domain, observer and receipt data, all written
// through store APIs against the raw connection.
func populatedV2(t *testing.T, path string) (Snapshot, []Event, []byte, []byte, []ObserverProjection) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), v1Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), observerSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO metadata(id,instance_id) VALUES(1,?)", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db, observerClock: time.Now, observerSeen: make(map[string]time.Time)}
	p, _ := seed(t, old)
	id, verifier, owner := mustObserver(t, old)
	r := mustRegister(t, old, verifier)
	owner = mustOwner(t, old, id, "bind", owner.Version, r.RuntimeID, p.ID)
	mustReport(t, old, verifier, reportFor(r, owner.Generation, 1))
	snap, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	events, err := old.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := old.Observers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return snap, events, receiptDump(t, old), observerReceiptDump(t, old), registry.Observers
}

// clearFreshness blanks process-local runtime freshness, which legitimately
// resets across reopen and must not be mistaken for migration data loss.
func clearFreshness(observers []ObserverProjection) {
	for i := range observers {
		for j := range observers[i].Runtimes {
			observers[i].Runtimes[j].Freshness = ""
		}
	}
}

func TestDeliveryV2UpgradePreservesDomainObserverAndReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite")
	snap, events, receipts, observerReceipts, observers := populatedV2(t, path)
	clearFreshness(observers)
	for range 2 {
		s, err := Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		after, err := s.Snapshot(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		history, err := s.Events(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		registry, err := s.Observers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		clearFreshness(registry.Observers)
		if !reflect.DeepEqual(snap, after) || !reflect.DeepEqual(events, history) ||
			string(receipts) != string(receiptDump(t, s)) || string(observerReceipts) != string(observerReceiptDump(t, s)) ||
			!reflect.DeepEqual(observers, registry.Observers) {
			t.Fatal("v2 domain/observer/receipt data changed by upgrade")
		}
		var version int
		if err := s.db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != 3 {
			t.Fatal(version)
		}
		for table, want := range map[string]int{"delivery_bindings": 0, "delivery_messages": 0, "delivery_attempts": 0} {
			if got := countRows(t, s, table); got != want {
				t.Fatalf("%s=%d after upgrade", table, got)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeliveryMigrationFailureRollsBackToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.sqlite")
	snap, _, receipts, _, _ := populatedV2(t, path)
	original := deliverySchema
	deliverySchema = original + "\nTHIS IS AN INJECTED UPGRADE ERROR;"
	s, err := Open(t.Context(), path)
	deliverySchema = original
	if err == nil {
		_ = s.Close()
		t.Fatal("bad delivery upgrade succeeded")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	old := &Store{db: db}
	var version, tables int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE name LIKE 'delivery%'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	after, err := old.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 || tables != 0 || !reflect.DeepEqual(snap, after) || string(receipts) != string(receiptDump(t, old)) {
		t.Fatal("partial delivery upgrade committed")
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

// A database already at v3 that claims an older version is the rollback
// boundary for an old binary: it must be refused, never "upgraded".
func TestDeliveryV3RefusesDowngradedVersionClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v3.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := seed(t, s)
	before, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version=2"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err == nil {
		_ = s.Close()
		t.Fatal("v3 database with claimed v2 version accepted")
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatal("failed downgrade check mutated the database")
	}
	if _, err := db.ExecContext(t.Context(), "PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	after, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || len(after.Projects) != 1 || after.Projects[0].ID != p.ID {
		t.Fatal("recovery changed v3 data")
	}
}

// driveCommission walks one fresh message through next/spend/verify/receipt.
func driveCommission(t *testing.T, s *Store, env deliveryEnv, writerID string) (DeliveryMessage, DeliveryAttemptResult) {
	t.Helper()
	m := env.mustAppend(t, s, "commission")
	next, receiptKey, err := s.DeliveryNext(t.Context(), env.binding.ID, writerID)
	if err != nil || next.ID != m.ID || receiptKey == "" {
		t.Fatalf("next: %+v %q %v", next, receiptKey, err)
	}
	attempt := env.mustSpend(t, s, writerID, m)
	if !attempt.Right || attempt.State != "spent" {
		t.Fatalf("spend: %+v", attempt)
	}
	verified := env.mustVerify(t, s, writerID, attempt)
	if !verified.Verified || verified.Right || verified.State != "verified" {
		t.Fatalf("verify: %+v", verified)
	}
	published := env.mustReceipt(t, s, env.receiptKey(t, s, attempt.MessageID), env.receiptFor(attempt))
	if published.ID != m.ID {
		t.Fatalf("receipt touched another message: %+v", published)
	}
	return m, attempt
}

func TestDeliveryPendingRefusesAppendWithoutRecord(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	// Pending refuses the owner POST and even metadata reads of the queue.
	if _, err := env.append(t, s, uuid.NewString(), "while pending"); !observerIs(err, "not_ready") {
		t.Fatalf("pending append: %v", err)
	}
	if _, _, err := s.DeliveryNext(t.Context(), env.binding.ID, uuid.NewString()); !observerIs(err, "not_ready") {
		t.Fatalf("pending next: %v", err)
	}
	if countRows(t, s, "delivery_messages") != 0 || deliveryReceiptCount(t, s, "binding:"+env.binding.ID) != 0 {
		t.Fatal("pending append left a record")
	}
	// Activation happens on attach (Task 1 semantics), not on enable.
	env.mustAttachReady(t, s, uuid.NewString(), "")
	b, err := s.DeliveryBindingStatus(t.Context(), env.observer, env.binding.ID)
	if err != nil || b.Status != "active" || b.WriterID != "" {
		t.Fatalf("activation: %+v %v", b, err)
	}
}

func TestDeliveryFullFlowStaysOutOfDomainHistory(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	snapBefore, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	eventsBefore, err := s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	receiptsBefore := countRows(t, s, "mutation_receipts")
	driveCommission(t, s, env, writer)
	env.mustAppend(t, s, "second commission")
	// Commissions never leak into the domain journal, snapshot or owner receipts.
	snapAfter, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	eventsAfter, err := s.Events(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapBefore, snapAfter) || !reflect.DeepEqual(eventsBefore, eventsAfter) ||
		countRows(t, s, "mutation_receipts") != receiptsBefore || countRows(t, s, "events") != len(eventsBefore) {
		t.Fatal("delivery flow touched domain state")
	}
	projection, err := s.DeliveryTaskProjection(t.Context(), env.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if projection["binding"].(DeliveryBinding).ID != env.binding.ID || projection["ready"] != true {
		t.Fatalf("projection: %+v", projection)
	}
}

func TestDeliveryReopenResetsReadinessAndPreservesState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "delivery.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	first := env.mustAppend(t, s, "first commission")
	attempt := env.mustSpend(t, s, writer, first)
	env.mustVerify(t, s, writer, attempt)
	env.mustReceipt(t, s, env.receiptKey(t, s, attempt.MessageID), env.receiptFor(attempt))
	env.mustAppend(t, s, "second commission")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen: history, statuses and ownership survive; readiness does not.
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != first.ID || page[0].Status != "observed" || page[1].Status != "stored" {
		t.Fatalf("history after reopen: %+v", page)
	}
	ctx, err := s.DeliveryContext(t.Context(), env.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ctx["status"] != "active" || ctx["writer_id"] != writer {
		t.Fatalf("ownership after reopen: %+v", ctx)
	}
	if _, err := env.append(t, s, uuid.NewString(), "before ready"); !observerIs(err, "not_ready") {
		t.Fatalf("append before ready after reopen: %v", err)
	}
	if err := s.DeliveryReady(t.Context(), env.binding.ID, writer); err != nil {
		t.Fatal(err)
	}
	env.mustAppend(t, s, "after ready")
	// A spend replay after reopen reports the stored attempt without a right.
	replayed := env.mustSpend(t, s, writer, first)
	if replayed.Right || replayed.AttemptID != attempt.AttemptID {
		t.Fatalf("reopen reissued a right: %+v", replayed)
	}
}

// walkDeliveryPages follows the opaque cursor to the end of the task history.
func walkDeliveryPages(t *testing.T, s *Store, taskID string, limit int) []DeliveryMessage {
	t.Helper()
	seen := map[string]bool{}
	var ordered []DeliveryMessage
	after := int64(0)
	for {
		page, next, err := s.ListDeliveryMessages(t.Context(), taskID, after, limit, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range page {
			if seen[m.ID] {
				t.Fatalf("duplicate %s", m.ID)
			}
			seen[m.ID] = true
			ordered = append(ordered, m)
		}
		if len(page) < limit {
			break
		}
		if next <= after {
			t.Fatalf("cursor did not advance: %d -> %d", after, next)
		}
		after = next
	}
	return ordered
}

func TestDeliveryPaginationHasNoSkipsOrDuplicates(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	env.mustAttachReady(t, s, uuid.NewString(), "")
	const total = 120
	texts := make([]string, total)
	for i := range texts {
		texts[i] = "commission-" + uuid.NewString()
		env.mustAppend(t, s, texts[i])
	}
	ordered := walkDeliveryPages(t, s, env.task.ID, 50)
	if len(ordered) != total {
		t.Fatalf("page walk returned %d of %d", len(ordered), total)
	}
	for i := range texts {
		if ordered[i].Text != texts[i] {
			t.Fatalf("order mismatch at %d", i)
		}
	}
	// An over-limit page is clamped to the contract maximum.
	clamped, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 500, nil)
	if err != nil || len(clamped) != 50 {
		t.Fatalf("clamp: %d %v", len(clamped), err)
	}
	// ids re-read returns exactly the requested visible rows in record order.
	ids := make([]string, 0, 5)
	want := map[string]string{}
	for _, m := range ordered[40:45] {
		ids = append(ids, m.ID)
		want[m.ID] = m.Text
	}
	reRead, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(reRead) != len(ids) {
		t.Fatalf("ids re-read: %d of %d", len(reRead), len(ids))
	}
	for i := range reRead {
		if reRead[i].ID != ids[i] || want[reRead[i].ID] != reRead[i].Text {
			t.Fatalf("ids re-read mismatch at %d", i)
		}
	}
	// A cursor at the newest record yields an empty tail.
	tail, tailNext, err := s.ListDeliveryMessages(t.Context(), env.task.ID, maxDeliverySeq(t, s, env.task.ID), 50, nil)
	if err != nil || len(tail) != 0 || tailNext != maxDeliverySeq(t, s, env.task.ID) {
		t.Fatalf("tail page: %+v %d %v", tail, tailNext, err)
	}
}

func TestDeliveryConcurrentSpendAdmitsExactlyOneWinner(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "one commission")
	second, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	results := make(chan DeliveryAttemptResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, target := range []*Store{s, second} {
		wg.Go(func() {
			r, err := target.DeliverySpend(t.Context(), env.binding.ID, writer, m.ID)
			if err != nil {
				errs <- err
				return
			}
			results <- r
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	winners := 0
	attemptIDs := map[string]bool{}
	for r := range results {
		if r.Right {
			winners++
		}
		if r.AttemptID == "" {
			t.Fatal("empty attempt id")
		}
		attemptIDs[r.AttemptID] = true
	}
	if winners != 1 || len(attemptIDs) != 1 {
		t.Fatalf("winners=%d attempts=%v", winners, attemptIDs)
	}
	if countRows(t, s, "delivery_attempts") != 1 {
		t.Fatal("concurrent spends recorded more than one attempt")
	}
	// Both sides now observe the committed attempt without any right.
	for _, target := range []*Store{s, second} {
		r, err := target.DeliverySpend(t.Context(), env.binding.ID, writer, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.Right || !attemptIDs[r.AttemptID] {
			t.Fatalf("replay reissued a right: %+v", r)
		}
	}
	next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer)
	if err != nil || next.ID != "" {
		t.Fatalf("spent message requeued: %+v %v", next, err)
	}
}

func TestDeliveryReceiptRepublishNeverGrantsASecondRight(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	env.mustVerify(t, s, writer, attempt)
	key := env.receiptKey(t, s, attempt.MessageID)
	published := env.mustReceipt(t, s, key, env.receiptFor(attempt))
	if published.ID != m.ID {
		t.Fatalf("receipt for wrong message: %+v", published)
	}
	if attemptState(t, s, attempt.AttemptID) != "observed" {
		t.Fatal("attempt not observed")
	}
	// Lost ACK: the identical republication replays the committed result.
	replayed, err := s.DeliveryReceipt(t.Context(), env.binding.ID, key, env.receiptFor(attempt))
	if err != nil || replayed.ID != m.ID || replayed.Status != "observed" {
		t.Fatalf("republish: %+v %v", replayed, err)
	}
	if deliveryReceiptCount(t, s, env.binding.ID) != 1 {
		t.Fatal("republish recorded a second receipt")
	}
	// A new writer on the unchanged assignment may republish the stored
	// receipt with the original key, but never acquires the spent right.
	writer2 := uuid.NewString()
	env.mustAttachReady(t, s, writer2, writer)
	republished, err := s.DeliveryReceipt(t.Context(), env.binding.ID, key, env.receiptFor(attempt))
	if err != nil || republished.ID != m.ID || republished.Status != "observed" {
		t.Fatalf("new-writer republish: %+v %v", republished, err)
	}
	if again := env.mustSpend(t, s, writer2, m); again.Right {
		t.Fatalf("republish reissued a right: %+v", again)
	}
	// The republishing writer never owned the spent right, so verify refuses it.
	if _, err := s.DeliveryVerify(t.Context(), env.binding.ID, writer2, attempt.AttemptID); !observerIs(err, "owner_conflict") {
		t.Fatalf("foreign verify: %v", err)
	}
}

func TestDeliveryReplaySemantics(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	key, text := uuid.NewString(), "original body"
	first, err := env.append(t, s, key, text)
	if err != nil {
		t.Fatal(err)
	}
	// Identical replay returns the original commission.
	replayed, err := env.append(t, s, key, text)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("matched replay: %+v %v", replayed, err)
	}
	// An incompatible body on the same key is a conflict and records nothing.
	if _, err := env.append(t, s, key, "tampered body"); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("incompatible replay: %v", err)
	}
	if countRows(t, s, "delivery_messages") != 1 {
		t.Fatal("incompatible replay recorded a message")
	}
	// Enable replays the stored binding; an incompatible enable conflicts. The
	// single installation slot frees up only once the first binding is revoked.
	env.revoke(t, s)
	otherID, otherVerifier, otherOwner := mustObserver(t, s)
	otherReg := mustRegister(t, s, otherVerifier)
	mustOwner(t, s, otherID, "bind", otherOwner.Version, otherReg.RuntimeID, env.project.ID)
	enable := DeliveryOwnerMutation{
		ObserverID: otherID, Key: uuid.NewString(), Action: "enable", ExpectedVer: observerVersion(t, s, otherID),
		BindingID: uuid.NewString(), JournalID: uuid.NewString(), Verifier: observerHash(uuid.NewString()),
		RuntimeID: otherReg.RuntimeID, ProjectID: env.project.ID, PiSessionID: otherReg.PiSessionID, InstanceID: env.instance,
	}
	b2, err := s.MutateDeliveryBinding(t.Context(), enable)
	if err != nil {
		t.Fatal(err)
	}
	replayedBinding, err := s.MutateDeliveryBinding(t.Context(), enable)
	if err != nil || replayedBinding != b2 {
		t.Fatalf("matched enable replay: %+v %v", replayedBinding, err)
	}
	enable.BindingID = uuid.NewString()
	if _, err := s.MutateDeliveryBinding(t.Context(), enable); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("incompatible enable replay: %v", err)
	}
	// A matched replay is served even after revoke, when freshness gates refuse.
	fourth, err := env.append(t, s, key, text)
	if err != nil || fourth.ID != first.ID {
		t.Fatalf("matched replay after revoke: %+v %v", fourth, err)
	}
	if _, err := env.append(t, s, key, "tampered after revoke"); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("incompatible replay after revoke: %v", err)
	}
	if countRows(t, s, "delivery_messages") != 1 {
		t.Fatal("replay after revoke recorded a message")
	}
}

func TestDeliveryForeignScopeRefusedWithoutRecord(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	env.mustAttachReady(t, s, uuid.NewString(), "")
	_, task2 := seedAt(t, s, "/delivery/other"+uuid.NewString())
	// A task from another project is refused even with an active ready binding.
	_, err := s.AppendDeliveryMessage(t.Context(), task2.ID, DeliveryAppend{
		Key: uuid.NewString(), Text: "cross-project", BindingID: env.binding.ID, InstanceID: env.instance,
	})
	if !observerIs(err, "assignment_mismatch") {
		t.Fatalf("cross-project append: %v", err)
	}
	// A stale database instance id is refused before replay or any record.
	_, err = s.AppendDeliveryMessage(t.Context(), env.task.ID, DeliveryAppend{
		Key: uuid.NewString(), Text: "wrong instance", BindingID: env.binding.ID, InstanceID: uuid.NewString(),
	})
	if !observerIs(err, "instance_mismatch") {
		t.Fatalf("instance check: %v", err)
	}
	// An unknown task is a plain miss.
	_, err = s.AppendDeliveryMessage(t.Context(), uuid.NewString(), DeliveryAppend{
		Key: uuid.NewString(), Text: "nowhere", BindingID: env.binding.ID, InstanceID: env.instance,
	})
	if !observerIs(err, "not_found") {
		t.Fatalf("unknown task: %v", err)
	}
	if countRows(t, s, "delivery_messages") != 0 || deliveryReceiptCount(t, s, "binding:"+env.binding.ID) != 0 {
		t.Fatal("foreign scope left a record")
	}
}

func TestDeliveryInvalidTextRefusedWithoutRecord(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	env.mustAttachReady(t, s, uuid.NewString(), "")
	for _, text := range []string{"", "with\x00nul", string([]byte{0xff, 0xfe})} {
		if _, err := env.append(t, s, uuid.NewString(), text); !observerIs(err, "invalid_request") {
			t.Fatalf("text %q: %v", text, err)
		}
	}
	long := make([]byte, deliveryMaxText+1)
	for i := range long {
		long[i] = 'x'
	}
	if _, err := env.append(t, s, uuid.NewString(), string(long)); !observerIs(err, "invalid_request") {
		t.Fatalf("oversize text: %v", err)
	}
	// Boundary text with control bytes (except NUL) round-trips.
	control := "line1\r\nline2\t\x01ok"
	m, err := env.append(t, s, uuid.NewString(), control)
	if err != nil || m.Text != control {
		t.Fatalf("boundary text refused or altered: %+v %v", m, err)
	}
	if got := countRows(t, s, "delivery_messages"); got != 1 {
		t.Fatalf("invalid text left records: %d", got)
	}
}

func TestDeliveryCloseBlocksPermanently(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	closedKey, closedText := uuid.NewString(), "to be closed"
	first, err := env.append(t, s, closedKey, closedText)
	if err != nil {
		t.Fatal(err)
	}
	second := env.mustAppend(t, s, "second")
	attempt := env.mustSpend(t, s, writer, first)
	// Closing the task blocks unspent commissions and pending confirmations.
	transitionTask(t, s, env.task.ID, env.project.ID, domain.Done)
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page {
		if !m.Blocked {
			t.Fatalf("message not blocked after close: %+v", m)
		}
	}
	if next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); err != nil || next.ID != "" {
		t.Fatalf("blocked queue still served: %+v %v", next, err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, second.ID); !observerIs(err, "closed") {
		t.Fatalf("spend after close: %v", err)
	}
	if _, err := s.DeliveryVerify(t.Context(), env.binding.ID, writer, attempt.AttemptID); !observerIs(err, "closed") {
		t.Fatalf("verify after close: %v", err)
	}
	if _, err := env.append(t, s, uuid.NewString(), "after close"); !observerIs(err, "closed") {
		t.Fatalf("append after close: %v", err)
	}
	// A matched replay is still served for the commission committed before close.
	replayed, err := env.append(t, s, closedKey, closedText)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replay after close: %+v %v", replayed, err)
	}
	// Reopening the task must not resume the old queue.
	transitionTask(t, s, env.task.ID, env.project.ID, domain.Backlog)
	if next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); err != nil || next.ID != "" {
		t.Fatalf("reopen resumed queue: %+v %v", next, err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, second.ID); !observerIs(err, "closed") {
		t.Fatalf("spend after reopen: %v", err)
	}
	if countRows(t, s, "delivery_attempts") != 1 {
		t.Fatal("blocked spends recorded attempts")
	}
}

func TestDeliveryArchiveBlocksPermanently(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	first := env.mustAppend(t, s, "before archive")
	archiveProject(t, s, env.project.ID, true)
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || !page[0].Blocked || page[0].ID != first.ID {
		t.Fatalf("archive did not block: %+v", page)
	}
	if _, err := env.append(t, s, uuid.NewString(), "while archived"); !observerIs(err, "closed") {
		t.Fatalf("append while archived: %v", err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, first.ID); !observerIs(err, "closed") {
		t.Fatalf("spend while archived: %v", err)
	}
	archiveProject(t, s, env.project.ID, false)
	if next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); err != nil || next.ID != "" {
		t.Fatalf("unarchive resumed queue: %+v %v", next, err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, first.ID); !observerIs(err, "closed") {
		t.Fatalf("spend after unarchive: %v", err)
	}
}

func TestDeliveryAssignmentChangeInvalidatesBinding(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	// Rebind to a new runtime: the observer generation moves on.
	r2 := mustRegister(t, s, env.observerVerifier)
	rebound := mustOwner(t, s, env.observer, "bind", observerView(t, s).Version, r2.RuntimeID, env.project.ID)
	if rebound.Generation == env.binding.Generation {
		t.Fatal("rebind did not bump generation")
	}
	if _, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); !observerIs(err, "revoked") {
		t.Fatalf("next after rebind: %v", err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, m.ID); !observerIs(err, "revoked") {
		t.Fatalf("spend after rebind: %v", err)
	}
	if _, err := env.append(t, s, uuid.NewString(), "stale assignment"); !observerIs(err, "not_ready") {
		t.Fatalf("append after rebind: %v", err)
	}
	if countRows(t, s, "delivery_messages") != 1 || countRows(t, s, "delivery_attempts") != 0 {
		t.Fatal("stale assignment recorded delivery state")
	}
	// Revoking the observer invalidates the assignment as well.
	mustOwner(t, s, env.observer, "revoke", rebound.Version, "", "")
	if _, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); !observerIs(err, "revoked") {
		t.Fatalf("next after observer revoke: %v", err)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, m.ID); !observerIs(err, "revoked") {
		t.Fatalf("spend after observer revoke: %v", err)
	}
}

func TestDeliveryRevokeBeforeAndAfterVerify(t *testing.T) {
	s := openTest(t)
	// Revoke between spend and verify: confirmation is refused, attempt stays spent.
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "before revoke")
	attempt := env.mustSpend(t, s, writer, m)
	env.revoke(t, s)
	if _, err := s.DeliveryVerify(t.Context(), env.binding.ID, writer, attempt.AttemptID); !observerIs(err, "revoked") {
		t.Fatalf("verify after revoke: %v", err)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "spent" {
		t.Fatalf("attempt state changed: %s", state)
	}
	if _, err := s.DeliverySpend(t.Context(), env.binding.ID, writer, m.ID); err == nil {
		t.Fatal("spend after revoke succeeded")
	}
	if countRows(t, s, "delivery_attempts") != 1 {
		t.Fatal("post-revoke spend recorded a new attempt")
	}
	if _, err := s.AuthenticateDeliveryBinding(t.Context(), env.deliveryVerifier); !observerIs(err, "unauthorized") {
		t.Fatalf("revoked token still authenticates: %v", err)
	}
	// Revoke after verify: no new confirmation, no right ever reappears.
	env2 := mustDeliveryEnv(t, s)
	writer2 := uuid.NewString()
	env2.mustAttachReady(t, s, writer2, "")
	m2 := env2.mustAppend(t, s, "verified first")
	attempt2 := env2.mustSpend(t, s, writer2, m2)
	verified := env2.mustVerify(t, s, writer2, attempt2)
	if !verified.Verified || verified.Right {
		t.Fatalf("verify: %+v", verified)
	}
	env2.revoke(t, s)
	if _, err := s.DeliveryVerify(t.Context(), env2.binding.ID, writer2, attempt2.AttemptID); !observerIs(err, "revoked") {
		t.Fatalf("verify after post-verify revoke: %v", err)
	}
	if _, err := s.AuthenticateDeliveryBinding(t.Context(), env2.deliveryVerifier); !observerIs(err, "unauthorized") {
		t.Fatalf("revoked token still authenticates: %v", err)
	}
}

func TestDeliveryReceiptAfterRevokeRejectedWithUnknownPreserved(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	env.revoke(t, s)
	// A first-time receipt with correct details is still refused after revoke.
	if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, uuid.NewString(), env.receiptFor(attempt)); !observerIs(err, "revoked") {
		t.Fatalf("receipt after revoke: %v", err)
	}
	if deliveryReceiptCount(t, s, env.binding.ID) != 0 {
		t.Fatal("rejected receipt was recorded")
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != "unknown" {
		t.Fatalf("unknown outcome not preserved: %+v", page)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "spent" {
		t.Fatalf("attempt outcome changed: %s", state)
	}
}

func TestDeliveryWrongReceiptDetailsRefused(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	valid := env.receiptFor(attempt)
	cases := map[string]func(*deliveryReceiptBodyT){
		"type":         func(b *deliveryReceiptBodyT) { b.Type = "after_agent_start" },
		"message_type": func(b *deliveryReceiptBodyT) { b.MessageType = "text" },
		"session":      func(b *deliveryReceiptBodyT) { b.PiSessionID = uuid.NewString() },
		"no_details":   func(b *deliveryReceiptBodyT) { b.Details = nil },
		"message_id":   func(b *deliveryReceiptBodyT) { b.Details["message_id"] = uuid.NewString() },
		"attempt_id":   func(b *deliveryReceiptBodyT) { b.Details["attempt_id"] = uuid.NewString() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := valid
			body.Details = map[string]any{"message_id": valid.MessageID, "attempt_id": valid.AttemptID}
			mutate(&body)
			if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, uuid.NewString(), body); !observerIs(err, "invalid_request") {
				t.Fatalf("receipt: %v", err)
			}
		})
	}
	if deliveryReceiptCount(t, s, env.binding.ID) != 0 {
		t.Fatal("wrong-details receipt recorded")
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != "unknown" {
		t.Fatalf("message changed: %+v", page)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "spent" {
		t.Fatalf("attempt changed: %s", state)
	}
	// The correct receipt still publishes afterwards, once the call is confirmed.
	env.mustVerify(t, s, writer, attempt)
	env.mustReceipt(t, s, env.receiptKey(t, s, attempt.MessageID), valid)
}

func TestDeliverySpentAttemptWithoutReceiptStaysOutOfQueue(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	first := env.mustAppend(t, s, "spent, no receipt")
	second := env.mustAppend(t, s, "still waiting")
	attempt := env.mustSpend(t, s, writer, first)
	env.mustVerify(t, s, writer, attempt)
	next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer)
	if err != nil || next.ID != second.ID {
		t.Fatalf("spent message requeued: %+v %v", next, err)
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Status != "unknown" || page[1].Status != "stored" {
		t.Fatalf("statuses: %+v", page)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "verified" {
		t.Fatalf("attempt: %s", state)
	}
}

func TestDeliveryLostAckOnObservedNeverDowngrades(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	env.mustVerify(t, s, writer, attempt)
	key := env.receiptKey(t, s, attempt.MessageID)
	env.mustReceipt(t, s, key, env.receiptFor(attempt))
	// Lost ACK on the committing response: the retry replays the stored result.
	if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, key, env.receiptFor(attempt)); err != nil {
		t.Fatalf("lost-ACK republish: %v", err)
	}
	// Revoke kills the old token; even then the committed observed stands.
	env.revoke(t, s)
	replayed, err := s.DeliveryReceipt(t.Context(), env.binding.ID, key, env.receiptFor(attempt))
	if err != nil || replayed.Status != "observed" {
		t.Fatalf("observed downgraded after revoke: %+v %v", replayed, err)
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Status != "observed" {
		t.Fatalf("history downgraded: %+v", page)
	}
	if deliveryReceiptCount(t, s, env.binding.ID) != 1 {
		t.Fatal("replays multiplied receipts")
	}
}

func TestDeliveryEnableStaleVersionAndSlotOccupied(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	// A stale observer version must not create a binding.
	stale := DeliveryOwnerMutation{
		ObserverID: env.observer, Key: uuid.NewString(), Action: "enable",
		ExpectedVer: observerView(t, s).Version + 1, BindingID: uuid.NewString(), JournalID: uuid.NewString(),
		Verifier: observerHash(uuid.NewString()), RuntimeID: env.runtime.RuntimeID, ProjectID: env.project.ID,
		PiSessionID: env.runtime.PiSessionID, InstanceID: env.instance,
	}
	if _, err := s.MutateDeliveryBinding(t.Context(), stale); !observerIs(err, "version_conflict") {
		t.Fatalf("stale enable: %v", err)
	}
	if countRows(t, s, "delivery_bindings") != 1 {
		t.Fatal("stale enable recorded a binding")
	}
	// A second enable while the first is non-revoked loses the single slot.
	second := env.freshEnable(t, s)
	if _, err := s.MutateDeliveryBinding(t.Context(), second); !observerIs(err, "slot_occupied") {
		t.Fatalf("second enable: %v", err)
	}
	if countRows(t, s, "delivery_bindings") != 1 {
		t.Fatal("slot_occupied recorded a binding")
	}
	// Revoking the observer frees the slot: a fresh observer can be enabled.
	mustOwner(t, s, env.observer, "revoke", observerView(t, s).Version, "", "")
	enabled := enableSecondObserver(t, s, env)
	if enabled.Status != "pending" {
		t.Fatalf("enable after observer revoke: %+v", enabled)
	}
}

// enableSecondObserver provisions, binds and enables a fresh observer against
// the env's project, proving the installation slot is free.
func enableSecondObserver(t *testing.T, s *Store, env deliveryEnv) DeliveryBinding {
	t.Helper()
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, r.RuntimeID, env.project.ID)
	enabled, err := s.MutateDeliveryBinding(t.Context(), DeliveryOwnerMutation{
		ObserverID: id, Key: uuid.NewString(), Action: "enable",
		ExpectedVer: owner.Version, BindingID: uuid.NewString(), JournalID: uuid.NewString(),
		Verifier: observerHash(uuid.NewString()), RuntimeID: r.RuntimeID,
		ProjectID: env.project.ID, PiSessionID: r.PiSessionID, InstanceID: env.instance,
	})
	if err != nil {
		t.Fatal(err)
	}
	return enabled
}

func TestDeliveryVerifyRefusedAfterRebind(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	// Rebind between spend and verify: confirmation must not cross the change.
	r2 := mustRegister(t, s, env.observerVerifier)
	mustOwner(t, s, env.observer, "bind", observerView(t, s).Version, r2.RuntimeID, env.project.ID)
	if _, err := s.DeliveryVerify(t.Context(), env.binding.ID, writer, attempt.AttemptID); !observerIs(err, "revoked") {
		t.Fatalf("verify after rebind: %v", err)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "spent" {
		t.Fatalf("attempt outcome changed: %s", state)
	}
	// The first receipt under the stale assignment is rejected as well.
	if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, env.receiptKey(t, s, m.ID), env.receiptFor(attempt)); !observerIs(err, "revoked") {
		t.Fatalf("receipt after rebind: %v", err)
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil || len(page) != 1 || page[0].Status != "unknown" {
		t.Fatalf("outcome: %+v %v", page, err)
	}
}

func TestDeliveryUnbindFreesSlot(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	// Unbind (bind with empty runtime/project) revokes the delivery binding.
	mustOwner(t, s, env.observer, "bind", observerView(t, s).Version, "", "")
	if _, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer); !observerIs(err, "revoked") {
		t.Fatalf("next after unbind: %v", err)
	}
	enabled := enableSecondObserver(t, s, env)
	if enabled.Status != "pending" {
		t.Fatalf("enable after unbind: %+v", enabled)
	}
}

func TestDeliveryReceiptAfterCloseAcceptsVerifiedTurn(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	env.mustVerify(t, s, writer, attempt)
	// A never-verified attempt stays unknown across the same close.
	late := env.mustAppend(t, s, "late commission")
	spent := env.mustSpend(t, s, writer, late)
	// Close the task after the first call was confirmed: the begun turn may
	// still publish its receipt under the unchanged assignment.
	transitionTask(t, s, env.task.ID, env.project.ID, domain.Done)
	published, err := s.DeliveryReceipt(t.Context(), env.binding.ID, env.receiptKey(t, s, m.ID), env.receiptFor(attempt))
	if err != nil || published.Status != "observed" {
		t.Fatalf("verified-turn receipt after close: %+v %v", published, err)
	}
	if _, err := s.DeliveryVerify(t.Context(), env.binding.ID, writer, spent.AttemptID); !observerIs(err, "closed") {
		t.Fatalf("verify after close: %v", err)
	}
	if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, env.receiptKey(t, s, late.ID), env.receiptFor(spent)); !observerIs(err, "closed") {
		t.Fatalf("receipt of unverified attempt after close: %v", err)
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Status != "observed" || page[1].Status != "unknown" || !page[1].Blocked || page[1].BlockReason != "task_closed" {
		t.Fatalf("close outcomes: %+v", page)
	}
}

func TestDeliveryReceiptOfUnverifiedAttemptRefused(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	m := env.mustAppend(t, s, "commission")
	attempt := env.mustSpend(t, s, writer, m)
	// A receipt can only follow a confirmed call: spent-but-never-verified
	// is refused and the message stays unknown.
	if _, err := s.DeliveryReceipt(t.Context(), env.binding.ID, env.receiptKey(t, s, m.ID), env.receiptFor(attempt)); !observerIs(err, "invalid_request") {
		t.Fatalf("receipt of spent attempt: %v", err)
	}
	page, _, err := s.ListDeliveryMessages(t.Context(), env.task.ID, 0, 50, nil)
	if err != nil || len(page) != 1 || page[0].Status != "unknown" {
		t.Fatalf("outcome: %+v %v", page, err)
	}
	if state := attemptState(t, s, attempt.AttemptID); state != "spent" {
		t.Fatalf("attempt changed: %s", state)
	}
}

func TestDeliveryNextRefusesForeignWriter(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	writer := uuid.NewString()
	env.mustAttachReady(t, s, writer, "")
	env.mustAppend(t, s, "commission")
	if _, _, err := s.DeliveryNext(t.Context(), env.binding.ID, uuid.NewString()); !observerIs(err, "owner_conflict") {
		t.Fatalf("next with foreign writer: %v", err)
	}
	next, _, err := s.DeliveryNext(t.Context(), env.binding.ID, writer)
	if err != nil || next.ID == "" {
		t.Fatalf("next with current writer: %+v %v", next, err)
	}
}

func TestDeliveryEnableRejectsObserverVerifierReuse(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	// Revoke the env binding to free the slot, then attempt a new enable on a
	// fresh observer using that observer's own verifier as the credential.
	env.revoke(t, s)
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, r.RuntimeID, env.project.ID)
	m := DeliveryOwnerMutation{
		ObserverID: id, Key: uuid.NewString(), Action: "enable",
		ExpectedVer: owner.Version, BindingID: uuid.NewString(), JournalID: uuid.NewString(),
		Verifier: verifier, RuntimeID: r.RuntimeID, ProjectID: env.project.ID,
		PiSessionID: r.PiSessionID, InstanceID: env.instance,
	}
	if _, err := s.MutateDeliveryBinding(t.Context(), m); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("observer verifier reuse: %v", err)
	}
	if countRows(t, s, "delivery_bindings") != 1 {
		t.Fatal("rejected enable recorded a binding")
	}
}

func TestObserverProvisionRejectsDeliveryVerifierReuse(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	// The delivery credential must never be provisioned as an observer
	// credential: one token, one channel.
	if _, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{
		ObserverID: uuid.NewString(), Key: uuid.NewString(), Action: "provision",
		Label: "Fictional", Verifier: env.deliveryVerifier,
	}); !observerIs(err, "idempotency_conflict") {
		t.Fatalf("provision with delivery verifier: %v", err)
	}
	if countRows(t, s, "observers") != 1 {
		t.Fatal("rejected provision recorded an observer")
	}
}

func TestDeliveryEnableReplayAfterInstanceChange(t *testing.T) {
	s := openTest(t)
	env := mustDeliveryEnv(t, s)
	m := env.freshEnable(t, s)
	// A recorded key replayed with a different instance identity must pin the
	// database first: instance_mismatch, never the historical receipt.
	m.InstanceID = uuid.NewString()
	if _, err := s.MutateDeliveryBinding(t.Context(), m); !observerIs(err, "instance_mismatch") {
		t.Fatalf("replayed enable with wrong instance: %v", err)
	}
	if countRows(t, s, "delivery_bindings") != 1 {
		t.Fatal("rejected enable recorded a binding")
	}
}
