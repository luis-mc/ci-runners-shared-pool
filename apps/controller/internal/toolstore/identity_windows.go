//go:build windows

package toolstore

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// WinIdentity wipes the registry of a job user: it loads nothing and keeps nothing. The hive of a user that
// is not logged on is a file (NTUSER.DAT) in its profile, which Finish removes with the job home when the job
// user is a reusable account with a profile folder under the job home; for a logged-on user the keys under
// HKU\<sid> are deleted one by one, except the Classes and Software\Microsoft\Windows hives Windows needs.
type WinIdentity struct{}

var _ Identity = WinIdentity{}

// Wipe implements Identity.
func (WinIdentity) Wipe(ctx context.Context, user string) error {
	sid, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"(New-Object System.Security.Principal.NTAccount($args[0])).Translate([System.Security.Principal.SecurityIdentifier]).Value", user).Output()
	if err != nil {
		return fmt.Errorf("sid of %s: %w", user, err)
	}
	hive := `HKU\` + strings.TrimSpace(string(sid))
	for _, sub := range []string{`Software`, `Environment`, `Volatile Environment`} {
		if out, err := exec.CommandContext(ctx, "reg", "delete", hive+`\`+sub, "/f").CombinedOutput(); err != nil &&
			!strings.Contains(string(out), "unable to find") {
			return fmt.Errorf("reg delete %s: %w", sub, err)
		}
	}
	return nil
}
