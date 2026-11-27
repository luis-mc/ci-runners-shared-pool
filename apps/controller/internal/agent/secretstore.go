// Package agent is the agent mode of crsp (decision 0004; machines R5.1 to
// R5.13, R5.23, R5.24, R5.30 to R5.39, R5.44 to R5.48): it dials the controller
// over TLS, keeps one Session open, reports facts and load, applies settings,
// holds new jobs for a drain and never opens a listening port (R5.9).
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// Secret store keys of the agent (R5.7).
const (
	MachineKeySecret secrets.Key = "crsp/agent/machine-key"
	MachineIDSecret  secrets.Key = "crsp/agent/machine-id"
	PinnedCertSecret secrets.Key = "crsp/agent/pinned-cert-sha256"
)

// FileStore is the secrets.Store of Linux without a Secret Service: one file
// per secret, mode 0600, in a folder of mode 0700 owned by the agent account
// (machines R5.7). Windows Credential Manager or DPAPI and the macOS Keychain
// are further implementations of secrets.Store.
type FileStore struct{ dir string }

var _ secrets.Store = (*FileStore)(nil)

// NewFileStore makes the folder (0700) and returns the store.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	return &FileStore{dir: dir}, nil
}

func (f *FileStore) path(k secrets.Key) string {
	sum := sha256.Sum256([]byte(k))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:8])+".secret")
}

// Get implements secrets.Store.
func (f *FileStore) Get(_ context.Context, k secrets.Key) ([]byte, error) {
	b, err := os.ReadFile(f.path(k))
	if errors.Is(err, os.ErrNotExist) {
		return nil, secrets.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	return b, nil
}

// Put implements secrets.Store. The write is atomic and the file is 0600.
func (f *FileStore) Put(_ context.Context, k secrets.Key, v []byte) error {
	tmp, err := os.CreateTemp(f.dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	name := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	if _, err := tmp.Write(v); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	if err := os.Rename(name, f.path(k)); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	return nil
}

// Delete implements secrets.Store.
func (f *FileStore) Delete(_ context.Context, k secrets.Key) error {
	if err := os.Remove(f.path(k)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	return nil
}

// Backend implements secrets.Store.
func (f *FileStore) Backend() string { return "file" }
