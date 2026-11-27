package macvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/vmclone"
)

// FS is the file work of a clone.
type FS interface {
	// CloneFile makes a copy-on-write copy of src at dst (APFS clonefile).
	CloneFile(src, dst string) error
	Remove(path string) error
	// List returns the names in dir.
	List(dir string) ([]string, error)
}

// Disks implements vmclone.Disks with APFS clones: a clone shares blocks with
// the base and costs nothing until the job writes.
type Disks struct{ FS FS }

var _ vmclone.Disks = Disks{}

// Clone makes the per-job disk. It never overwrites: a clone that exists
// belongs to another job or is an orphan the sweep deletes.
func (d Disks) Clone(ctx context.Context, base, clone string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.FS.CloneFile(base, clone); err != nil {
		return fmt.Errorf("clone %s: %w", filepath.Base(base), err)
	}
	return nil
}

// Remove deletes a clone; a missing clone is no error.
func (d Disks) Remove(_ context.Context, clone string) error {
	if err := d.FS.Remove(clone); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", filepath.Base(clone), err)
	}
	return nil
}

// List returns the clones this package made in dir.
func (d Disks) List(_ context.Context, dir string) ([]string, error) {
	names, err := d.FS.List(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, vmclone.CloneSuffix) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out, nil
}

// OSFS is the real FS. CloneFile is per OS (clonefile_darwin.go).
type OSFS struct{}

// Remove implements FS.
func (OSFS) Remove(p string) error { return os.Remove(p) }

// List implements FS.
func (OSFS) List(dir string) ([]string, error) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out, nil
}
