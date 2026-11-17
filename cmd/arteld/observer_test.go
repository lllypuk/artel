package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

func TestObserverDaemonKillRestartAndShutdown(t *testing.T) {
	base, e := os.MkdirTemp("", "artel-observer-process-*")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	binary := filepath.Join(base, "arteld")
	// #nosec G204 -- Builds only this daemon into a temporary test path.
	if b, e := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); e != nil {
		t.Fatalf("build %v %s", e, b)
	}
	socket := filepath.Join(base, "runtime", "observer.sock")
	tokenPath := filepath.Join(base, "private", "owner.token")
	args := []string{"--listen", "127.0.0.1:0", "--data-dir", filepath.Join(base, "data"), "--token-file", tokenPath, "--observer-socket", socket}
	observerToken := strings.Repeat("ab", 32)
	observerID := uuid.NewString()
	registration := store.ObserverRegistration{RuntimeID: uuid.NewString(), PiSessionID: uuid.NewString(), Reason: "startup", PiVersion: "1.5.2", BridgeVersion: "0.1.0", Cwd: "/fictional"}
	databaseID := ""
	for iteration := range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		// #nosec G204 -- Test-owned binary, private paths, ephemeral port; no shell or global settings.
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = []string{"HOME=" + base}
		stdout, e := cmd.StdoutPipe()
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		if e = cmd.Start(); e != nil {
			cancel()
			t.Fatal(e)
		}
		t.Cleanup(func() {
			cancel()
			if cmd.ProcessState == nil {
				_ = cmd.Wait()
			}
		})
		line, e := bufio.NewReader(stdout).ReadString('\n')
		if e != nil {
			t.Fatal("startup", e)
		}
		address := strings.TrimSpace(strings.TrimPrefix(line, "arteld: listening "))
		ownerToken, e := local.ReadToken(tokenPath)
		if e != nil {
			t.Fatal(e)
		}
		ownerTransport := &http.Transport{Proxy: nil}
		ownerClient := &http.Client{Transport: ownerTransport, Timeout: 2 * time.Second}
		transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		}}
		observerClient := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		call := daemonObserverRequest(t, ctx, address, ownerClient, observerClient, ownerToken, observerToken)
		if iteration == 0 {
			call(false, "PUT", "/v1/observers/"+observerID, ownerToken, uuid.NewString(), local.ObserverProvision{Label: "Fictional", Verifier: local.TokenVerifier(observerToken)}, 200)
		}
		var registry store.ObserverRegistry
		if e = json.Unmarshal(call(false, "GET", "/v1/observers", ownerToken, "", nil, 200), &registry); e != nil {
			t.Fatal(e)
		}
		if iteration == 0 {
			databaseID = registry.DatabaseInstanceID
		} else if databaseID != registry.DatabaseInstanceID || registry.Observers[0].Runtimes[0].Freshness != "stale" {
			t.Fatal("restart identity/freshness changed incorrectly")
		}
		call(true, "POST", "/observer/v1/register", observerToken, "", registration, 200)
		call(true, "GET", "/observer/v1/context?runtime_id="+registration.RuntimeID, ownerToken, "", nil, 401)
		call(false, "GET", "/v1/snapshot", observerToken, "", nil, 401)
		call(true, "PUT", "/observer/v1/report", observerToken, "", store.ObserverReport{RuntimeID: registration.RuntimeID, ReportSeq: int64(iteration + 1), Activity: "idle", Lifecycle: "running"}, 200)
		stopObserverDaemon(t, cmd, socket, iteration == 0)
		observerTransportClose(transport, ownerTransport)
		cancel()
		if strings.Contains(diagnostic.String(), observerToken) || strings.Contains(diagnostic.String(), ownerToken) || strings.Contains(diagnostic.String(), base) {
			t.Fatal("sensitive diagnostics")
		}
	}
}
func observerTransportClose(transports ...*http.Transport) {
	for _, transport := range transports {
		transport.CloseIdleConnections()
	}
}
