package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func mustPrune(t *testing.T, s *Store, id string, version int64, ids ...string) ObserverOwnerResult {
	t.Helper()
	got, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: version, ExpectedRuntimeIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestObserverPruneSerialization(t *testing.T) {
	for _, name := range []string{"register_first", "prune_first"} {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			id, verifier, owner := mustObserver(t, s)
			a := mustRegister(t, s, verifier)
			second, err := Open(t.Context(), s.dbPathForTest(t))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = second.Close() }()
			b := registration()
			register := func() {
				if _, err := second.RegisterObserver(t.Context(), verifier, b); err != nil {
					t.Fatal(err)
				}
			}
			m := ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: owner.Version, ExpectedRuntimeIDs: []string{a.RuntimeID}}
			if name == "register_first" {
				register()
				before := observerView(t, s)
				if before.Version != owner.Version {
					t.Fatal("registration changed CAS version")
				}
				_, err = s.MutateObserver(t.Context(), m)
				expectObserverError(t, err, "version_conflict")
				if !reflect.DeepEqual(before, observerView(t, s)) || countRows(t, s, "observer_audit") != 1 || countRows(t, s, "observer_receipts") != 1 {
					t.Fatal("rejected prune changed registry, retirement markers or owner history")
				}
				for _, r := range []ObserverRegistration{a, b} {
					if _, err := s.RegisterObserver(t.Context(), verifier, r); err != nil {
						t.Fatal(err)
					}
				}
				m.ExpectedRuntimeIDs = append(m.ExpectedRuntimeIDs, b.RuntimeID)
				if _, err = s.MutateObserver(t.Context(), m); err != nil {
					t.Fatal(err)
				}
				return
			}
			first, err := s.MutateObserver(t.Context(), m)
			if err != nil || first.PrunedCount != 1 {
				t.Fatalf("prune: %+v %v", first, err)
			}
			register()
			retry, err := s.MutateObserver(t.Context(), m)
			if err != nil || retry != first {
				t.Fatalf("retry: %+v %v", retry, err)
			}
			after := observerView(t, s)
			if len(after.Runtimes) != 1 || after.Runtimes[0].Registration.RuntimeID != b.RuntimeID || after.RetiredCount != 1 || after.Version != first.Version {
				t.Fatal(after)
			}
		})
	}
}

func TestObserverPruneRevokedProjection(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	bound := mustRegister(t, s, verifier)
	candidate := mustRegister(t, s, verifier)
	owner = mustOwner(t, s, id, "bind", owner.Version, bound.RuntimeID, p.ID)
	stopped := reportFor(bound, owner.Generation, 1)
	stopped.Lifecycle = "stopped"
	mustReport(t, s, verifier, stopped)
	owner = mustOwner(t, s, id, "revoke", owner.Version, "", "")
	for _, r := range observerView(t, s).Runtimes {
		if r.Disposition != "revoked" || r.PruneEligible != (r.Registration.RuntimeID == candidate.RuntimeID) {
			t.Fatal(r)
		}
	}
	m := ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: owner.Version, ExpectedRuntimeIDs: []string{candidate.RuntimeID, bound.RuntimeID}}
	_, err := s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "version_conflict")
	owner = mustPrune(t, s, id, owner.Version, candidate.RuntimeID)
	view := observerView(t, s)
	if len(view.Runtimes) != 1 || view.Runtimes[0].Registration.RuntimeID != bound.RuntimeID || view.Runtimes[0].PruneEligible || view.RetiredCount != 1 {
		t.Fatal(view)
	}
	m.ExpectedVersion = owner.Version
	m.ExpectedRuntimeIDs = []string{}
	_, err = s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "invalid_request")
	m.ExpectedRuntimeIDs = []string{candidate.RuntimeID}
	_, err = s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "version_conflict")
}

func TestObserverPreUpgradeReceipts(t *testing.T) {
	// This DTO is the pre-upgrade hash format, including order and empty fields.
	type legacyMutation struct {
		ObserverID, Key, Action               string
		ExpectedVersion                       int64
		Label, Verifier, RuntimeID, ProjectID string
	}
	s := openTest(t)
	id, _, _ := mustObserver(t, s)
	for _, action := range []string{"provision", "bind", "unbind", "revoke", "prune"} {
		t.Run(action, func(t *testing.T) {
			m := ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: action, ExpectedVersion: 17}
			switch action {
			case "provision":
				m.Label, m.Verifier = "Receipt fixture", observerHash("fixture")
			case "bind":
				m.RuntimeID, m.ProjectID = uuid.NewString(), uuid.NewString()
			case "unbind":
				m.Action = "bind"
			}
			old := legacyMutation{m.ObserverID, m.Key, m.Action, m.ExpectedVersion, m.Label, m.Verifier, m.RuntimeID, m.ProjectID}
			result := ObserverOwnerResult{ObserverID: id, Version: 18, Generation: 2}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), "INSERT INTO observer_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", id, m.Key, observerHash(old), string(data)); err != nil {
				t.Fatal(err)
			}
			got, err := s.MutateObserver(t.Context(), m)
			if action == "prune" {
				expectObserverError(t, err, "invalid_request")
				m.ExpectedRuntimeIDs = []string{uuid.NewString()}
				_, err = s.MutateObserver(t.Context(), m)
				expectObserverError(t, err, "idempotency_conflict")
			} else if err != nil || got != result {
				t.Fatalf("legacy replay: %+v %v", got, err)
			}
		})
	}
	if countRows(t, s, "observer_audit") != 1 || observerView(t, s).Version != 1 {
		t.Fatal("receipt replay wrote owner action")
	}
}
