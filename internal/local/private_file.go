package local

import (
	"errors"
	"os"
	"path/filepath"
)

// ReadPrivateFile reads a small private regular file after the same ownership,
// permission and symlink checks as observer configs. Callers decode and
// validate the payload; the file itself holds no implicit trust.
func ReadPrivateFile(path string) ([]byte, error) { return readPrivate(path) }

// WritePrivateFile atomically publishes data with mode 0600 inside a private
// 0700 directory, fsyncing file and directory. Initial publication refuses to
// replace an existing path; replace=true is only for validated transitions of
// a file this process read immediately before.
func WritePrivateFile(path string, data []byte, replace bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("private path must be absolute")
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := noSymlinks(path); err != nil {
		return err
	}
	if !replace {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("private artifact already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return errors.New("private artifact unavailable")
		}
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".artel-*")
	if err != nil {
		return errors.New("private artifact creation failed")
	}
	// #nosec G703 -- CreateTemp name in validated private directory.
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("private artifact write failed")
	}
	if replace {
		// #nosec G703 -- CreateTemp source and absolute destination validated in private no-symlink directory.
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
	}
	if err != nil {
		return errors.New("private artifact publication failed")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
