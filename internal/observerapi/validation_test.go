package observerapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/store"
)

func TestObserverRejectsCaseFoldedAliases(t *testing.T) {
	f := newFixture(t)
	r := reg()
	r.Provider, r.Model = "provider", "model"
	r.TerminalHint = &store.ObserverTerminalHint{WindowID: uuid.NewString(), SessionID: uuid.NewString(), PaneID: uuid.NewString()}
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	registration, _ := json.Marshal(r)
	var registrationFields map[string]any
	_ = json.Unmarshal(registration, &registrationFields)
	for _, op := range []struct {
		name, method, path string
		observer           bool
		fields             map[string]any
	}{
		{"register", "POST", "/observer/v1/register", true, registrationFields},
		{"report", "PUT", "/observer/v1/report", true, map[string]any{"runtime_id": r.RuntimeID, "generation": 0, "report_seq": 1, "activity": "idle", "tool_count": 0, "lifecycle": "running", "provider": "provider", "model": "model"}},
		{"provision", "PUT", "/v1/observers/" + uuid.NewString(), false, map[string]any{"label": "Fixture", "verifier": strings.Repeat("ef", 32)}},
		{"binding", "PATCH", "/v1/observers/" + f.id + "/binding", false, map[string]any{"expected_version": 1, "runtime_id": nil, "project_id": nil}},
		{"revoke", "POST", "/v1/observers/" + f.id + "/revoke", false, map[string]any{"expected_version": 1}},
		{"prune", "POST", "/v1/observers/" + f.id + "/prune", false, map[string]any{"expected_version": 1, "expected_runtime_ids": []string{r.RuntimeID}}},
	} {
		for name, value := range op.fields {
			for _, aliasOnly := range []bool{false, true} {
				t.Run(op.name+"/"+name+"/"+map[bool]string{false: "duplicate", true: "alias-only"}[aliasOnly], func(t *testing.T) {
					fields := make(map[string]any, len(op.fields)+1)
					for k, v := range op.fields {
						fields[k] = v
					}
					if aliasOnly {
						delete(fields, name)
					}
					fields[strings.ToUpper(name)] = value
					token := f.ownerToken
					if op.observer {
						token = f.token
					}
					f.request(t, op.observer, op.method, op.path, token, uuid.NewString(), fields, 400)
				})
			}
		}
	}
	for _, name := range []string{"window_id", "session_id", "pane_id"} {
		for _, aliasOnly := range []bool{false, true} {
			t.Run("terminal_hint/"+name+"/"+map[bool]string{false: "duplicate", true: "alias-only"}[aliasOnly], func(t *testing.T) {
				var fields map[string]any
				_ = json.Unmarshal(registration, &fields)
				hint := fields["terminal_hint"].(map[string]any)
				hint[strings.ToUpper(name)] = hint[name]
				if aliasOnly {
					delete(hint, name)
				}
				f.request(t, true, "POST", "/observer/v1/register", f.token, "", fields, 400)
			})
		}
	}
	// A folded alias must not turn a stale CAS into an accepted mutation.
	f.request(t, false, "POST", "/v1/observers/"+f.id+"/prune", f.ownerToken, uuid.NewString(), `{"expected_version":0,"EXPECTED_VERSION":1,"expected_runtime_ids":["`+r.RuntimeID+`"]}`, 400)
	view, err := f.s.Observers(t.Context())
	if err != nil || len(view.Observers) != 1 || view.Observers[0].Version != 1 || view.Observers[0].Revoked {
		t.Fatal("invalid aliases changed owner state")
	}
}

func TestObserverRejectsNilIdentityBeforePersistence(t *testing.T) {
	f := newFixture(t)
	for _, field := range []string{"runtime_id", "pi_session_id", "window_id", "session_id", "pane_id"} {
		t.Run(field, func(t *testing.T) {
			r := reg()
			r.TerminalHint = &store.ObserverTerminalHint{WindowID: uuid.NewString(), SessionID: uuid.NewString(), PaneID: uuid.NewString()}
			data, _ := json.Marshal(r)
			var fields map[string]any
			_ = json.Unmarshal(data, &fields)
			if field == "runtime_id" || field == "pi_session_id" {
				fields[field] = uuid.Nil.String()
			} else {
				fields["terminal_hint"].(map[string]any)[field] = uuid.Nil.String()
			}
			f.request(t, true, "POST", "/observer/v1/register", f.token, "", fields, 400)
		})
	}
	f.request(t, true, "GET", "/observer/v1/context?runtime_id="+uuid.Nil.String(), f.token, "", nil, 400)
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", store.ObserverReport{RuntimeID: uuid.Nil.String(), ReportSeq: 1, Activity: "idle", Lifecycle: "running"}, 400)
	view, err := f.s.Observers(t.Context())
	if err != nil || len(view.Observers) != 1 || len(view.Observers[0].Runtimes) != 0 || view.Observers[0].IdentityCount != 0 {
		t.Fatal("nil identity persisted or consumed registry capacity")
	}
}
