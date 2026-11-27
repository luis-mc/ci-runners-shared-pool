package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// FileStore is the secrets.Store the CLI uses where no OS store is wired: one
// JSON file with mode 0600. It refuses to read a file whose mode is wider
// (cli R6.2). An OS-backed store (Windows Credential Manager, macOS Keychain,
// Linux Secret Service) replaces it behind the same interface.
type FileStore struct {
	Path string
	mu   sync.Mutex
}

var _ secrets.Store = (*FileStore)(nil)

// Backend names the store.
func (s *FileStore) Backend() string { return "file" }

func (s *FileStore) load() (map[string]string, error) {
	info, err := os.Stat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	// Windows has no Unix mode bits; the file sits in the user's profile, whose ACL limits it.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: %s must have mode 0600", secrets.ErrUnavailable, s.Path)
	}
	raw, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	m := map[string]string{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%w: unreadable secret file", secrets.ErrUnavailable)
		}
	}
	return m, nil
}

func (s *FileStore) save(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(s.Path, raw, 0o600); err != nil {
		return fmt.Errorf("%w: %v", secrets.ErrUnavailable, err)
	}
	return os.Chmod(s.Path, 0o600)
}

// Get returns the secret under key.
func (s *FileStore) Get(_ context.Context, key secrets.Key) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return nil, err
	}
	v, ok := m[string(key)]
	if !ok {
		return nil, secrets.ErrNotFound
	}
	return []byte(v), nil
}

// Put stores value under key.
func (s *FileStore) Put(_ context.Context, key secrets.Key, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	m[string(key)] = string(value)
	return s.save(m)
}

// Delete removes the secret under key.
func (s *FileStore) Delete(_ context.Context, key secrets.Key) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load()
	if err != nil {
		return err
	}
	delete(m, string(key))
	return s.save(m)
}
