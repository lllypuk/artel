package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/api"
	"github.com/lllypuk/artel/internal/domain"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

// deliveryHarness is a private temporary installation with one bound observer,
// one registered runtime and one seeded project/task pair.
type deliveryHarness struct {
	store      *store.Store
	handler    http.Handler
	server     *httptest.Server
	ownerPath  string
	ownerToken string
	observer   string
	runtime    store.ObserverRegistration
	project    domain.Project
	task       domain.Task
	version    int64
	socket     string
}

func newDeliveryHarness(t *testing.T) *deliveryHarness {
	t.Helper()
	base := t.TempDir()
	ownerPath := filepath.Join(base, "owner", "token")
	ownerToken, err := local.InitToken(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	h := &deliveryHarness{store: s, ownerPath: ownerPath, ownerToken: ownerToken, socket: filepath.Join(base, "observer", "observer.sock")}
	observerToken := sha256Token()
	if _, err = s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: uuid.NewString(), Key: uuid.NewString(), Action: "provision", Label: "Fictional observer", Verifier: observerToken}); err != nil {
		t.Fatal(err)
	}
	registry, err := s.Observers(t.Context())
	if err != nil || len(registry.Observers) != 1 {
		t.Fatal(err)
	}
	h.observer = registry.Observers[0].ObserverID
	h.runtime = store.ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1.5.2", BridgeVersion: "0.1.0", Cwd: "/fictional/project"}
	if _, err = s.RegisterObserver(t.Context(), observerToken, h.runtime); err != nil {
		t.Fatal(err)
	}
	h.project, h.task = seedDeliveryProject(t, s)
	bound, err := s.MutateObserver(t.Context(), store.ObserverOwnerMutation{ObserverID: h.observer, Key: uuid.NewString(), Action: "bind", ExpectedVersion: registry.Observers[0].Version, RuntimeID: h.runtime.RuntimeID, ProjectID: h.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.version = bound.Version
	handler := api.New(t.Context(), s)
	h.handler = handler
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+ownerToken {
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(h.server.Close)
	return h
}

func sha256Token() string {
	sum := sha256.Sum256([]byte(uuid.NewString()))
	return hex.EncodeToString(sum[:])
}

func seedDeliveryProject(t *testing.T, s *store.Store) (domain.Project, domain.Task) {
	t.Helper()
	p := domain.Project{ID: uuid.NewString(), RootPath: "/delivery/" + uuid.NewString(), Name: "test", Version: 1, QueueVersion: 1}
	task := domain.Task{ID: uuid.NewString(), ProjectID: p.ID, Title: "test", State: domain.Backlog, Version: 1}
	_, err := s.Apply(t.Context(), store.Mutation{Key: uuid.NewString()}, func(snap *store.Snapshot) (store.Result, *store.Event, error) {
		snap.Projects = append(snap.Projects, p)
		snap.Tasks = append(snap.Tasks, task)
		snap.Queues[p.ID] = []string{}
		return store.Result{Status: 201, Body: []byte(`{}`)}, &store.Event{Action: "seed", ProjectID: p.ID, TaskID: task.ID, Timestamp: "2026-11-19T00:00:00Z"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return p, task
}

func (h *deliveryHarness) call(t *testing.T, want int, args ...string) (string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	args = append(args, "--endpoint", h.server.URL, "--token-file", h.ownerPath)
	if code := run(args, &out, &diagnostic); code != want {
		t.Fatalf("exit %d want %d: %s", code, want, diagnostic.String())
	}
	return out.String(), diagnostic.String()
}

func (h *deliveryHarness) enableArgs(output string) []string {
	return []string{"delivery", "enable", "--id", h.observer, "--expected-version", fmtInt(h.version),
		"--runtime-id", h.runtime.RuntimeID, "--project-id", h.project.ID,
		"--pi-session-id", h.runtime.PiSessionID, "--observer-socket", h.socket, "--output", output}
}

func fmtInt(v int64) string { return strconv.FormatInt(v, 10) }

func readPending(t *testing.T, path string) deliveryPending {
	t.Helper()
	raw, err := local.ReadPrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var p deliveryPending
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertNoSecret(t *testing.T, outputs []string, tokenPath string) {
	t.Helper()
	token, err := local.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range outputs {
		if strings.Contains(output, token) {
			t.Fatal("delivery token leaked into CLI output")
		}
	}
}

func assertPrivateModes(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 || !dir.IsDir() {
		t.Fatalf("dir mode %o", dir.Mode().Perm())
	}
}

func TestDeliveryEnablePublishesOnlyAfterConfirmation(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	var mu sync.Mutex
	var puts []struct {
		key  string
		body []byte
	}
	var sawPendingBeforePut bool
	inner := h.server.Config.Handler
	h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/delivery") {
			mu.Lock()
			body := mustRead(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			puts = append(puts, struct {
				key  string
				body []byte
			}{r.Header.Get("Idempotency-Key"), body})
			_, statErr := os.Lstat(filepath.Join(filepath.Dir(output), "delivery"))
			sawPendingBeforePut = statErr == nil
			mu.Unlock()
		}
		inner.ServeHTTP(w, r)
	})
	stdout, _ := h.call(t, 0, h.enableArgs(output)...)
	if !sawPendingBeforePut {
		t.Fatal("pending record not durable before the enable request")
	}
	if len(puts) != 1 {
		t.Fatalf("PUT count %d", len(puts))
	}
	var request deliveryEnableRequest
	if err := json.Unmarshal(puts[0].body, &request); err != nil || !validCanonicalID(request.DeliveryBindingID) || !validCanonicalID(request.JournalID) {
		t.Fatal("invalid enable request body")
	}
	if bytes.Contains(puts[0].body, []byte("allow_owner_questions")) {
		t.Fatal("4a enable bytes changed without an explicit 4b opt-in")
	}
	p := readPending(t, filepath.Join(filepath.Dir(output), "delivery", request.DeliveryBindingID, "pending.json"))
	assertConfirmedArtifacts(t, output, p, stdout)
	if p.IdempotencyKey != puts[0].key {
		t.Fatalf("pending key mismatch: %+v", p)
	}
	binding, err := h.store.DeliveryBindingStatus(t.Context(), h.observer, p.DeliveryBindingID)
	if err != nil || binding.Status != "pending" {
		t.Fatalf("server binding: %+v %v", binding, err)
	}
}

func assertConfirmedArtifacts(t *testing.T, output string, p deliveryPending, stdout string) {
	t.Helper()
	if p.Status != "confirmed" || !validCanonicalID(p.DeliveryBindingID) || !validCanonicalID(p.JournalID) {
		t.Fatalf("pending record mismatch: %+v", p)
	}
	assertPrivateModes(t, deliveryPendingPath(p))
	assertPrivateModes(t, p.TokenFile)
	assertPrivateModes(t, p.JournalFile)
	assertPrivateModes(t, output)
	assertNoSecret(t, []string{stdout}, p.TokenFile)
	raw, err := local.ReadPrivateFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var config deliveryWriterConfig
	if err := json.Unmarshal(raw, &config); err != nil || config.Status != "confirmed" || config.DeliveryBindingID != p.DeliveryBindingID || config.TokenFile != p.TokenFile {
		t.Fatalf("writer config mismatch: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"verifier"`)) || bytes.Contains(raw, []byte(p.Verifier)) {
		t.Fatal("secret material in writer config")
	}
}

func mustRead(r io.Reader) []byte {
	b, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return b
}

func TestDeliveryEnableLostAckResumeSameKey(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	var drop atomic.Bool
	var mu sync.Mutex
	var puts []struct {
		key  string
		body []byte
	}
	inner := h.server.Config.Handler
	h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/delivery") {
			mu.Lock()
			body := mustRead(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(body))
			puts = append(puts, struct {
				key  string
				body []byte
			}{r.Header.Get("Idempotency-Key"), body})
			mu.Unlock()
			if drop.Load() {
				hijackClose(t, w, inner, r)
				return
			}
		}
		inner.ServeHTTP(w, r)
	})
	drop.Store(true)
	stdout, _ := h.call(t, 1, h.enableArgs(output)...)
	if stdout != "" {
		t.Fatal("output on failed enable")
	}
	// The partial bootstrap is durable but grants nothing: no confirmed config,
	// no writer activation artifacts beyond the pending record and the secret.
	entries, err := os.ReadDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "delivery.json" {
			t.Fatal("confirmed config published without server confirmation")
		}
	}
	mu.Lock()
	if len(puts) < 1 {
		t.Fatal("no enable request recorded")
	}
	firstKey, firstBody := puts[0].key, puts[0].body
	for _, put := range puts {
		if put.key != firstKey || !bytes.Equal(put.body, firstBody) {
			t.Fatal("retry changed the original key or request body")
		}
	}
	pendingPath := filepath.Join(filepath.Dir(output), "delivery", mustBindingID(t, firstBody), "pending.json")
	p := readPending(t, pendingPath)
	if p.Status != "pending" {
		t.Fatalf("pending status: %s", p.Status)
	}
	mu.Unlock()
	drop.Store(false)
	resumeOut, _ := h.call(t, 0, "delivery", "enable", "--resume", pendingPath)
	mu.Lock()
	if len(puts) == 0 || puts[len(puts)-1].key != firstKey || !bytes.Equal(puts[len(puts)-1].body, firstBody) {
		t.Fatal("resume changed the original key or request body")
	}
	mu.Unlock()
	assertNoSecret(t, []string{resumeOut}, p.TokenFile)
	confirmed, err := local.ReadPrivateFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(confirmed, []byte(p.DeliveryBindingID)) {
		t.Fatal("confirmed config does not name the resumed binding")
	}
}

func TestDeliveryOwnerQuestionsOptInAndConfirmedResume(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	args := append(h.enableArgs(output), "--allow-owner-questions")
	h.call(t, 0, args...)
	pendingPath := filepath.Join(filepath.Dir(output), "delivery", onlyBindingDir(t, output), "pending.json")
	p := readPending(t, pendingPath)
	if !p.Request.AllowOwnerQuestions {
		t.Fatal("durable resume record lost the owner-questions opt-in")
	}
	raw, err := local.ReadPrivateFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var config deliveryWriterConfig
	if json.Unmarshal(raw, &config) != nil || !config.AllowOwnerQuestions {
		t.Fatal("confirmed writer config lost the owner-questions opt-in")
	}
	binding, err := h.store.DeliveryBindingStatus(t.Context(), h.observer, p.DeliveryBindingID)
	if err != nil || !binding.AllowOwnerQuestions {
		t.Fatalf("server binding lost opt-in: %+v %v", binding, err)
	}
	before, err := local.ReadPrivateFile(output)
	if err != nil {
		t.Fatal(err)
	}
	h.call(t, 0, "delivery", "enable", "--resume", pendingPath)
	after, err := local.ReadPrivateFile(output)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("confirmed resume rewrote the writer config")
	}
}

