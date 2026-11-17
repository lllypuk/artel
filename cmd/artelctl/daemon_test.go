package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Use a real isolated daemon, not a production listener or a mock JSON response.
func TestCLIWithTemporaryDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	dir := t.TempDir()
	binary := filepath.Join(dir, "arteld")
	// #nosec G204 -- Fixed Go build command; output is a test-private temporary path.
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../arteld")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	home := filepath.Join(dir, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "private", "owner.token")
	// #nosec G204 -- Binary is built from this source into a test-private directory.
	daemon := exec.CommandContext(ctx, binary, "--listen", "127.0.0.1:0", "--data-dir", filepath.Join(dir, "data"), "--token-file", token)
	daemon.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "config"), "XDG_DATA_HOME="+filepath.Join(home, "data"))
	pipe, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	daemon.Stderr = &stderr
	if err = daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	scanner := bufio.NewScanner(pipe)
	if !scanner.Scan() {
		t.Fatal("daemon failed to announce address")
	}
	address := strings.TrimPrefix(scanner.Text(), "arteld: listening ")
	for _, cmd := range []string{"status", "snapshot"} {
		var out, diagnostic bytes.Buffer
		code := run([]string{cmd, "--json", "--endpoint", "http://" + address, "--token-file", token}, &out, &diagnostic)
		if code != 0 {
			t.Fatalf("%s code %d: %s", cmd, code, &diagnostic)
		}
		var data map[string]json.RawMessage
		if err = json.Unmarshal(out.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if cmd == "snapshot" && (data["instance_id"] == nil || string(data["projects"]) != "[]") {
			t.Fatalf("invalid snapshot %s", &out)
		}
		if cmd == "status" && string(data["status"]) != `"ok"` {
			t.Fatalf("invalid status %s", &out)
		}
	}
}
