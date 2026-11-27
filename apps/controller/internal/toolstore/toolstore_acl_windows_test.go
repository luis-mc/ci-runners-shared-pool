//go:build windows

package toolstore

import (
	"context"
	"os/exec"
)

// resetACL gives a sealed folder its inherited rights back (as internal/agent does, CRSP345). The seal cuts
// inheritance and grants full control to Administrators and SYSTEM only, so an owner who is not elevated cannot
// delete the test's temp folder until the ACL is reset.
func resetACL(dir string) {
	_ = exec.CommandContext(context.Background(), "icacls", dir, "/reset", "/T", "/C", "/Q").Run()
}
