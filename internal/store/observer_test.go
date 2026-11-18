package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func mustObserver(t *testing.T, s *Store) (string, string, ObserverOwnerResult) {
	t.Helper()
	id := uuid.NewString()
	verifier := observerHash(uuid.NewString())
	r, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "provision", Label: "Fictional observer", Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	return id, verifier, r
}
func registration() ObserverRegistration {
	return ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1.5.2", BridgeVersion: "0.1.0", Cwd: "/fictional/project"}
}
func mustRegister(t *testing.T, s *Store, verifier string) ObserverRegistration {
	t.Helper()
	r := registration()
	if _, err := s.RegisterObserver(t.Context(), verifier, r); err != nil {
		t.Fatal(err)
	}
	return r
}
func mustOwner(t *testing.T, s *Store, id, action string, version int64, runtime, project string) ObserverOwnerResult {
	t.Helper()
	r, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: action, ExpectedVersion: version, RuntimeID: runtime, ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func reportFor(r ObserverRegistration, generation, seq int64) ObserverReport {
	return ObserverReport{RuntimeID: r.RuntimeID, Generation: generation, ReportSeq: seq, Activity: "idle", Lifecycle: "running"}
}
func expectObserverError(t *testing.T, err error, code string) {
	t.Helper()
	if !observerIs(err, code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func mustReport(t *testing.T, s *Store, verifier string, r ObserverReport) ObserverReportResult {
	t.Helper()
	got, err := s.ReportObserver(t.Context(), verifier, r)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func observerView(t *testing.T, s *Store) ObserverProjection {
	t.Helper()
	r, err := s.Observers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return r.Observers[0]
}
func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestObserverBindingGenerationAndRetirement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observer.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			_ = s.Close()
		}
	}()
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	a := mustRegister(t, s, verifier)
	c, err := s.ObserverContext(t.Context(), verifier, uuid.NewString())
	if err != nil || c.Generation != 0 || c.Binding != nil {
		t.Fatalf("pre-registration: %+v %v", c, err)
	}
	owner = mustOwner(t, s, id, "bind", owner.Version, a.RuntimeID, p.ID)
	if owner.Generation != 1 {
		t.Fatal(owner)
	}
	mustReport(t, s, verifier, reportFor(a, owner.Generation, 1))
	b := mustRegister(t, s, verifier)
	c, err = s.ObserverContext(t.Context(), verifier, b.RuntimeID)
	if err != nil || c.Binding != nil || c.Disposition != "candidate" {
		t.Fatalf("new runtime inherited binding: %+v %v", c, err)
	}
	owner = mustOwner(t, s, id, "bind", owner.Version, b.RuntimeID, p.ID)
	if owner.Generation != 2 {
		t.Fatal(owner)
	}
	c, err = s.RegisterObserver(t.Context(), verifier, a)
	if err != nil || c.Disposition != "superseded" || c.Binding != nil || c.Generation != 1 {
		t.Fatalf("old runtime: %+v %v", c, err)
	}
	_, err = s.ReportObserver(t.Context(), verifier, reportFor(a, 1, 2))
	expectObserverError(t, err, "runtime_superseded")
	_, err = s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "bind", ExpectedVersion: owner.Version, RuntimeID: a.RuntimeID, ProjectID: p.ID})
	expectObserverError(t, err, "runtime_superseded")
	changed := a
	changed.Cwd = "/different"
	_, err = s.RegisterObserver(t.Context(), verifier, changed)
	expectObserverError(t, err, "registration_conflict")
	// The bound stopped runtime is never pruned; replayed old registrations retire permanently.
	stopped := reportFor(b, 2, 1)
	stopped.Lifecycle = "stopped"
	mustReport(t, s, verifier, stopped)
	owner = mustPrune(t, s, id, owner.Version, a.RuntimeID)
	if owner.PrunedCount != 1 {
		t.Fatal(owner)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = nil
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		_, err = s.RegisterObserver(t.Context(), verifier, a)
		expectObserverError(t, err, "runtime_retired")
		_, err = s.ObserverContext(t.Context(), verifier, a.RuntimeID)
		expectObserverError(t, err, "runtime_retired")
		_, err = s.ReportObserver(t.Context(), verifier, reportFor(a, 1, 3))
		expectObserverError(t, err, "runtime_retired")
	}
	verifyRetiredAndRevoke(t, s, id, verifier, owner, b, stopped)
}
func verifyRetiredAndRevoke(t *testing.T, s *Store, id, verifier string, owner ObserverOwnerResult, b ObserverRegistration, stopped ObserverReport) {
	t.Helper()
	view := observerView(t, s)
	if view.IdentityCount != 2 || view.RetiredCount != 1 || len(view.Runtimes) != 1 || view.Runtimes[0].Freshness != "stopped" {
		t.Fatalf("projection: %+v", view)
	}
	mustOwner(t, s, id, "revoke", owner.Version, "", "")
	_, err := s.RegisterObserver(t.Context(), verifier, b)
	expectObserverError(t, err, "unauthorized")
	_, err = s.ObserverContext(t.Context(), verifier, b.RuntimeID)
	expectObserverError(t, err, "unauthorized")
	_, err = s.ReportObserver(t.Context(), verifier, stopped)
	expectObserverError(t, err, "unauthorized")
	if !observerView(t, s).Revoked {
		t.Fatal("revoke lost")
	}
}

