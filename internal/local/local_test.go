package local

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func TestPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := Resolve("", "")
	if err != nil || p.DataDir != filepath.Join(home, ".local/share/artel/v2") || p.TokenFile != filepath.Join(home, ".config/artel/local-owner.token") {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	p, err = Resolve("", "")
	if err != nil || p.DataDir != filepath.Join(home, "data/artel/v2") {
		t.Fatal("XDG defaults")
	}
	p, err = Resolve(filepath.Join(home, "explicit"), filepath.Join(home, "credential"))
	if err != nil || p.TokenFile != filepath.Join(home, "credential") {
		t.Fatal("overrides")
	}
	if _, err = Resolve("relative", ""); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestExplicitPathsWithoutHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	if _, err := Resolve(filepath.Join(base, "data"), filepath.Join(base, "token")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve("", ""); err == nil {
		t.Fatal("missing home accepted")
	}
}

func TestSpecialCredentialFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "token")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("FIFO credential accepted")
	}
}

func TestTokenLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "owner.token")
	first, err := InitToken(path)
	if err != nil || len(first) != 64 {
		t.Fatalf("init: %v", err)
	}
	second, err := InitToken(path)
	if err != nil || second != first {
		t.Fatal("credential replaced")
	}
	read, err := ReadToken(path)
	if err != nil || read != first {
		t.Fatal("read")
	}
	chmodFixture(t, path, 0o644)
	if _, err = InitToken(path); err == nil || strings.Contains(err.Error(), first) {
		t.Fatal("unsafe file accepted or secret leaked")
	}
	chmodFixture(t, path, 0o600)
	chmodFixture(t, filepath.Dir(path), 0o755)
	if _, err = ReadToken(path); err == nil {
		t.Fatal("unsafe parent accepted")
	}
}

func TestTokenConcurrentInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "owner.token")
	var wg sync.WaitGroup
	results := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			token, err := InitToken(path)
			if err != nil {
				t.Error(err)
				return
			}
			results <- token
		})
	}
	wg.Wait()
	close(results)
	first := ""
	for token := range results {
		if first == "" {
			first = token
		}
		if token != first {
			t.Fatal("different credentials")
		}
	}
}

func chmodFixture(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	// #nosec G302 -- Deliberately unsafe permissions on test-owned fixtures exercise rejection.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func initFixture(t *testing.T, path string) {
	t.Helper()
	if _, err := InitToken(path); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafePaths(t *testing.T) {
	for _, kind := range []string{"file-link", "parent-link", "data-link", "lock-link", "bad-token", "unsafe-data"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			dir := filepath.Join(base, "private")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "token")
			initFixture(t, target)
			link := filepath.Join(base, "link")
			switch kind {
			case "file-link":
				link = filepath.Join(dir, "link")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				if _, err := InitToken(link); err == nil {
					t.Fatal("file symlink accepted")
				}
			case "parent-link":
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				if _, err := ReadToken(filepath.Join(link, "token")); err == nil {
					t.Fatal("parent symlink accepted")
				}
			case "data-link":
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				if _, err := Acquire(link); err == nil {
					t.Fatal("data symlink accepted")
				}
			case "lock-link":
				if err := os.Symlink(target, filepath.Join(dir, "daemon.lock")); err != nil {
					t.Fatal(err)
				}
				if _, err := Acquire(dir); err == nil {
					t.Fatal("lock symlink accepted")
				}
			case "bad-token":
				if err := os.WriteFile(target, []byte("not a token"), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := InitToken(target); err == nil {
					t.Fatal("invalid token accepted")
				}
			case "unsafe-data":
				chmodFixture(t, dir, 0o755)
				if _, err := Acquire(dir); err == nil {
					t.Fatal("unsafe data accepted")
				}
			}
		})
	}
}

func TestLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err2 := Acquire(dir); err2 == nil {
		_ = second.Close()
		t.Fatal("second lock succeeded")
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = again.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkValidation(t *testing.T) {
	for _, address := range []string{"0.0.0.0:7331", "localhost:7331", "[::1]:7331", "192.168.0.1:7331", "127.0.0.1:-1", "127.0.0.1:65536", "127.0.0.1:abc", "127.0.0.1:01"} {
		if ListenAddress(address) == nil {
			t.Errorf("accepted %s", address)
		}
	}
	for _, address := range []string{"127.0.0.1:7331", "127.0.0.1:0"} {
		if err := ListenAddress(address); err != nil {
			t.Error(err)
		}
	}
	for _, endpoint := range []string{"https://127.0.0.1:7331", "http://localhost:7331", "http://user@127.0.0.1:7331", "http://127.0.0.1:7331/path", "http://127.0.0.1:7331?token=secret", "http://127.0.0.1:0"} {
		if Endpoint(endpoint) == nil {
			t.Errorf("accepted %s", endpoint)
		}
	}
	if err := Endpoint("http://127.0.0.1:7331"); err != nil {
		t.Fatal(err)
	}
}
