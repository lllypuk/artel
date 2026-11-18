package observerapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/api"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/observerapi"
	"github.com/lllypuk/artel/internal/store"
)

type fixture struct {
	s                     *store.Store
	owner, observer       http.Handler
	token, ownerToken, id string
	contract              *openapi3.T
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, e := store.Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	f := &fixture{s: s, token: strings.Repeat("ab", 32), ownerToken: strings.Repeat("cd", 32), id: uuid.NewString()}
	f.owner = api.Protect(f.ownerToken, "127.0.0.1:7331", api.New(t.Context(), s))
	f.observer = observerapi.New(s)
	f.contract, e = openapi3.NewLoader().LoadFromFile("../../api/observer-openapi.yaml")
	if e != nil {
		t.Fatal(e)
	}
	f.request(t, false, "PUT", "/v1/observers/"+f.id, f.ownerToken, uuid.NewString(), local.ObserverProvision{Label: "Fictional", Verifier: local.TokenVerifier(f.token)}, 200)
	return f
}
func (f *fixture) request(t *testing.T, observer bool, method, path, token, key string, body any, status int) []byte {
	t.Helper()
	var b []byte
	if raw, ok := body.(string); ok {
		b = []byte(raw)
	} else if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Host = "127.0.0.1:7331"
	handler := f.owner
	if observer {
		r.Host = "artel-observer"
		handler = f.observer
	}
	r.Header.Set("Authorization", "Bearer "+token)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	if strings.Contains(w.Body.String(), f.token) || strings.Contains(w.Body.String(), local.TokenVerifier(f.token)) || strings.Contains(w.Body.String(), f.ownerToken) {
		t.Fatal("credential in response")
	}
	{
		schema := "Error"
		if !observer {
			schema = "OwnerError"
		}
		if status == 200 {
			switch {
			case path == "/v1/observers":
				schema = "Projection"
			case strings.Contains(path, "/register") || strings.Contains(path, "/context"):
				schema = "Context"
			case strings.Contains(path, "/report"):
				schema = "ReportResult"
			default:
				schema = "OwnerResult"
			}
		}
		var value any
		if e := json.Unmarshal(w.Body.Bytes(), &value); e != nil {
			t.Fatal(e)
		}
		if e := f.contract.Components.Schemas[schema].Value.VisitJSON(value); e != nil {
			t.Fatalf("%s response: %v: %s", schema, e, w.Body.String())
		}
	}
	return w.Body.Bytes()
}
func reg() store.ObserverRegistration {
	return store.ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1.5.2", BridgeVersion: "0.1.0", Cwd: "/fictional"}
}
func TestObserverAuthenticationMatrix(t *testing.T) {
	f := newFixture(t)
	r := reg()
	for _, token := range []string{"", f.ownerToken, strings.Repeat("ef", 32)} {
		f.request(t, true, "POST", "/observer/v1/register", token, "", r, 401)
		f.request(t, true, "GET", "/observer/v1/context?runtime_id="+r.RuntimeID, token, "", nil, 401)
		f.request(t, true, "PUT", "/observer/v1/report", token, "", store.ObserverReport{RuntimeID: r.RuntimeID, ReportSeq: 1, Activity: "idle", Lifecycle: "running"}, 401)
	}
	for _, path := range []string{"/v1/observers", "/v1/snapshot", "/v1/events", "/v1/stream"} {
		f.request(t, false, "GET", path, f.token, "", nil, 401)
	}
	for _, suffix := range []string{"/revoke", "/prune", "/binding", ""} {
		method := "POST"
		if suffix == "/binding" {
			method = "PATCH"
		}
		if suffix == "" {
			method = "PUT"
		}
		f.request(t, false, method, "/v1/observers/"+f.id+suffix, f.token, uuid.NewString(), `{}`, 401)
	}
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	f.request(t, true, "GET", "/observer/v1/context?runtime_id="+r.RuntimeID, f.token, "", nil, 200)
	f.request(t, false, "POST", "/v1/observers/"+f.id+"/revoke", f.ownerToken, uuid.NewString(), `{"expected_version":1}`, 200)
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 401)
	f.request(t, true, "GET", "/observer/v1/context?runtime_id="+r.RuntimeID, f.token, "", nil, 401)
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", store.ObserverReport{RuntimeID: r.RuntimeID, ReportSeq: 1, Activity: "idle", Lifecycle: "running"}, 401)
}
func TestObserverStrictBoundary(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{`null`, `{}`, `{"runtime_id":"x","unknown":"secret"}`, `{"runtime_id":"x","runtime_id":"y"}`, `{} {}`, strings.Repeat("x", 16385)} {
		status := 400
		if len(body) > 16384 {
			status = 413
		}
		f.request(t, true, "POST", "/observer/v1/register", f.token, "", body, status)
	}
	registration, _ := json.Marshal(reg())
	for _, field := range []string{"provider", "model"} {
		body := string(registration[:len(registration)-1]) + `,"` + field + `":""}`
		f.request(t, true, "POST", "/observer/v1/register", f.token, "", body, 400)
	}
	for _, mode := range []string{"host", "origin", "empty-origin", "absolute", "content-type", "query"} {
		t.Run(mode, func(t *testing.T) {
			data, _ := json.Marshal(reg())
			r := httptest.NewRequest("POST", "/observer/v1/register", bytes.NewReader(data))
			r.Host = "artel-observer"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+f.token)
			switch mode {
			case "host":
				r.Host = "localhost"
			case "origin":
				r.Header.Set("Origin", "http://evil")
			case "empty-origin":
				r.Header.Set("Origin", "")
			case "absolute":
				r.URL.Scheme = "http"
				r.URL.Host = "artel-observer"
			case "content-type":
				r.Header.Del("Content-Type")
			case "query":
				r.URL.RawQuery = "path=/tmp/private"
			}
			w := httptest.NewRecorder()
			f.observer.ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("boundary accepted: %d", w.Code)
			}
		})
	}
	runtime := reg()
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", runtime, 200)
	for _, fields := range []string{`"expected_version":null`, `"expected_version":1,"unexpected":true`, `"expected_version":1,"expected_version":1`} {
		body := `{"expected_runtime_ids":["` + runtime.RuntimeID + `"],` + fields + `}`
		f.request(t, false, "POST", "/v1/observers/"+f.id+"/prune", f.ownerToken, uuid.NewString(), body, 400)
	}
	f.request(t, true, "GET", "/observer/v1/context?runtime_id=not-uuid", f.token, "", nil, 400)
	f.request(t, true, "GET", "/observer/v1/context?runtime_id="+uuid.NewString()+"&runtime_id="+uuid.NewString(), f.token, "", nil, 400)
	for _, path := range []string{"/v1/snapshot", "/observer//v1/context", "/observer/v1/../context", "/observer/v1/register", "/observer/v1/files?path=/tmp/fictional"} {
		f.request(t, true, "GET", path, f.token, "", nil, 404)
	}
	f.request(t, true, "HEAD", "/observer/v1/context", f.token, "", nil, 404)
}
func TestObserverReplayForeignRuntimeAndRate(t *testing.T) {
	f := newFixture(t)
	r := reg()
	first := f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	repeated := f.request(t, true, "POST", "/observer/v1/register", f.token, "", r, 200)
	if !bytes.Equal(first, repeated) {
		t.Fatal("registration response not replayed")
	}
	changed := r
	changed.Cwd = "/changed"
	f.request(t, true, "POST", "/observer/v1/register", f.token, "", changed, 409)
	report := store.ObserverReport{RuntimeID: r.RuntimeID, ReportSeq: 1, Activity: "idle", Lifecycle: "running"}
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 200)
	duplicate := f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 200)
	if !bytes.Contains(duplicate, []byte(`"result":"duplicate"`)) {
		t.Fatal("lost report response not recoverable")
	}
	report.Activity = "working"
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 409)
	report.ReportSeq = 2
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 200)
	report.ReportSeq = 3
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 429)
	foreign := reg()
	otherToken := strings.Repeat("ef", 32)
	_, err := f.s.MutateObserver(t.Context(), store.ObserverOwnerMutation{Action: "provision", Key: uuid.NewString(), ObserverID: uuid.NewString(), Label: "Other", Verifier: local.TokenVerifier(otherToken)})
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, true, "POST", "/observer/v1/register", otherToken, "", foreign, 200)
	report.RuntimeID = foreign.RuntimeID
	f.request(t, true, "PUT", "/observer/v1/report", f.token, "", report, 404)
}