func TestObserverSequencesFreshnessAndNoDomainWrites(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, r.RuntimeID, p.ID)
	// time.Time values retain their monotonic component. Wall time is display-only.
	now := time.Now()
	s.observerClock = func() time.Time { return now }
	before, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, table := range []string{"events", "mutation_receipts", "observer_audit", "observer_receipts"} {
		counts[table] = countRows(t, s, table)
	}
	report := reportFor(r, owner.Generation, 1)
	mustReport(t, s, verifier, report)
	if observerView(t, s).Runtimes[0].Freshness != "fresh" {
		t.Fatal("not fresh")
	}
	now = now.Add(20 * time.Second)
	if mustReport(t, s, verifier, report).Result != "duplicate" || observerView(t, s).Runtimes[0].Freshness != "stale" {
		t.Fatal("duplicate renewed freshness")
	}
	changed := report
	changed.Activity = "working"
	_, err = s.ReportObserver(t.Context(), verifier, changed)
	expectObserverError(t, err, "sequence_conflict")
	for seq := int64(2); seq <= 10000; seq++ {
		report.ReportSeq = seq
		mustReport(t, s, verifier, report)
	}
	report.ReportSeq = 9999
	_, err = s.ReportObserver(t.Context(), verifier, report)
	expectObserverError(t, err, "sequence_conflict")
	for table, count := range counts {
		if got := countRows(t, s, table); got != count {
			t.Fatalf("%s grew: %d -> %d", table, count, got)
		}
	}
	after, err := s.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("manual state changed")
	}
	if countRows(t, s, "observer_runtimes") != 1 {
		t.Fatal("heartbeat history grew")
	}
	// No process-local freshness survives reopening, even with a future last_seen.
	other, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if observerView(t, other).Runtimes[0].Freshness != "stale" {
		t.Fatal("restart falsely fresh")
	}
	report.ReportSeq = 10000
	if got := mustReport(t, other, verifier, report); got.Result != "duplicate" || observerView(t, other).Runtimes[0].Freshness != "stale" {
		t.Fatal("replay after restart renewed freshness")
	}
	report.ReportSeq = 10001
	mustReport(t, other, verifier, report)
	if observerView(t, other).Runtimes[0].Freshness != "fresh" {
		t.Fatal("restart report rejected")
	}
}
func (s *Store) dbPathForTest(t *testing.T) string {
	t.Helper()
	var seq int
	var name, path string
	if err := s.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestObserverProjectCASReceiptsAndIsolation(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	r := mustRegister(t, s, verifier)
	m := ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "bind", ExpectedVersion: owner.Version, RuntimeID: r.RuntimeID, ProjectID: uuid.NewString()}
	_, err := s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "project_unavailable")
	if _, err := s.db.ExecContext(t.Context(), "UPDATE projects SET archived=1 WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	m.ProjectID = p.ID
	_, err = s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "project_unavailable")
	if _, err := s.db.ExecContext(t.Context(), "UPDATE projects SET archived=0 WHERE id=?", p.ID); err != nil {
		t.Fatal(err)
	}
	first, err := s.MutateObserver(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.MutateObserver(t.Context(), m)
	if err != nil || replay != first {
		t.Fatalf("receipt replay: %+v %v", replay, err)
	}
	different := m
	different.Action = "prune"
	different.ExpectedRuntimeIDs = []string{r.RuntimeID}
	_, err = s.MutateObserver(t.Context(), different)
	expectObserverError(t, err, "idempotency_conflict")
	different = m
	different.Key = uuid.NewString()
	_, err = s.MutateObserver(t.Context(), different)
	expectObserverError(t, err, "version_conflict")
	// A manual receipt with the identical key remains independent.
	_, err = s.Apply(t.Context(), Mutation{Key: m.Key}, func(*Snapshot) (Result, *Event, error) { return Result{Status: 200, Body: []byte(`{}`)}, nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	otherID, otherVerifier, otherOwner := mustObserver(t, s)
	foreign := m
	foreign.ObserverID = otherID
	foreign.ExpectedVersion = otherOwner.Version
	_, err = s.MutateObserver(t.Context(), foreign)
	expectObserverError(t, err, "not_found")
	_, err = s.ReportObserver(t.Context(), otherVerifier, reportFor(r, first.Generation, 1))
	expectObserverError(t, err, "not_found")
	// Generation changes even when only the project changes, and resets the seq namespace.
	otherProject := uuid.NewString()
	if _, err := s.db.ExecContext(t.Context(), "INSERT INTO projects(id,root_path,archived,record) VALUES(?,'/other',0,'{}')", otherProject); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, verifier, reportFor(r, first.Generation, 42))
	rebound := mustOwner(t, s, id, "bind", first.Version, r.RuntimeID, otherProject)
	_, err = s.ReportObserver(t.Context(), verifier, reportFor(r, first.Generation, 43))
	expectObserverError(t, err, "generation_conflict")
	mustReport(t, s, verifier, reportFor(r, rebound.Generation, 1))
	unbound := mustOwner(t, s, id, "bind", rebound.Version, "", "")
	if unbound.Generation != rebound.Generation+1 {
		t.Fatal("unbind did not fence")
	}
	c, err := s.ObserverContext(t.Context(), verifier, r.RuntimeID)
	if err != nil || c.Binding != nil || c.Disposition != "superseded" {
		t.Fatalf("unbind: %+v %v", c, err)
	}
	data, err := json.Marshal(observerView(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), verifier) || strings.Contains(string(data), "verifier") {
		t.Fatal("projection leaked verifier")
	}
}

func TestObserverQuotasAuditAndRetirementBounds(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	bound := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, bound.RuntimeID, p.ID)
	var last ObserverRegistration
	var ids []string
	for range 7 {
		last = mustRegister(t, s, verifier)
		ids = append(ids, last.RuntimeID)
	}
	_, err := s.RegisterObserver(t.Context(), verifier, registration())
	requireCapacity(t, err, "runtime_payloads")
	if _, err := s.RegisterObserver(t.Context(), verifier, last); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, verifier, reportFor(bound, owner.Generation, 1))
	owner = mustPrune(t, s, id, owner.Version, ids...)
	if owner.PrunedCount != 7 {
		t.Fatal(owner)
	}
	// Fill all 1024 lifetime identities through actual registration/pruning.
	for count := 8; count < 1024; {
		ids = ids[:0]
		for n := 0; n < 7 && count < 1024; n++ {
			last = mustRegister(t, s, verifier)
			ids = append(ids, last.RuntimeID)
			count++
		}
		owner = mustPrune(t, s, id, owner.Version, ids...)
	}
	_, err = s.RegisterObserver(t.Context(), verifier, registration())
	requireCapacity(t, err, "runtime_identities")
	_, err = s.RegisterObserver(t.Context(), verifier, last)
	expectObserverError(t, err, "runtime_retired")
	if _, err := s.RegisterObserver(t.Context(), verifier, bound); err != nil {
		t.Fatal(err)
	}
	mustReport(t, s, verifier, reportFor(bound, owner.Generation, 2))
	for range 270 {
		// Valid same-binding CAS mutations preserve the generation and freshness.
		owner = mustOwner(t, s, id, "bind", owner.Version, bound.RuntimeID, p.ID)
	}
	view := observerView(t, s)
	if view.IdentityCount != 1024 || view.RetiredCount != 1023 || len(view.Runtimes) != 1 || view.Runtimes[0].Freshness != "fresh" {
		t.Fatalf("quotas %+v", view)
	}
	if countRows(t, s, "observer_audit") != 256 {
		t.Fatal("audit unbounded")
	}
	// Revoked entries still consume the global quota.
	mustOwner(t, s, id, "revoke", owner.Version, "", "")
	for range 255 {
		mustObserver(t, s)
	}
	_, err = s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: uuid.NewString(), Key: uuid.NewString(), Action: "provision", Label: "overflow", Verifier: observerHash("overflow")})
	requireCapacity(t, err, "observers")
	if countRows(t, s, "observers") != 256 {
		t.Fatal("observer quota")
	}
}
func requireCapacity(t *testing.T, err error, quota string) {
	t.Helper()
	expectObserverError(t, err, "capacity_exceeded")
	if err.(*ObserverError).Capacity != quota {
		t.Fatalf("wrong quota: %+v", err)
	}
}

