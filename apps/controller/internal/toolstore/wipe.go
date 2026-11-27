package toolstore

import (
	"context"
	"errors"
	"os"
)

// Identity wipes what a job user leaves outside its home: the registry hive of the job user on Windows, the
// keychain on macOS (step 8). The real ones are identity_windows.go and identity_darwin.go.
type Identity interface {
	// Wipe clears the per-user state of user. It must be safe to call when there is nothing to clear.
	Wipe(ctx context.Context, user string) error
}

// TreeKiller kills a process and every process it started (step 6). The real ones are tree_*.go.
type TreeKiller interface {
	KillTree(ctx context.Context, pid int) error
}

// Finish ends a job: it kills the whole process tree first (a leftover process could write into the folders
// about to be wiped), then removes the job home files and temp, then wipes the job user's registry or
// keychain. Every step runs even when an earlier one fails; the errors are joined.
func (jh JobHome) Finish(ctx context.Context, k TreeKiller, pid int, id Identity, user string) error {
	var errs []error
	if k != nil && pid > 0 {
		errs = append(errs, k.KillTree(ctx, pid))
	}
	errs = append(errs, os.RemoveAll(jh.Root))
	if id != nil && user != "" {
		errs = append(errs, id.Wipe(ctx, user))
	}
	return errors.Join(errs...)
}
