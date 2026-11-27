//go:build !windows

package runnercache

import "os"

// applySharedMode sets 1777 explicitly, because mkdir is cut by the umask.
func applySharedMode(dir string) error {
	return os.Chmod(dir, SharedMode&^os.ModeDir)
}
