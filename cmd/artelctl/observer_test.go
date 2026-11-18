package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/api"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

func TestObserverCLIProcessProvisionResume(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "private", "observer.json")
	tokenPath := filepath.Join(base, "owner", "token")
	ownerToken, e := local.InitToken(tokenPath)
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	handler := api.New(t.Context(), s)
	var drop atomic.Bool
	drop.Store(true)
	var mu sync.Mutex
	var keys []string
	var payloads []local.ObserverProvision
	server := httptest.NewServer(lostProvisionHandler(t, handler, config, ownerToken, &drop, func(key string, payload local.ObserverProvision) {
		mu.Lock()
		defer mu.Unlock()
		keys = append(keys, key)
		payloads = append(payloads, payload)
	}))
	defer server.Close()
	binary := filepath.Join(base, "artelctl")
	// #nosec G204 -- Build this CLI into a private test path, without shell.
	if b, e := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatalf("build: %v %s", e, b)
	}
	call := func(expectSuccess bool, args ...string) []byte {
		t.Helper()
		args = append(args, "--endpoint", server.URL, "--token-file", tokenPath)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		// #nosec G204 -- Test-built CLI, private fixtures and ephemeral API only.
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = []string{"HOME=" + base}
		b, e := cmd.CombinedOutput()
		if (e == nil) != expectSuccess {
			t.Fatalf("CLI success=%v error=%v output=%s", expectSuccess, e, b)
		}
		if strings.Contains(string(b), ownerToken) {
			t.Fatal("owner token in output")
		}
		return b
	}
	call(false, "observer", "provision", "--name", "Fictional", "--observer-socket", "/tmp/fictional-artel.sock", "--output", config)
	drop.Store(false)
	mu.Lock()
	initialAttempts := len(keys)
	mu.Unlock()
	pending, e := local.ReadObserverConfig(config, false)
	if e != nil || pending.Status != "pending" {
		t.Fatalf("pending artifact: %+v %v", pending, e)
	}
	if _, e = local.ReadObserverConfig(config, true); e == nil {
		t.Fatal("pending config accepted for runtime")
	}
	registry, e := s.Observers(t.Context())
	if e != nil || len(registry.Observers) != 1 {
		t.Fatal("lost response duplicated/lost principal", e)
	}
	call(false, "observer", "provision", "--resume", config, "--name", "Changed")
	second := call(true, "observer", "provision", "--resume", config)
	confirmed, e := local.ReadObserverConfig(config, true)
	if e != nil || confirmed.Status != "confirmed" {
		t.Fatal("config not confirmed", e)
	}
	third := call(true, "observer", "provision", "--resume", config)
	if !bytes.Equal(second, third) {
		t.Fatal("confirmed resume response changed")
	}
	call(false, "observer", "provision", "--name", "Changed", "--observer-socket", "/tmp/fictional-artel.sock", "--output", config)
	b := call(true, "observer", "list", "--json")
	token, e := local.ReadToken(confirmed.TokenFile)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(b, []byte(token)) || bytes.Contains(b, []byte(confirmed.Payload.Verifier)) {
		t.Fatal("credential exposed")
	}
	mu.Lock()
	defer mu.Unlock()
	assertCanonicalProvision(t, keys, payloads, pending, initialAttempts+2)
}

func TestObserverCLIPruneDoesNotDowngrade(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "owner", "token")
	if _, err := local.InitToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	id, runtime := uuid.NewString(), uuid.NewString()
	var requests, exact, legacyAccepted atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v1/observers/"+id+"/prune" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var legacy struct {
			ExpectedVersion int `json:"expected_version"`
		}
		var payload struct {
			ExpectedVersion int      `json:"expected_version"`
			RuntimeIDs      []string `json:"expected_runtime_ids"`
		}
		var raw json.RawMessage
		if json.NewDecoder(r.Body).Decode(&raw) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if json.Unmarshal(raw, &payload) == nil && payload.ExpectedVersion == 3 && len(payload.RuntimeIDs) == 1 && payload.RuntimeIDs[0] == runtime {
			exact.Add(1)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&legacy) != nil || legacy.ExpectedVersion != 3 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// An older daemon would accept this unsafe downgrade.
		legacyAccepted.Add(1)
		_, _ = w.Write([]byte(`{"version":4,"pruned_count":1}`))
	}))
	defer server.Close()
	args := []string{"observer", "prune", "--endpoint", server.URL, "--token-file", tokenPath, "--id", id, "--expected-version", "3"}
	var out, diagnostic bytes.Buffer
	if code := run(args, &out, &diagnostic); code != 2 || requests.Load() != 0 {
		t.Fatalf("version-only invocation: exit=%d requests=%d", code, requests.Load())
	}
	diagnostic.Reset()
	if code := run(append(args, "--runtime-id", runtime), &out, &diagnostic); code != 1 || !strings.Contains(diagnostic.String(), "API operation rejected") || out.Len() != 0 {
		t.Fatalf("legacy rejection: exit=%d diagnostic=%s stdout=%s", code, diagnostic.String(), out.String())
	}
	if requests.Load() != 1 || exact.Load() != 1 || legacyAccepted.Load() != 0 {
		t.Fatalf("requests=%d exact=%d legacy accepted=%d", requests.Load(), exact.Load(), legacyAccepted.Load())
	}
}

