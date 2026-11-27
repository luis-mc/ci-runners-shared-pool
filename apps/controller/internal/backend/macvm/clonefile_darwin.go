//go:build darwin

package macvm

import "golang.org/x/sys/unix"

// CloneFile implements FS with clonefile(2): copy-on-write on APFS. It fails,
// and never falls back to a full copy, on a volume that is not APFS.
func (OSFS) CloneFile(src, dst string) error { return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW) }
