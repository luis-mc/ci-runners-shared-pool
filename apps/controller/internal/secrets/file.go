package secrets

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// File is the fallback store: one file per secret in a directory that only the
// service account can use (mode 0700, files 0600). It is used when the OS has
// no secret service (decision 0002).
type File struct {
	dir string
	// transform and untransform let an OS wrap the bytes (Windows DPAPI).
	transform   func([]byte) ([]byte, error)
	untransform func([]byte) ([]byte, error)
	backend     string
}

// NewFile returns a file store under dir, creating it with mode 0700.
func NewFile(dir string) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// MkdirAll does not tighten an existing directory.
	if err := os.Chmod(dir, 0o700); err != nil && !errors.Is(err, fs.ErrPermission) {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return &File{dir: dir, backend: "file"}, nil
}

func (f *File) path(key Key) string {
	return filepath.Join(f.dir, base64.RawURLEncoding.EncodeToString([]byte(key))+".secret")
}

// Get implements Store.
func (f *File) Get(_ context.Context, key Key) ([]byte, error) {
	b, err := os.ReadFile(f.path(key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if f.untransform != nil {
		b, err = f.untransform(b)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	return b, nil
}

// Put implements Store. The file is written to a temporary name with mode
// 0600 and renamed, so a reader never sees a half-written secret.
func (f *File) Put(_ context.Context, key Key, value []byte) error {
	if f.transform != nil {
		var err error
		value, err = f.transform(value)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	tmp, err := os.CreateTemp(f.dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if _, err := tmp.Write(value); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if err := os.Rename(name, f.path(key)); err != nil {
		cleanup()
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// Delete implements Store.
func (f *File) Delete(_ context.Context, key Key) error {
	err := os.Remove(f.path(key))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

// Backend implements Store.
func (f *File) Backend() string { return f.backend }

var _ Store = (*File)(nil)
