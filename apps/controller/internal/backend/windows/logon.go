package windows

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// The runner service account can lose "log on as a service" after the grant
// at creation: a security policy refresh at boot rewrites User Rights
// Assignment, or the account was made again with a new SID (runners R5.89).
// The agent gives the right back, idempotently, at start, after each boot and
// right before it starts a service.

// scriptEnsureLogon gives the account the three rights through the LSA and
// reads each back. Needs CRSP_ACCOUNT. It never touches the symlink right.
const scriptEnsureLogon = scriptHeader + lsaSource + `
$want = @('SeServiceLogonRight', 'SeDenyInteractiveLogonRight', 'SeDenyRemoteInteractiveLogonRight')
$had = [CrspLsa]::Has($env:CRSP_ACCOUNT, 'SeServiceLogonRight')
[CrspLsa]::Add($env:CRSP_ACCOUNT, $want)
foreach ($r in $want) {
    if (-not [CrspLsa]::Has($env:CRSP_ACCOUNT, $r)) { throw "the account $($env:CRSP_ACCOUNT) does not hold the right $r after it was set" }
}
Write-Output ('regranted=' + (-not $had))
`

// scriptLogonRight reads, without changing anything, whether the account holds
// SeServiceLogonRight. Prints `held=True`. Needs CRSP_ACCOUNT.
const scriptLogonRight = scriptHeader + lsaSource + `
Write-Output ('held=' + [CrspLsa]::Has($env:CRSP_ACCOUNT, 'SeServiceLogonRight'))
`

// errLogonFailed is ERROR_SERVICE_LOGON_FAILED.
const errLogonFailed = "1069"

// isLogonFailure says whether a failed start was the service logon failure
// (Windows error 1069, "Logon failure: the user has not been granted the
// requested logon type at this computer").
func isLogonFailure(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "windows error "+errLogonFailed) || strings.Contains(m, "error "+errLogonFailed) ||
		strings.Contains(m, "logon failure") || strings.Contains(m, "logon failed") ||
		strings.Contains(m, "not been granted the requested logon type")
}

