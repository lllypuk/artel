package store

import (
	"encoding/json"
	"reflect"
	"strings"
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
		if r.Disposition != "revoked" || r.PruneEligible == nil || *r.PruneEligible != (r.Registration.RuntimeID == candidate.RuntimeID) {
			t.Fatal(r)
		}
	}
	m := ObserverOwnerMutation{ObserverID: id, Key: uuid.NewString(), Action: "prune", ExpectedVersion: owner.Version, ExpectedRuntimeIDs: []string{candidate.RuntimeID, bound.RuntimeID}}
	_, err := s.MutateObserver(t.Context(), m)
	expectObserverError(t, err, "version_conflict")
	owner = mustPrune(t, s, id, owner.Version, candidate.RuntimeID)
	view := observerView(t, s)
	if len(view.Runtimes) != 1 || view.Runtimes[0].Registration.RuntimeID != bound.RuntimeID || view.Runtimes[0].PruneEligible == nil || *view.Runtimes[0].PruneEligible || view.RetiredCount != 1 {
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
	// Golden SHA-256 values from the JSON encoding at base revision 152552a.
	const prefix = "00000000-0000-4000-8000-0000000000"
	const id = prefix + "01"
	s := openTest(t)
	if _, err := s.MutateObserver(t.Context(), ObserverOwnerMutation{ObserverID: id, Key: prefix + "10", Action: "provision", Label: "Fictional observer", Verifier: strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ action, keySuffix, hash string }{
		{"provision", "11", "3ec6ec53d0f91fe9ba17c5914f7a652855490106576ca2edb1262e4a736d0468"},
		{"bind", "12", "898ac92892d591ccf2e8c40d7a56716fbd0b76c78e0b8856c648ad09be2b01a2"},
		{"unbind", "13", "020b5dfb7cd00f22d6b85e60e82f173af32eb85e9cd2d89cd8dc1df8a2d07f74"},
		{"revoke", "14", "216ee597e3cd2d93b8f9f56308d10ad38902e88fdd6f0601f61f576f218bfafe"},
		{"prune", "15", "ea9a3d54d69e83086274539e3078f4971dff089d1797c021e682a14974bbcf76"},
	} {
		action := tc.action
		t.Run(action, func(t *testing.T) {
			m := ObserverOwnerMutation{ObserverID: id, Key: prefix + tc.keySuffix, Action: action, ExpectedVersion: 17}
			switch action {
			case "provision":
				m.Label, m.Verifier = "Receipt fixture", strings.Repeat("a", 64)
			case "bind":
				m.RuntimeID, m.ProjectID = prefix+"02", prefix+"03"
			case "unbind":
				m.Action = "bind"
			}
			result := ObserverOwnerResult{ObserverID: id, Version: 18, Generation: 2}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(t.Context(), "INSERT INTO observer_receipts(principal,key,request_hash,result) VALUES(?,?,?,?)", id, m.Key, tc.hash, string(data)); err != nil {
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
