//go:build !windows

package cli

import "os"

// writeOwnerOnly saves data so only the current user can read it (machines
// R5.65): mode 0600, or 0700 when the file is a script that may run. The mode is
// set again after the write, because an existing file keeps its old mode.
func writeOwnerOnly(path string, data []byte, runnable bool) error {
	mode := os.FileMode(0o600)
	if runnable {
		mode = 0o700
	}
	if err := os.WriteFile(path, data, mode); err != nil { //nolint:gosec // the caller's own --out
		return err
	}
	return os.Chmod(path, mode)
}
