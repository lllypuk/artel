package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// ObserverSocket owns its socket inode and a separate lifetime lock. Lock files
// deliberately survive shutdown and SIGKILL, so competing owners share one inode.
type ObserverSocket struct {
	net.Listener
	lock     *Lock
	path     string
	inode    os.FileInfo
	once     sync.Once
	closeErr error
}

func socketInfo(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int64(st.Uid) == int64(os.Getuid()) && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600
}

// ListenObserver never replaces files or a listening socket, even under its lock.
func ListenObserver(path string) (*ObserverSocket, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 107 {
		return nil, errors.New("invalid observer socket path")
	}
	dir := filepath.Dir(path)
	lock, err := acquire(dir, filepath.Base(path)+".lock")
	if err != nil {
		return nil, errors.New("observer socket locked or unsafe")
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	if err = noSymlinks(path); err != nil {
		return nil, err
	}
	if info, e := os.Lstat(path); e == nil {
		if !socketInfo(info) {
			return nil, errors.New("unsafe observer socket")
		}
		conn, dialErr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(context.Background(), "unix", path)
		if dialErr == nil {
			_ = conn.Close()
			return nil, errors.New("observer socket occupied")
		}
		// Only ECONNREFUSED proves a stale listener; timeout/permissions are not proof.
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return nil, errors.New("observer socket unavailable")
		}
		current, e := os.Lstat(path)
		if e != nil || !os.SameFile(info, current) || !socketInfo(current) {
			return nil, errors.New("observer socket changed")
		}
		if e = os.Remove(path); e != nil {
			return nil, errors.New("cannot remove stale observer socket")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, errors.New("cannot inspect observer socket")
	}
	socket, err := bindObserverSocket(path, lock)
	success = err == nil
	return socket, err
}

func bindObserverSocket(path string, lock *Lock) (*ObserverSocket, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, errors.New("cannot bind observer socket")
	}
	listener.SetUnlinkOnClose(false)
	original, e := os.Lstat(path)
	if e != nil || original.Mode()&os.ModeSocket == 0 {
		_ = listener.Close()
		return nil, errors.New("observer socket changed")
	}
	success := false
	defer func() {
		if !success {
			_ = listener.Close()
			if current, e := os.Lstat(path); e == nil && os.SameFile(original, current) {
				_ = os.Remove(path)
			}
		}
	}()
	if err = os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, errors.New("cannot protect observer socket")
	}
	info, err := os.Lstat(path)
	if err != nil || !socketInfo(info) || !os.SameFile(original, info) {
		_ = listener.Close()
		return nil, errors.New("observer socket changed")
	}
	success = true
	return &ObserverSocket{Listener: listener, lock: lock, path: path, inode: info}, nil
}

// Close removes only the originally bound inode, never a replacement file.
func (s *ObserverSocket) Close() error {
	s.once.Do(func() {
		s.closeErr = s.Listener.Close()
		if info, e := os.Lstat(s.path); e == nil && os.SameFile(info, s.inode) && socketInfo(info) {
			_ = os.Remove(s.path)
		}
		_ = s.lock.Close()
	})
	return s.closeErr
}
