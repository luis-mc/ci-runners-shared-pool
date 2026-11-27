//go:build !windows

package toolstore

import (
	"io/fs"
	"os"
	"path/filepath"
)

// sealDir removes every write bit under dir, so no job user can change an installed version.
func sealDir(dir string) error { return chmodTree(dir, false) }

// unsealDir gives the owner write access again, for housekeeping and a failed install.
func unsealDir(dir string) error { return chmodTree(dir, true) }

func chmodTree(dir string, write bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		m := info.Mode().Perm()
		if write {
			m |= 0o200
		} else {
			m &^= 0o222
		}
		if d.IsDir() && write {
			m |= 0o700
		}
		return os.Chmod(p, m)
	})
}
