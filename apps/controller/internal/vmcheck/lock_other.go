//go:build !unix

package vmcheck

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Lock takes the lock of one name in the cache with an exclusively created
// file (no flock here; check VMs run on Linux only, this keeps the package
// building and its tests running elsewhere).
func Lock(ctx context.Context, cacheDir, name string) (func(), error) {
	if err := EnsureCacheDir(cacheDir); err != nil {
		return nil, err
	}
	p := filepath.Join(cacheDir, ".lock-"+name)
	for {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // cache lock file
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(p) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func setShared(string) {}

// PidAlive says whether a process id runs; here it cannot tell, so it says yes
// and a lease is never taken as stale.
func PidAlive(int) bool { return true }
