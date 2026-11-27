package windows

import (
	"context"
	"fmt"
	"strings"
)

// RightsStatus is what the machine says about the symbolic link right of one
// runner account (runners R5.14).
type RightsStatus struct {
	// SymlinkHeld is true when the account itself holds
	// SeCreateSymbolicLinkPrivilege.
	SymlinkHeld bool
	// DeveloperMode is true when Windows Developer Mode is on. It lets a
	// non-admin account make symbolic links without the right. It is only
	// read: the agent never turns it on, because it is a setting of the host
	// outside the runner account.
	DeveloperMode bool
}

// CanLink reports whether a job of the account can create a symbolic link.
func (s RightsStatus) CanLink() bool { return s.SymlinkHeld || s.DeveloperMode }

// RightsGranter sets and reads the user rights of the runner's own account. It
// touches no other account and no other setting of the host (runners R5.14).
// The Windows backend takes one through Config.Rights; tests use a fake.
type RightsGranter interface {
	// Grant gives the account "log on as a service" and the two logon denials,
	// and gives it SeCreateSymbolicLinkPrivilege when symlink is true or takes
	// it away when false.
	Grant(ctx context.Context, account string, symlink bool) error
	// Status reads the symbolic link right of the account and Developer Mode.
	Status(ctx context.Context, account string) (RightsStatus, error)
}

// psRights is the PowerShell implementation: secedit on the user rights area.
type psRights struct{ b *Backend }

// Grant implements RightsGranter.
func (p psRights) Grant(ctx context.Context, account string, symlink bool) error {
	return p.b.psOK(ctx, "rights", scriptRights,
		map[string]string{"CRSP_ACCOUNT": account, "CRSP_SYMLINK": flag(symlink)}, nil)
}

// Status implements RightsGranter.
func (p psRights) Status(ctx context.Context, account string) (RightsStatus, error) {
	res, err := p.b.ps(ctx, scriptRightsStatus, map[string]string{"CRSP_ACCOUNT": account}, nil)
	if err != nil {
		return RightsStatus{}, fmt.Errorf("rights status: %w", err)
	}
	if res.ExitCode != 0 {
		return RightsStatus{}, fmt.Errorf("rights status: powershell exited %d: %s", res.ExitCode, p.b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return ParseRightsStatus(res.Stdout), nil
}

// ParseRightsStatus reads the `symlink=True;devmode=False` line of
// scriptRightsStatus. Anything else reads as false.
func ParseRightsStatus(out string) RightsStatus {
	var s RightsStatus
	for _, kv := range strings.Split(strings.TrimSpace(out), ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(kv), "=")
		on := strings.EqualFold(strings.TrimSpace(v), "true")
		switch k {
		case "symlink":
			s.SymlinkHeld = on
		case "devmode":
			s.DeveloperMode = on
		}
	}
	return s
}

// SymlinkStatus reads the symbolic link right of a runner's account, for the
// agent's report to the doctor (doctor R5.42).
func (b *Backend) SymlinkStatus(ctx context.Context, account string) (RightsStatus, error) {
	return b.rights.Status(ctx, account)
}
