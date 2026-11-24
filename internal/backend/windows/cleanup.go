package windows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

// startState records what a failed Start made, so the clean-up knows what to
// undo.
type startState struct {
	made       bool // the folder or a fresh registration was started
	registered bool // the runner configure step was tried
	account    bool // the account step was tried
	service    bool // the service step was tried
}

// StartFailure is a failed add together with what the clean-up did. Its text
// is whole and plain, and it says what is left behind (runners R5.10).
type StartFailure struct {
	Err     error
	Cleaned []string
	Left    []string
}

// Error implements error.
func (e *StartFailure) Error() string {
	var b strings.Builder
	// The cause often ends in a full stop already; each part adds one.
	b.WriteString(strings.TrimRight(strings.TrimSpace(e.Err.Error()), ". "))
	if len(e.Cleaned) > 0 {
		b.WriteString(". Cleaned up: " + strings.Join(e.Cleaned, ", "))
	}
	if len(e.Left) > 0 {
		b.WriteString(". Left behind: " + strings.Join(e.Left, "; "))
	}
	return b.String()
}

// Unwrap implements errors.Unwrap.
func (e *StartFailure) Unwrap() error { return e.Err }

// cleanFailedStart undoes what a failed add made: the service, the GitHub
// registration when a remove token came with the add, the account and the
// folder. Each step is tried even when an earlier one failed; what stays is
// named in the error. It runs on a context that outlives a cancelled add.
func (b *Backend) cleanFailedStart(ctx context.Context, r backend.RunnerSpec, cfg backend.StartConfig, st *startState, ref string, cause error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	folder := b.Folder(r.Name)
	account := runners.AccountName(r.Name, nil)
	service := ref
	out := &StartFailure{Err: cause}
	done := func(what string, err error) {
		if err != nil {
			out.Left = append(out.Left, what+" ("+b.redact(plainError(err.Error(), ""))+")")
			return
		}
		out.Cleaned = append(out.Cleaned, what)
	}
	if st.service {
		done("service "+service, b.psOK(ctx, "delete service", scriptDeleteService, map[string]string{"CRSP_SERVICE": service}, nil))
	}
	if st.registered && cfg.Removal != "" {
		done("GitHub registration "+r.Name, b.runner(ctx, "remove registration", folder, []string{"remove"}, cfg.Removal))
	}
	if st.account {
		res, err := b.ps(ctx, scriptRemoveAccount, map[string]string{"CRSP_ACCOUNT": account}, nil)
		switch {
		case err != nil:
			done("account "+account, fmt.Errorf("remove account: %w", err))
		case res.ExitCode != 0:
			done("account "+account, fmt.Errorf("remove account: powershell exited %d: %s", res.ExitCode, plainError(res.Stderr, res.Stdout)))
		case strings.Contains(res.Stdout, "account-absent"):
			// Never made, or taken away by an earlier clean-up: nothing to claim.
		default:
			done("account "+account, nil)
		}
	}
	if b.d.FS.Exists(folder) {
		done("folder "+folder, b.d.FS.RemoveAll(folder))
	}
	b.log("cleanup", out.Error())
	return out
}