func TestDeliveryOwnerQuestionsRejectedEnablePublishesNothing(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	inner := h.server.Config.Handler
	h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/delivery") {
			body := mustRead(r.Body)
			if !bytes.Contains(body, []byte(`"allow_owner_questions":true`)) {
				t.Error("opt-in field missing from initial enable request")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			return
		}
		inner.ServeHTTP(w, r)
	})
	h.call(t, 1, append(h.enableArgs(output), "--allow-owner-questions")...)
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected opt-in published a confirmed config")
	}
	bindingID := onlyBindingDir(t, output)
	if _, err := h.store.DeliveryBindingStatus(t.Context(), h.observer, bindingID); err == nil {
		t.Fatal("rejected opt-in left a server grant")
	}
	p := readPending(t, filepath.Join(filepath.Dir(output), "delivery", bindingID, "pending.json"))
	if _, err := os.Lstat(p.JournalFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("rejected opt-in created a delivery journal")
	}
}

func mustBindingID(t *testing.T, body []byte) string {
	t.Helper()
	var request deliveryEnableRequest
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	return request.DeliveryBindingID
}

func TestDeliveryRevokeClearsLocalState(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	h.call(t, 0, h.enableArgs(output)...)
	p := readPending(t, filepath.Join(filepath.Dir(output), "delivery", onlyBindingDir(t, output), "pending.json"))
	secret, err := os.ReadFile(p.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _ := h.call(t, 0, "delivery", "revoke", "--id", h.observer, "--binding-id", p.DeliveryBindingID, "--output", output)
	if strings.Contains(stdout, string(bytes.TrimSpace(secret))) {
		t.Fatal("delivery token leaked into CLI output")
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed config not cleared")
	}
	for _, path := range []string{p.TokenFile, filepath.Join(filepath.Dir(output), "delivery", p.DeliveryBindingID, "pending.json")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("local state not cleared: %s", path)
		}
	}
	// The attempt journal is retained as history and stays integrity-checked.
	assertPrivateModes(t, p.JournalFile)
	binding, err := h.store.DeliveryBindingStatus(t.Context(), h.observer, p.DeliveryBindingID)
	if err != nil || binding.Status != "revoked" {
		t.Fatalf("binding not revoked: %+v %v", binding, err)
	}
}

