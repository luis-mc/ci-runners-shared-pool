//go:build unix

package vmcheck

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Lock takes the exclusive lock of one name in the cache (flock on
// <cache>/.lock-<name>) and returns the function that releases it. It waits
// until ctx ends. Two jobs that want the same download or base disk meet here,
// so the second finds the file the first made.
func Lock(ctx context.Context, cacheDir, name string) (func(), error) {
	if err := EnsureCacheDir(cacheDir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(cacheDir, ".lock-"+name), os.O_CREATE|os.O_RDWR, 0o664) //nolint:gosec // shared cache lock file
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) //nolint:gosec // fd fits an int
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR { //nolint:errorlint // raw errno from the syscall
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // fd fits an int
		_ = f.Close()
	}, nil
}

// setShared gives a folder mode 2775 (setgid, group write) when this process
// may; a folder of another owner keeps the mode its owner set.
func setShared(dir string) { _ = os.Chmod(dir, os.ModeSetgid|0o775) }

// PidAlive says whether a process id runs.
func PidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM //nolint:errorlint // raw errno from the syscall
}
