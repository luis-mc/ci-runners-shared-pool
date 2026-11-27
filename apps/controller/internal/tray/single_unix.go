//go:build !windows

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock is the single-instance lock (R5.23).
type Lock struct{ f *os.File }

// Release frees the lock.
func (l *Lock) Release() {
	if l != nil && l.f != nil {
		_ = l.f.Close()
	}
}

// AcquireLock takes a lock file in the user's runtime folder. ok=false means another
// tray in this session holds it.
func AcquireLock() (*Lock, bool, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return acquireLockAt(filepath.Join(dir, fmt.Sprintf("crsp-tray-%d.lock", os.Getuid())))
}

func acquireLockAt(path string) (*Lock, bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &Lock{f: f}, true, nil
}