func otherBindingDir(t *testing.T, output, exclude string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(output), "delivery"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != exclude {
			return entry.Name()
		}
	}
	t.Fatal("no other binding directory")
	return ""
}

func onlyBindingDir(t *testing.T, output string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(output), "delivery"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("binding dirs: %v", err)
	}
	return entries[0].Name()
}

// Lost ACK -> revoke -> new binding -> the old resume must not touch the new
// binding's confirmed files or leave stale authorization artifacts behind.
func TestDeliveryOldResumeNeverTouchesNewBinding(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	var drop atomic.Bool
	inner := h.server.Config.Handler
	h.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if drop.Load() && r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/delivery") {
			hijackClose(t, w, inner, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
	drop.Store(true)
	h.call(t, 1, h.enableArgs(output)...)
	drop.Store(false)
	oldBinding := onlyBindingDir(t, output)
	oldPending := filepath.Join(filepath.Dir(output), "delivery", oldBinding, "pending.json")
	h.call(t, 0, "delivery", "revoke", "--id", h.observer, "--binding-id", oldBinding)
	h.call(t, 0, h.enableArgs(output)...)
	newBinding := otherBindingDir(t, output, oldBinding)
	if newBinding == oldBinding {
		t.Fatal("new binding reused the revoked binding ID")
	}
	newConfig, err := local.ReadPrivateFile(output)
	if err != nil {
		t.Fatal(err)
	}
	before, err := local.ReadPrivateFile(filepath.Join(filepath.Dir(output), "delivery", newBinding, "pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	h.call(t, 1, "delivery", "enable", "--resume", oldPending)
	if after, err := local.ReadPrivateFile(output); err != nil || !bytes.Equal(after, newConfig) {
		t.Fatal("old resume touched the new binding's confirmed config")
	}
	if after, err := local.ReadPrivateFile(filepath.Join(filepath.Dir(output), "delivery", newBinding, "pending.json")); err != nil || !bytes.Equal(after, before) {
		t.Fatal("old resume touched the new binding's pending record")
	}
	if _, err := h.store.DeliveryBindingStatus(t.Context(), h.observer, newBinding); err != nil {
		t.Fatal("new binding affected by old resume")
	}
}

func TestDeliveryEnableRejectsMismatchedSelection(t *testing.T) {
	h := newDeliveryHarness(t)
	output := filepath.Join(t.TempDir(), "private", "delivery.json")
	args := h.enableArgs(output)
	for i := range args {
		if args[i] == h.runtime.PiSessionID {
			args[i] = uuid.NewString()
		}
		if args[i] == fmtInt(h.version) {
			args[i] = fmtInt(h.version + 1)
		}
	}
	h.call(t, 1, args...)
	// Client-side rejection precedes any private directory creation: the
	// parent must not even exist, so no artifact could have been written.
	if _, err := os.Stat(filepath.Dir(output)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private directory created for a mismatched selection: %v", err)
	}
}

func hijackClose(t *testing.T, w http.ResponseWriter, inner http.Handler, r *http.Request) {
	t.Helper()
	recorder := httptest.NewRecorder()
	inner.ServeHTTP(recorder, r)
	if recorder.Code != 200 {
		t.Error("inner rejected:", recorder.Code)
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Error("missing hijacker")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Error(err)
		return
	}
	_ = conn.Close()
}
