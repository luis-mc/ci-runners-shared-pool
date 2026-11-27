package windows

import (
	"context"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

const errResetACLPrefix = "reset folder owner and access"

// ErrPrepareFolder starts the error of a failed folder reset. The error goes on
// with the redacted detail (the failing step, the tool's exit code and its
// message), so the controller log and the runner's reason say why.
const ErrPrepareFolder = "Could not prepare the runner folder"

// FolderResetError is a failed folder reset. Detail is the whole reason, already
// redacted; Error is the short line followed by it.
type FolderResetError struct {
	Detail string
	Err    error
}

func (e *FolderResetError) Error() string {
	if e.Detail == "" {
		return ErrPrepareFolder
	}
	return ErrPrepareFolder + ": " + e.Detail
}
func (e *FolderResetError) Unwrap() error { return e.Err }

// ResetFolderACL gives the runner folder and every file in it to the account
// and puts every file back to inherited access (runners R5.50). A runner moved
// from NETWORK SERVICE to its own account otherwise keeps files, such as its
// tool cache in _work, with the old owner and explicit entries, and the first
// job fails with "Access is denied". The steps are the same as running by hand:
//
//	takeown /F <dir> /R /D Y
//	icacls <dir> /setowner *<sid> /T /C
//	icacls <dir> /reset /T /C
//
// The owner is given as the SID of the account that was made ("*S-1-5-…"),
// never as a name, so a name Windows cannot map (error 1332) never reaches
// icacls. It runs after the account exists and before the folder is locked.
// Each exit code is checked; a failure stops the change. The error is one
// short line; the step and the reason are logged and kept in Detail.
func ResetFolderACL(ctx context.Context, d sys.Deps, folder, account, sid string) error {
	if !strings.HasPrefix(sid, "S-1-") {
		return fail(d, &FolderResetError{Detail: fmt.Sprintf("%s: no SID for account %s", errResetACLPrefix, account)})
	}
	steps := []struct {
		name string
		cmd  sys.Command
	}{
		{"take ownership", sys.Command{Name: "takeown", Args: []string{"/F", folder, "/R", "/D", "Y"}}},
		{"set owner " + account, sys.Command{Name: "icacls", Args: []string{folder, "/setowner", "*" + sid, "/T", "/C"}}},
		{"reset to inherited access", sys.Command{Name: "icacls", Args: []string{folder, "/reset", "/T", "/C"}}},
	}
	for _, s := range steps {
		res, err := d.Cmd.Run(ctx, s.cmd)
		if err != nil {
			return fail(d, &FolderResetError{Detail: fmt.Sprintf("%s (%s): %v", errResetACLPrefix, s.name, err), Err: err})
		}
		if res.ExitCode != 0 {
			return fail(d, &FolderResetError{Detail: fmt.Sprintf("%s (%s): %s exited %d: %s", errResetACLPrefix, s.name, s.cmd.Name, res.ExitCode, firstLineOf(d, res.Stderr+res.Stdout))})
		}
		if d.LogLine != nil {
			d.LogLine("acl", redactWith(d, fmt.Sprintf("%s: %s %s", s.name, s.cmd.Name, strings.Join(s.cmd.Args, " "))))
		}
	}
	return nil
}

func fail(d sys.Deps, e *FolderResetError) error {
	e.Detail = redactWith(d, e.Detail)
	if d.LogLine != nil {
		d.LogLine("acl", redactWith(d, e.Detail))
	}
	return e
}

func redactWith(d sys.Deps, s string) string {
	if d.Redact != nil {
		return d.Redact(s)
	}
	return s
}

func firstLineOf(d sys.Deps, s string) string { return redactWith(d, collapse(s)) }
