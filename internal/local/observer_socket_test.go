package local

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "artel-socket-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "observer.sock")
}
func TestObserverSocketSafety(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "directory", "mode", "occupied", "lock", "parent-symlink", "parent-mode"} {
		t.Run(kind, func(t *testing.T) {
			path := unsafeSocketFixture(t, kind)
			before, _ := os.Lstat(path)
			l, e := ListenObserver(path)
			if e == nil {
				_ = l.Close()
				t.Fatal("unsafe socket accepted")
			}
			if before != nil {
				after, e := os.Lstat(path)
				if e != nil || !os.SameFile(before, after) {
					t.Fatal("foreign path changed")
				}
			}
		})
	}
	for _, path := range []string{"relative.sock", "/tmp/" + strings.Repeat("a", 108)} {
		if l, e := ListenObserver(path); e == nil {
			_ = l.Close()
			t.Fatal("invalid path accepted")
		}
	}
}

type socketStatFixture struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (s socketStatFixture) Sys() any { return s.stat }

func TestObserverSocketOwnershipAndConcurrentAcquire(t *testing.T) {
	path := socketPath(t)
	l, e := ListenObserver(path)
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(path)
	if e != nil {
		t.Fatal(e)
	}
	foreign := *info.Sys().(*syscall.Stat_t)
	foreign.Uid++
	if socketInfo(socketStatFixture{info, &foreign}) {
		t.Fatal("foreign UID accepted")
	}
	_ = l.Close()
	var group sync.WaitGroup
	ready := make(chan struct{})
	results := make(chan *ObserverSocket, 2)
	for range 2 {
		group.Go(func() { <-ready; socket, _ := ListenObserver(path); results <- socket })
	}
	close(ready)
	group.Wait()
	close(results)
	winners := 0
	for socket := range results {
		if socket != nil {
			winners++
			_ = socket.Close()
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent socket owners: %d", winners)
	}
}

func TestObserverSocketDoesNotLeakDescriptors(t *testing.T) {
	path := socketPath(t)
	warmup, e := ListenObserver(path)
	if e != nil {
		t.Fatal(e)
	}
	_ = warmup.Close()
	before, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	for range 32 {
		listener, e := ListenObserver(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = listener.Close(); e != nil {
			t.Fatal(e)
		}
	}
	after, e := os.ReadDir("/proc/self/fd")
	if e != nil {
		t.Fatal(e)
	}
	if len(after) != len(before) {
		t.Fatalf("descriptor count before=%d after=%d", len(before), len(after))
	}
}

func TestObserverStaleSocketAndReplacement(t *testing.T) {
	path := socketPath(t)
	stale, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	stale.SetUnlinkOnClose(false)
	if e = os.Chmod(path, 0o600); e != nil {
		t.Fatal(e)
	}
	_ = stale.Close()
	l, e := ListenObserver(path)
	if e != nil {
		t.Fatal(e)
	}
	if info, e := os.Stat(path); e != nil || !socketInfo(info) {
		t.Fatal("socket mode")
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
	if _, e = os.Lstat(path); !os.IsNotExist(e) {
		t.Fatal("socket retained")
	}
	l, e = ListenObserver(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("replacement"), 0o600); e != nil {
		t.Fatal(e)
	}
	_ = l.Close()
	// #nosec G304 -- Test-owned replacement at generated private path.
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "replacement" {
		t.Fatal("replacement removed")
	}
}
