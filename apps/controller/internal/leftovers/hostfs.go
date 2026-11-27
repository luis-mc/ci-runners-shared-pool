package leftovers

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// hostFS is the file part of the real OS layer, the same on every platform.
type hostFS struct{ root string }

func (h hostFS) Root() string { return h.root }

// Dirs lists the direct subdirectories of root; a missing root has none.
func (hostFS) Dirs(_ context.Context, root string) ([]Dir, error) {
	es, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Dir
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		d := Dir{Name: e.Name(), Path: filepath.Join(root, e.Name())}
		if info, err := e.Info(); err == nil {
			d.ModTime = info.ModTime()
		}
		out = append(out, d)
	}
	return out, nil
}

// Files lists the regular files directly in root; a missing root has none.
func (hostFS) Files(_ context.Context, root string) ([]File, error) {
	es, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range es {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Path: filepath.Join(root, e.Name()), Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

// DirSize adds up the sizes of the files below path; it does not follow links.
func (hostFS) DirSize(ctx context.Context, path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return nil // a file we cannot read adds nothing
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func (hostFS) Resolve(path string) (string, error) { return filepath.EvalSymlinks(path) }

func (hostFS) RemoveAll(path string) error { return os.RemoveAll(path) }