func TestObserverCLIPruneDiagnosticsAndLegacyList(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "owner", "token")
	if _, err := local.InitToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	id, runtime := uuid.NewString(), uuid.NewString()
	for _, reply := range []struct{ code, diagnostic string }{
		{"version_conflict", "refresh the observer list and confirm the runtime IDs again"},
		{"idempotency_conflict", "API operation rejected"},
	} {
		t.Run(reply.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"observers":[{"runtimes":[{}, {"prune_eligible":false}]}]}`))
					return
				}
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": reply.code, "message": "not for the terminal"})
			}))
			defer server.Close()
			args := []string{"--endpoint", server.URL, "--token-file", tokenPath}
			var out, diagnostic bytes.Buffer
			if code := run(append([]string{"observer", "list"}, args...), &out, &diagnostic); code != 0 || strings.Count(out.String(), `"prune_eligible":false`) != 1 || strings.Count(out.String(), `"prune_eligible"`) != 1 {
				t.Fatalf("legacy list: exit=%d stdout=%s diagnostic=%s", code, out.String(), diagnostic.String())
			}
			out.Reset()
			prune := append([]string{"observer", "prune", "--id", id, "--runtime-id", runtime, "--expected-version", "3"}, args...)
			if code := run(prune, &out, &diagnostic); code != 1 || !strings.Contains(diagnostic.String(), reply.diagnostic) || strings.Contains(diagnostic.String(), "not for the terminal") {
				t.Fatalf("prune: exit=%d diagnostic=%s", code, diagnostic.String())
			}
		})
	}
}

func TestObserverCLIControls(t *testing.T) {
	base := t.TempDir()
	tokenPath := filepath.Join(base, "owner", "token")
	token, e := local.InitToken(tokenPath)
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	server := httptest.NewServer(api.New(t.Context(), s))
	defer server.Close()
	call := func(expect int, args ...string) []byte {
		t.Helper()
		args = append([]string{args[0], args[1], "--endpoint", server.URL, "--token-file", tokenPath}, args[2:]...)
		var out, diagnostic bytes.Buffer
		code := run(args, &out, &diagnostic)
		if code != expect {
			t.Fatalf("code=%d expected=%d: %s", code, expect, diagnostic.String())
		}
		return out.Bytes()
	}
	config := filepath.Join(base, "observer", "config.json")
	call(0, "observer", "provision", "--name", "Fictional", "--observer-socket", "/tmp/fictional.sock", "--output", config)
	c, e := local.ReadObserverConfig(config, true)
	if e != nil {
		t.Fatal(e)
	}
	call(0, "observer", "unbind", "--id", c.ObserverID, "--expected-version", "1")
	var ids []string
	for range 2 {
		r := store.ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1", BridgeVersion: "1", Cwd: "/fixture"}
		if _, err := s.RegisterObserver(t.Context(), c.Payload.Verifier, r); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.RuntimeID)
	}
	call(1, "observer", "revoke", "--id", c.ObserverID, "--expected-version", "1")
	response := call(0, "observer", "revoke", "--id", c.ObserverID, "--expected-version", "2")
	var result store.ObserverOwnerResult
	if e = json.Unmarshal(response, &result); e != nil || result.Version != 3 {
		t.Fatal("revoke result", e)
	}
	prune := []string{"observer", "prune", "--id", c.ObserverID, "--expected-version", "3"}
	call(2, prune...)
	call(2, append(prune, "--runtime-id", "invalid")...)
	call(2, append(prune, "--runtime-id", ids[0], "--runtime-id", ids[0])...)
	call(1, append(prune, "--runtime-id", ids[0])...)
	response = call(0, append(prune, "--runtime-id", ids[1], "--runtime-id", ids[0])...)
	if e = json.Unmarshal(response, &result); e != nil || result.PrunedCount != 2 || result.Version != 4 {
		t.Fatal("prune result", e, result)
	}
	call(2, "observer", "bind", "--id", c.ObserverID, "--expected-version", "4")
	call(2, "observer", "no-such-command")
	call(2, "observer", "list", "--endpoint", "https://example.invalid")
	// Replacing the pinned database blocks provisioning, even with a confirmed file.
	other, e := store.Open(t.Context(), filepath.Join(base, "other.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = other.Close() }()
	replacement := httptest.NewServer(api.New(t.Context(), other))
	defer replacement.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := ownerClient{replacement.URL, token, &http.Client{Transport: transport, Timeout: time.Second}}
	if _, e = provisionObserver(client, "", "", "", config); e == nil {
		t.Fatal("database pin ignored")
	}
}