// accountName is the local account name without the `.\` prefix.
func accountName(account string) string {
	return strings.TrimPrefix(strings.TrimSpace(account), `.\`)
}

// EnsureLogonRight gives the runner account SeServiceLogonRight and the two
// deny-logon rights, through the same LSA call as account creation. It reports
// whether the logon right had been lost.
func (b *Backend) EnsureLogonRight(ctx context.Context, account string) (regranted bool, err error) {
	account = accountName(account)
	if !strings.HasPrefix(account, runners.AccountPrefix) {
		return false, fmt.Errorf("%s is not a crsp runner account", account)
	}
	res, err := b.ps(ctx, scriptEnsureLogon, map[string]string{"CRSP_ACCOUNT": account}, nil)
	if err != nil {
		return false, fmt.Errorf("logon right: %w", err)
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("logon right: powershell exited %d: %s", res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return strings.Contains(res.Stdout, "regranted=True"), nil
}

// LogonRightHeld reads whether the account holds SeServiceLogonRight.
func (b *Backend) LogonRightHeld(ctx context.Context, account string) (bool, error) {
	res, err := b.ps(ctx, scriptLogonRight, map[string]string{"CRSP_ACCOUNT": accountName(account)}, nil)
	if err != nil {
		return false, fmt.Errorf("logon right: %w", err)
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("logon right: powershell exited %d: %s", res.ExitCode, b.redact(plainError(res.Stderr, res.Stdout)))
	}
	return strings.Contains(res.Stdout, "held=True"), nil
}

// startService re-asserts the logon right of the account and starts the
// service. A start that fails with the logon failure (1069) gets the right
// again and one more try; any other failure, or a second logon failure,
// comes back whole.
func (b *Backend) startService(ctx context.Context, env map[string]string, account string) error {
	return b.startServiceAfter(ctx, env, account, false)
}

// startServiceAfter is startService; given is true when the caller has just
// re-asserted the right, so it is not asked a second time before the start.
func (b *Backend) startServiceAfter(ctx context.Context, env map[string]string, account string, given bool) error {
	if account != "" && !given {
		if _, err := b.EnsureLogonRight(ctx, account); err != nil {
			b.log("logon", "could not re-assert the logon right of "+account+": "+err.Error())
		}
	}
	err := b.psOK(ctx, "start", scriptStartService, env, nil)
	if err == nil || account == "" || !isLogonFailure(err.Error()) {
		return err
	}
	b.log("logon", "service "+env["CRSP_SERVICE"]+" failed with the logon failure; giving "+account+" the right again and starting once more")
	if _, gerr := b.EnsureLogonRight(ctx, account); gerr != nil {
		return fmt.Errorf("%w (and the right could not be given again: %v)", err, gerr)
	}
	return b.psOK(ctx, "start again", scriptStartService, env, nil)
}

// HealServices implements backend.ServiceHealer: for every managed crsp
// runner service it re-asserts the logon right of the account, and it starts
// a stopped service with one retry on the logon failure (runners R5.89).
func (b *Backend) HealServices(ctx context.Context) ([]backend.ServiceHealth, error) {
	found, err := b.Find(ctx)
	if err != nil {
		return nil, err
	}
	var out []backend.ServiceHealth
	var errs []error
	listed := false
	for _, f := range found {
		acct := accountName(f.Account)
		if !f.Managed || !strings.HasPrefix(acct, runners.AccountPrefix) {
			continue
		}
		h := backend.ServiceHealth{Runner: f.Name, Service: f.ServiceName, Account: `.\` + acct, Running: f.Running}
		regranted, gerr := b.EnsureLogonRight(ctx, acct)
		if gerr != nil {
			b.log("logon", gerr.Error())
			// The right could not be given; read it so the report is honest.
			if held, rerr := b.LogonRightHeld(ctx, acct); rerr == nil {
				h.LogonKnown, h.LogonHeld = true, held
			}
		} else {
			h.LogonKnown, h.LogonHeld, h.Regranted = true, true, regranted
			if regranted {
				b.log("logon", "gave "+h.Account+" the right to log on as a service again")
			}
		}
		if !f.Running {
			// A listener that cannot list the folders above its own stops at once
			// (R5.156): give the group the right before the start, once a pass.
			if !listed {
				listed = true
				if lerr := b.ensureListRight(ctx); lerr != nil {
					b.log("acl", lerr.Error())
				}
			}
			now := b.clock()
			if prob, waiting := b.heal.wait(f.ServiceName, now); waiting {
				// Still in the backoff of an earlier failed start: say why, do not try.
				h.Problem = prob
				out = append(out, h)
				continue
			}
			why := b.whyStopped(ctx, f.ServiceName)
			env := map[string]string{"CRSP_SERVICE": f.ServiceName, "CRSP_RUNNER": f.Name}
			if serr := b.startServiceAfter(ctx, env, acct, gerr == nil); serr != nil {
				h.Problem = b.redact(serr.Error())
				if why != "" {
					h.Problem += " (it stopped because: " + why + ")"
				}
				wait := b.heal.failed(f.ServiceName, now, h.Problem)
				b.log("service", "could not start "+f.ServiceName+" again, next try in "+wait.String()+": "+h.Problem)
				errs = append(errs, serr)
			} else {
				h.Running = true
				b.heal.ok(f.ServiceName)
				// A running service with a Problem is the one-time note of a
				// restart: the controller records it and shows no failure.
				h.Problem = "started again after it stopped"
				if why != "" {
					h.Problem += ": " + why
				}
				b.log("service", f.ServiceName+" "+h.Problem)
			}
		}
		out = append(out, h)
	}
	return out, errors.Join(errs...)
}