func TestObserverConcurrentCASPruneRegisterReportRevoke(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	a := mustRegister(t, s, verifier)
	b := mustRegister(t, s, verifier)
	// Independent store handles exercise SQLite's immediate transaction locking too.
	second, err := Open(t.Context(), s.dbPathForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, r := range []ObserverRegistration{a, b} {
		wg.Go(func() {
			target := s
			if i == 1 {
				target = second
			}
			_, err := target.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "bind", ExpectedVersion: owner.Version, RuntimeID: r.RuntimeID, ProjectID: p.ID})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			success++
		case observerIs(err, "version_conflict"):
			conflict++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("CAS was not atomic")
	}
	view := observerView(t, s)
	current := a
	candidate := b
	if view.Binding.RuntimeID == b.RuntimeID {
		current = b
		candidate = a
	}
	errs = make(chan error, 3)
	wg.Go(func() {
		_, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: view.Version, ExpectedRuntimeIDs: []string{candidate.RuntimeID}})
		errs <- err
	})
	wg.Go(func() { _, err := second.RegisterObserver(t.Context(), verifier, candidate); errs <- err })
	wg.Go(func() {
		_, err := second.ReportObserver(t.Context(), verifier, reportFor(candidate, 0, 1))
		errs <- err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !observerIs(err, "runtime_retired") {
			t.Fatal(err)
		}
	}
	_, err = s.RegisterObserver(t.Context(), verifier, candidate)
	expectObserverError(t, err, "runtime_retired")
	view = observerView(t, s)
	errs = make(chan error, 3)
	wg.Go(func() {
		_, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "revoke", ExpectedVersion: view.Version})
		errs <- err
	})
	wg.Go(func() { _, err := second.RegisterObserver(t.Context(), verifier, current); errs <- err })
	wg.Go(func() {
		_, err := second.ReportObserver(t.Context(), verifier, reportFor(current, view.Generation, 1))
		errs <- err
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !observerIs(err, "unauthorized") {
			t.Fatal(err)
		}
	}
	_, err = second.ReportObserver(t.Context(), verifier, reportFor(current, view.Generation, 2))
	expectObserverError(t, err, "unauthorized")
}

