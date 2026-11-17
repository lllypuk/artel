package local

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func unsafeSocketFixture(t *testing.T, kind string) string {
	t.Helper()
	path := socketPath(t)
	switch kind {
	case "file":
		if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "symlink":
		if err := os.Symlink(filepath.Join(filepath.Dir(path), "missing"), path); err != nil {
			t.Fatal(err)
		}
	case "directory":
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	case "mode", "occupied":
		l, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = l.Close() })
		mode := os.FileMode(0o600)
		if kind == "mode" {
			mode = 0o666
		}
		if e = os.Chmod(path, mode); e != nil {
			t.Fatal(e)
		}
	case "lock":
		l, e := ListenObserver(path)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = l.Close() })
	case "parent-symlink":
		link := filepath.Join(filepath.Dir(path), "link")
		if e := os.Symlink(filepath.Dir(path), link); e != nil {
			t.Fatal(e)
		}
		path = filepath.Join(link, "socket")
	case "parent-mode":
		// #nosec G302 -- Intentional unsafe-directory negative fixture in temporary test tree.
		if e := os.Chmod(filepath.Dir(path), 0o755); e != nil {
			t.Fatal(e)
		}
	}
	return path
}
