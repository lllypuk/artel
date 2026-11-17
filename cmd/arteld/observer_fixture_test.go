package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

func daemonObserverRequest(t *testing.T, ctx context.Context, address string, ownerClient, observerClient *http.Client, ownerToken, observerToken string) func(bool, string, string, string, string, any, int) []byte {
	t.Helper()
	return func(observer bool, method, path, token, key string, body any, status int) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		endpoint := "http://" + address
		client := ownerClient
		if observer {
			endpoint = "http://artel-observer"
			client = observerClient
		}
		req, e := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(data))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", key)
		// #nosec G704 -- Test-owned ephemeral numeric loopback or private Unix transport only.
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if e != nil || response.StatusCode != status {
			t.Fatalf("%s %s: %d %s %v", method, path, response.StatusCode, b, e)
		}
		if bytes.Contains(b, []byte(ownerToken)) || bytes.Contains(b, []byte(observerToken)) {
			t.Fatal("secret in response")
		}
		return b
	}
}
func stopObserverDaemon(t *testing.T, cmd *exec.Cmd, socket string, kill bool) {
	t.Helper()
	if kill {
		if e := cmd.Process.Kill(); e != nil {
			t.Fatal(e)
		}
		if e := cmd.Wait(); e == nil {
			t.Fatal("SIGKILL exited successfully")
		}
		if _, e := os.Lstat(socket); e != nil {
			t.Fatal("SIGKILL did not leave stale socket", e)
		}
	} else {
		if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
			t.Fatal(e)
		}
		if e := cmd.Wait(); e != nil {
			t.Fatal(e)
		}
		if _, e := os.Lstat(socket); !os.IsNotExist(e) {
			t.Fatal("graceful shutdown leaked socket")
		}
	}
}
