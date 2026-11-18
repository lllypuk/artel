package local

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
)

type ObserverProvision struct {
	Label    string `json:"label"`
	Verifier string `json:"verifier"`
}

// ObserverConfig contains no plaintext credential. Pending artifacts are durable
// recovery records, not permission for an extension to contact the daemon.
type ObserverConfig struct {
	Status             string            `json:"status"`
	ObserverID         string            `json:"observer_id"`
	DatabaseInstanceID string            `json:"database_instance_id"`
	ObserverSocket     string            `json:"observer_socket"`
	TokenFile          string            `json:"token_file"`
	ProvisioningKey    string            `json:"provisioning_key"`
	Payload            ObserverProvision `json:"payload"`
}

func TokenVerifier(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func readPrivate(path string) ([]byte, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("private path must be absolute")
	}
	if err := noSymlinks(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err = privateInfo(info, true); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("private file unavailable")
	}
	// #nosec G115 -- Successful syscall.Open returns a nonnegative descriptor.
	f := os.NewFile(uintptr(fd), "private file")
	defer func() { _ = f.Close() }()
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if err = privateInfo(info, false); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(b) > 16384 {
		return nil, errors.New("invalid private file")
	}
	return b, nil
}

// ReadObserverConfig validates permissions and verifier; consumers must request
// confirmedOnly. Provision --resume is the sole caller accepting pending state.
func ReadObserverConfig(path string, confirmedOnly bool) (ObserverConfig, error) {
	var c ObserverConfig
	b, err := readPrivate(path)
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return c, errors.New("invalid observer config")
	}
	validID := func(s string) bool { u, e := uuid.Parse(s); return e == nil && u != uuid.Nil && u.String() == s }
	if !validID(c.ObserverID) || !validID(c.DatabaseInstanceID) || !validID(c.ProvisioningKey) || !filepath.IsAbs(c.ObserverSocket) || len(c.ObserverSocket) > 107 || (c.Status != "pending" && c.Status != "confirmed") || confirmedOnly && c.Status != "confirmed" {
		return c, errors.New("observer config not usable")
	}
	token, err := ReadToken(c.TokenFile)
	if err != nil || TokenVerifier(token) != c.Payload.Verifier {
		return c, errors.New("observer credential mismatch")
	}
	return c, nil
}

// WriteObserverConfig publishes complete, fsynced data; initial publication never
// overwrites an existing path. Confirmation only replaces a validated pending file.
func WriteObserverConfig(path string, c ObserverConfig, confirm bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("config path must be absolute")
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := noSymlinks(path); err != nil {
		return err
	}
	if confirm {
		previous, err := ReadObserverConfig(path, false)
		expected := c
		expected.Status = previous.Status
		if err != nil || previous != expected {
			return errors.New("observer config changed")
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".observer-*")
	if err != nil {
		return err
	}
	// #nosec G703 -- CreateTemp name in validated private directory.
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if confirm {
		// #nosec G703 -- CreateTemp source and absolute destination validated in private no-symlink directory.
		err = os.Rename(f.Name(), path)
	} else {
		err = os.Link(f.Name(), path)
	}
	if err != nil {
		return errors.New("observer config publication failed")
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
