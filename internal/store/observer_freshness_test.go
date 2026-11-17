package store

import (
	"testing"
	"time"
)

func TestObserverFreshnessIsIndependentOfOtherCandidatesAndOwnerActions(t *testing.T) {
	s := openTest(t)
	p, _ := seed(t, s)
	id, verifier, owner := mustObserver(t, s)
	now := time.Now()
	s.observerClock = func() time.Time { return now }
	a := mustRegister(t, s, verifier)
	b := mustRegister(t, s, verifier)
	mustReport(t, s, verifier, reportFor(a, 0, 1))
	mustReport(t, s, verifier, reportFor(b, 0, 1))
	owner = mustOwner(t, s, id, "bind", owner.Version, a.RuntimeID, p.ID)
	view := observerView(t, s)
	for _, r := range view.Runtimes {
		switch r.Registration.RuntimeID {
		case a.RuntimeID:
			if r.Freshness != "unknown" {
				t.Fatal("binding reused old generation freshness")
			}
		case b.RuntimeID:
			if r.Freshness != "fresh" {
				t.Fatal("unrelated binding invalidated candidate")
			}
		}
	}
	now = now.Add(19 * time.Second)
	mustReport(t, s, verifier, reportFor(a, owner.Generation, 1))
	owner = mustOwner(t, s, id, "prune", owner.Version, "", "")
	if owner.PrunedCount != 1 {
		t.Fatal(owner)
	}
	now = now.Add(19 * time.Second)
	if observerView(t, s).Runtimes[0].Freshness != "fresh" {
		t.Fatal("prune invalidated bound heartbeat")
	}
	now = now.Add(time.Second)
	if observerView(t, s).Runtimes[0].Freshness != "stale" {
		t.Fatal("freshness exceeded 20 seconds")
	}
	if len(s.observerSeen) != 1 {
		t.Fatal("pruned telemetry remained in cache")
	}
}
