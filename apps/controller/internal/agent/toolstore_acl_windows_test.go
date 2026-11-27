//go:build windows

package agent

import (
	"context"
	"os/exec"
)

// resetACL gives a sealed store its inherited rights back. The seal cuts inheritance and grants full control to
// Administrators and SYSTEM only, so an owner who is not elevated cannot delete the folder until the ACL is reset.
func resetACL(dir string) {
	_ = exec.CommandContext(context.Background(), "icacls", dir, "/reset", "/T", "/C", "/Q").Run()
}
