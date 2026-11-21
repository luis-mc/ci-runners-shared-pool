//go:build darwin

package toolstore

import (
	"context"

	"golang.org/x/sys/unix"
)

// DefaultCloner is APFS clonefile, falling back to a copy of the tree when the volume cannot clone.
func DefaultCloner() Cloner { return apfsCloner{} }

type apfsCloner struct{}

// Clone makes dst a copy-on-write clone of src in one call; the files share blocks until one is written.
func (apfsCloner) Clone(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err == nil {
		return nil
	}
	return copyTree(src, dst)
}

// copyTree copies a tree byte for byte; the last resort of a cloner.
func copyTree(src, dst string) error {
	return walkClone(context.Background(), src, dst, copyFile)
}
