package main

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"testing"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputFailure(t *testing.T) {
	if run([]string{"--version"}, brokenWriter{}, io.Discard) != 1 {
		t.Fatal("output failure must exit nonzero")
	}
}

func TestProcessExit(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "artelctl")
	// #nosec G204 -- Builds only this package into a test-owned temporary path, without a shell.
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v: %s", err, output)
	}
	for _, tc := range []struct {
		arg  string
		code int
	}{{"--help", 0}, {"--version", 0}, {"bad-command", 2}} {
		// #nosec G204 -- Executes the test-built CLI with table-defined arguments, without a shell.
		cmd := exec.CommandContext(t.Context(), binary, tc.arg)
		cmd.Env = []string{"HOME=" + t.TempDir()}
		err := cmd.Run()
		var exit *exec.ExitError
		if tc.code == 0 && err != nil {
			t.Fatal(err)
		}
		if tc.code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != tc.code) {
			t.Fatalf("%s: %v", tc.arg, err)
		}
	}
}

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"--help"}, 0}, {[]string{"-h"}, 0}, {[]string{"--version"}, 0},
		{nil, 2}, {[]string{"status"}, 2}, {[]string{"--version", "extra"}, 2}, {[]string{"--unknown"}, 2},
	} {
		var out, errOut bytes.Buffer
		if code := run(tc.args, &out, &errOut); code != tc.code {
			t.Fatalf("%v: %d", tc.args, code)
		}
		if tc.code == 0 && (out.Len() == 0 || errOut.Len() != 0) {
			t.Fatal("invalid successful output")
		}
		if tc.code != 0 && (out.Len() != 0 || errOut.Len() == 0) {
			t.Fatal("invalid error output")
		}
	}
}
