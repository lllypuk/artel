package store

import (
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestObserverRegisterAndReportFaultRollback(t *testing.T) {
	s := openTest(t)
	_, verifier, _ := mustObserver(t, s)
	r := registration()
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER injected BEFORE INSERT ON observer_runtimes BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterObserver(t.Context(), verifier, r); err == nil {
		t.Fatal("registration fault not observed")
	}
	if countRows(t, s, "observer_identities") != 0 || countRows(t, s, "observer_runtimes") != 0 {
		t.Fatal("partial registration")
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER injected"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterObserver(t.Context(), verifier, r); err != nil {
		t.Fatal(err)
	}
	report := reportFor(r, 0, 1)
	mustReport(t, s, verifier, report)
	before := observerView(t, s)
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER injected BEFORE INSERT ON observer_runtimes BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	report.ReportSeq++
	if _, err := s.ReportObserver(t.Context(), verifier, report); err == nil {
		t.Fatal("report fault not observed")
	}
	if after := observerView(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("report state changed on rollback")
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER injected"); err != nil {
		t.Fatal(err)
	}
	if got := mustReport(t, s, verifier, report); got.Result != "accepted" {
		t.Fatal("failed report consumed sequence")
	}
}

func TestObserverPruneFaultPreservesPayloadAndIdentity(t *testing.T) {
	s := openTest(t)
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	mustReport(t, s, verifier, reportFor(r, 0, 1))
	before := observerView(t, s)
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER injected BEFORE UPDATE ON observer_identities BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	_, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: owner.Version})
	if err == nil {
		t.Fatal("prune fault not observed")
	}
	if after := observerView(t, s); !reflect.DeepEqual(before, after) {
		t.Fatal("prune partially committed")
	}
	if _, err := s.RegisterObserver(t.Context(), verifier, r); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, "observer_receipts") != 1 || countRows(t, s, "observer_audit") != 1 {
		t.Fatal("failed prune left owner action")
	}
}

func TestObserverProvisionAtomicReplayAndUniqueVerifier(t *testing.T) {
	s := openTest(t)
	m := ObserverOwnerMutation{ObserverID: uuid.NewString(), Key: uuid.NewString(), Action: "provision", Label: "Fictional", Verifier: observerHash(uuid.NewString())}
	if _, err := s.db.ExecContext(t.Context(), "CREATE TRIGGER injected BEFORE INSERT ON observer_receipts BEGIN SELECT RAISE(ABORT,'injected failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateObserver(t.Context(), m); err == nil {
		t.Fatal("provision fault not observed")
	}
	if countRows(t, s, "observers") != 0 {
		t.Fatal("partial provision")
	}
	if _, err := s.db.ExecContext(t.Context(), "DROP TRIGGER injected"); err != nil {
		t.Fatal(err)
	}
	first, err := s.MutateObserver(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.MutateObserver(t.Context(), m); err != nil || got != first {
		t.Fatalf("provision replay: %+v %v", got, err)
	}
	changed := m
	changed.Label = "different"
	_, err = s.MutateObserver(t.Context(), changed)
	expectObserverError(t, err, "idempotency_conflict")
	changed = m
	changed.ObserverID = uuid.NewString()
	_, err = s.MutateObserver(t.Context(), changed)
	expectObserverError(t, err, "idempotency_conflict")
	// Identical keys on separate principals are independent, and receipts never contain verifier.
	changed.Verifier = observerHash(uuid.NewString())
	if _, err := s.MutateObserver(t.Context(), changed); err != nil {
		t.Fatal(err)
	}
	var leaks int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM observer_receipts WHERE instr(result,?)>0", m.Verifier).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatal("verifier in receipt")
	}
}

func TestObserverConcurrentRegistrationQuotaAndDuplicateReport(t *testing.T) {
	s := openTest(t)
	_, verifier, _ := mustObserver(t, s)
	for range 7 {
		mustRegister(t, s, verifier)
	}
	second, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, target := range []*Store{s, second} {
		wg.Go(func() { <-start; _, err := target.RegisterObserver(t.Context(), verifier, registration()); errs <- err })
	}
	close(start)
	wg.Wait()
	close(errs)
	accepted, capacity := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case observerIs(err, "capacity_exceeded"):
			capacity++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 1 || capacity != 1 || countRows(t, s, "observer_runtimes") != 8 {
		t.Fatal("quota race")
	}
	// The same seq/body has one acceptance, one duplicate even on separate connections.
	r := observerView(t, s).Runtimes[0].Registration
	report := reportFor(r, 0, 1)
	results := make(chan ObserverReportResult, 2)
	errs = make(chan error, 2)
	for _, target := range []*Store{s, second} {
		wg.Go(func() { got, err := target.ReportObserver(t.Context(), verifier, report); results <- got; errs <- err })
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	kinds := map[string]int{}
	for result := range results {
		kinds[result.Result]++
	}
	if kinds["accepted"] != 1 || kinds["duplicate"] != 1 {
		t.Fatal(kinds)
	}
}

func TestObserverBindRevokeCASRace(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"bind", "revoke"} {
		wg.Go(func() {
			<-start
			_, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: action, ExpectedVersion: owner.Version, RuntimeID: r.RuntimeID, ProjectID: p.ID})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	accepted, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			accepted++
		case observerIs(err, "version_conflict"):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatal("bind/revoke both passed same CAS")
	}
	view := observerView(t, s)
	if !view.Revoked {
		mustOwner(t, s, id, "revoke", view.Version, "", "")
	}
	_, err := s.ReportObserver(t.Context(), verifier, reportFor(r, view.Generation, 1))
	expectObserverError(t, err, "unauthorized")
}
