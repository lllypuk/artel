package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/lllypuk/artel/internal/api"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

func TestProvisionConfirmedWriteFailureRemainsResumable(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "private", "observer.json")
	ownerPath := filepath.Join(base, "owner", "token")
	if _, e := local.InitToken(ownerPath); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(t.Context(), filepath.Join(base, "db.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = s.Close() }()
	handler := api.New(t.Context(), s)
	var sabotage atomic.Bool
	sabotage.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if r.Method == "PUT" && sabotage.Swap(false) {
			// Force the post-ACK config validation/write to fail; this does not corrupt
			// its durable pending payload or the server's successful receipt.
			if e := os.Chmod(config, 0o400); e != nil {
				t.Error(e)
			}
		}
	}))
	defer server.Close()
	call := func(want int, args ...string) {
		t.Helper()
		var out, diagnostic bytes.Buffer
		args = append(args, "--endpoint", server.URL, "--token-file", ownerPath)
		if code := run(args, &out, &diagnostic); code != want {
			t.Fatalf("exit %d want %d: %s", code, want, diagnostic.String())
		}
	}
	call(1, "observer", "provision", "--name", "Fictional", "--observer-socket", "/tmp/fictional.sock", "--output", config)
	if e = os.Chmod(config, 0o600); e != nil {
		t.Fatal(e)
	}
	c, e := local.ReadObserverConfig(config, false)
	if e != nil || c.Status != "pending" {
		t.Fatal("pending state lost", e)
	}
	call(0, "observer", "provision", "--resume", config)
	if _, e = local.ReadObserverConfig(config, true); e != nil {
		t.Fatal(e)
	}
	registry, e := s.Observers(t.Context())
	if e != nil || len(registry.Observers) != 1 {
		t.Fatal("resume duplicated principal", e)
	}
}
