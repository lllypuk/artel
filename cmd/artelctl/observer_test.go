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
	call(1, "observer", "revoke", "--id", c.ObserverID, "--expected-version", "1")
	response := call(0, "observer", "revoke", "--id", c.ObserverID, "--expected-version", "2")
	var result store.ObserverOwnerResult
	if e = json.Unmarshal(response, &result); e != nil || result.Version != 3 {
		t.Fatal("revoke result", e)
	}
	call(0, "observer", "prune", "--id", c.ObserverID, "--expected-version", "3")
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