func TestObserverOwnerTransactionFaults(t *testing.T) {
	for table, query := range map[string]string{
		"observer_runtimes": "CREATE TRIGGER injected BEFORE INSERT ON observer_runtimes BEGIN SELECT RAISE(ABORT,'injected failure'); END",
		"observers":         "CREATE TRIGGER injected BEFORE UPDATE ON observers BEGIN SELECT RAISE(ABORT,'injected failure'); END",
		"observer_receipts": "CREATE TRIGGER injected BEFORE INSERT ON observer_receipts BEGIN SELECT RAISE(ABORT,'injected failure'); END",
		"observer_audit":    "CREATE TRIGGER injected BEFORE INSERT ON observer_audit BEGIN SELECT RAISE(ABORT,'injected failure'); END",
	} {
		t.Run(table, func(t *testing.T) {
			s := openTest(t)
			p, _ := seed(t, s)
			id, verifier, owner := mustObserver(t, s)
			r := mustRegister(t, s, verifier)
			before := observerView(t, s)
			if _, err := s.db.ExecContext(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			_, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "bind", ExpectedVersion: owner.Version, RuntimeID: r.RuntimeID, ProjectID: p.ID})
			if err == nil {
				t.Fatal("fault not observed")
			}
			if after := observerView(t, s); !reflect.DeepEqual(before, after) {
				t.Fatal("partial binding committed")
			}
		})
	}
}

func TestObserverValidation(t *testing.T) {
	s := openTest(t)
	id, verifier, owner := mustObserver(t, s)
	for _, mutate := range []func(*ObserverRegistration){func(r *ObserverRegistration) { r.Cwd = "bad\npath" }, func(r *ObserverRegistration) { r.Cwd = strings.Repeat("x", 4097) }, func(r *ObserverRegistration) { r.RuntimeID = "partial" }, func(r *ObserverRegistration) { r.Reason = "autobind" }, func(r *ObserverRegistration) { r.Model = strings.Repeat("x", 257) }, func(r *ObserverRegistration) { r.TerminalHint = &ObserverTerminalHint{} }} {
		r := registration()
		mutate(&r)
		_, err := s.RegisterObserver(t.Context(), verifier, r)
		expectObserverError(t, err, "invalid_request")
	}
	r := mustRegister(t, s, verifier)
	for _, mutate := range []func(*ObserverReport){func(r *ObserverReport) { r.ReportSeq = 0 }, func(r *ObserverReport) { r.Generation = -1 }, func(r *ObserverReport) { r.Activity = "done" }, func(r *ObserverReport) { r.ToolCount = 65536 }, func(r *ObserverReport) { r.Lifecycle = "crashed" }} {
		report := reportFor(r, 0, 1)
		mutate(&report)
		_, err := s.ReportObserver(t.Context(), verifier, report)
		expectObserverError(t, err, "invalid_request")
	}
	_, err := s.ObserverContext(t.Context(), observerHash("foreign"), r.RuntimeID)
	expectObserverError(t, err, "unauthorized")
	_, err = s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "bind", ExpectedVersion: owner.Version, RuntimeID: r.RuntimeID})
	expectObserverError(t, err, "invalid_request")
}
