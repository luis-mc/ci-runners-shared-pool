//go:build windows

package toolstore

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sealDir makes the folder read-only for every job user: the read-only attribute on files, and an ACL that
// gives Administrators and SYSTEM full control and Users read and execute only (SIDs, so no language
// dependence). Inheritance is cut, so a job user's own rights never reach into the store.
func sealDir(dir string) error {
	if err := attrTree(dir, false); err != nil {
		return err
	}
	return icacls(dir, "/inheritance:r", "/grant:r", "*S-1-5-32-544:(OI)(CI)F", "/grant:r", "*S-1-5-18:(OI)(CI)F",
		"/grant:r", "*S-1-5-32-545:(OI)(CI)RX", "/T", "/C", "/Q")
}

// unsealDir gives the admin identity write access again (it keeps full control by the ACL).
func unsealDir(dir string) error { return attrTree(dir, true) }

func icacls(dir string, args ...string) error {
	out, err := exec.CommandContext(context.Background(), "icacls", append([]string{dir}, args...)...).CombinedOutput()
	if err != nil {
		return &execError{err: err, out: string(out)}
	}
	return nil
}

type execError struct {
	err error
	out string
}

func (e *execError) Error() string { return "icacls: " + e.err.Error() + ": " + e.out }

func attrTree(dir string, write bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		m := os.FileMode(0o444)
		if write {
			m = 0o666
		}
		return os.Chmod(p, m)
	})
}

func isGone(out string) bool {
	return strings.Contains(out, "not found") || strings.Contains(out, "no running instance")
}
