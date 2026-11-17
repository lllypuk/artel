package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

func TestServeRefusesNonlocal(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if serve(t.Context(), []string{"--listen", "0.0.0.0:7331"}, &out, &diagnostic) != 1 || out.Len() != 0 {
		t.Fatal("nonlocal bind accepted")
	}
	if serve(t.Context(), []string{"--bad"}, &out, &diagnostic) != 2 {
		t.Fatal("bad flags accepted")
	}
}

func TestBindFailure(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	base := t.TempDir()
	args := []string{"--listen", listener.Addr().String(), "--data-dir", filepath.Join(base, "data"), "--token-file", filepath.Join(base, "private", "token")}
	var out, diagnostic bytes.Buffer
	if serve(t.Context(), args, &out, &diagnostic) != 1 || out.Len() != 0 || strings.Contains(diagnostic.String(), base) {
		t.Fatal("occupied bind did not fail safely")
	}
	assertUnlocked(t, filepath.Join(base, "data"))
}

func assertUnlocked(t *testing.T, data string) {
	t.Helper()
	lock, err := local.Acquire(data)
	if err != nil {
		t.Fatal("shutdown retained lock", err)
	}
	if err = lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertSecondBlocked(t *testing.T, ctx context.Context, binary string, args []string, token, base string) {
	t.Helper()
	// #nosec G204 -- Same test-owned binary and paths; no production services.
	second := exec.CommandContext(ctx, binary, args...)
	output, err := second.CombinedOutput()
	if err == nil || strings.Contains(string(output), token) || strings.Contains(string(output), base) {
		t.Fatal("lock or redaction failure")
	}
}

func assertDaemonResponse(t *testing.T, ctx context.Context, address, path, token string, status int) {
	t.Helper()
	if err := local.ListenAddress(address); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+address+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// #nosec G704 -- Test-owned ephemeral loopback listener; address validated above.
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode != status || token != "" && strings.Contains(string(body), token) {
		t.Fatal("invalid daemon response")
	}
}

func databaseIdentity(t *testing.T, data string) string {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(data, "artel.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, readErr := db.Snapshot(t.Context())
	closeErr := db.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("database read/close: %v %v", readErr, closeErr)
	}
	return snapshot.InstanceID
}

func TestDaemonLifecycle(t *testing.T) {
	base := t.TempDir()
	binary := filepath.Join(base, "arteld")
	// #nosec G204 -- Builds only this package into a test-owned path, without a shell.
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	data := filepath.Join(base, "data")
	tokenPath := filepath.Join(base, "private", "owner.token")
	args := []string{"--listen", "127.0.0.1:0", "--data-dir", data, "--token-file", tokenPath}
	token := ""
	instance := ""
	for iteration := range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		// #nosec G204 -- Executes test-built daemon with private paths and ephemeral port only.
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = []string{"HOME=" + base}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		if err = cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		// Any failed assertion kills and reaps only this test-owned process.
		t.Cleanup(func() {
			cancel()
			if cmd.ProcessState == nil {
				_ = cmd.Wait()
			}
		})
		line, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			t.Fatalf("startup: %v", err)
		}
		address := strings.TrimSpace(strings.TrimPrefix(line, "arteld: listening "))
		current, err := local.ReadToken(tokenPath)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			token = current
		} else if token != current {
			t.Fatal("credential changed on restart")
		}
		if strings.Contains(line, token) {
			t.Fatal("credential in startup output")
		}
		assertDaemonResponse(t, ctx, address, "/healthz", "", 200)
		assertDaemonResponse(t, ctx, address, "/v1/snapshot", token, 200)
		assertSecondBlocked(t, ctx, binary, args, token, base)
		if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if err = cmd.Wait(); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		currentInstance := databaseIdentity(t, data)
		if iteration == 0 {
			instance = currentInstance
		} else if instance != currentInstance {
			t.Fatal("database identity changed on daemon restart")
		}
		cancel()
		if strings.Contains(diagnostic.String(), token) {
			t.Fatal("credential in diagnostics")
		}
		assertUnlocked(t, data)
	}
	// Invalid existing credentials are never overwritten.
	if err := os.WriteFile(tokenPath, []byte("invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if serve(t.Context(), args, &out, &diagnostic) != 1 || strings.Contains(diagnostic.String(), base) {
		t.Fatal("bad credential accepted or private path printed")
	}
}
