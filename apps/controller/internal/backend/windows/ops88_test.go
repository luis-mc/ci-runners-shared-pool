package windows

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// OPS88: a native lookup that finds only accounts the fake machine has.
func (r *rig) nativeLookup() {
	r.b.cfg.LookupSID = func(a string) (string, error) {
		if !r.accounts[a] {
			return "", errors.New("LookupAccountName: No mapping between account names and security IDs was done.")
		}
		return testSID, nil
	}
}

// A retry after a clean-up that deleted the account makes the account again
// before it reads the SID, on every attempt.
func TestRetryAfterCleanupRecreatesAccountBeforeSID(t *testing.T) {
	r := newRig(t)
	r.nativeLookup()
	r.cmd.Rules = []sysfake.Rule{{Match: "New-Service", Result: sys.Result{ExitCode: 1, Stderr: errMarker + "logon failure"}}}
	spec := r.spec()
	spec.Name = "gameroompc-pool-1-r1378816711"
	account := runners.AccountName(spec.Name, nil)
	for attempt := 1; attempt <= 3; attempt++ {
		n := len(r.cmd.Calls)
		err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"})
		if err == nil || strings.Contains(err.Error(), "read SID") {
			t.Fatalf("attempt %d: err = %v", attempt, err)
		}
		if r.accounts[account] {
			t.Errorf("attempt %d: the clean-up left the account", attempt)
		}
		made, sid := -1, -1
		for i, c := range r.cmd.Calls[n:] {
			if strings.Contains(c.Stdin, "account-ready") {
				made = i
			}
			if c.Name == "takeown" && sid < 0 {
				sid = i
			}
		}
		if made < 0 || sid < 0 || made > sid {
			t.Errorf("attempt %d: account made at %d, folder prepared at %d", attempt, made, sid)
		}
		if strings.Count(err.Error(), "account "+account) != 1 || strings.Count(err.Error(), "folder ") != 1 || strings.Contains(err.Error(), "..") {
			t.Errorf("attempt %d: message says a part twice: %v", attempt, err)
		}
	}
}

// The account step that exits 0 without confirming it made nothing is an
// error that names the account, and no SID is read.
func TestAccountStepWithoutConfirmationFails(t *testing.T) {
	r := newRig(t)
	r.cmd.Fallback = nil
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.Contains(err.Error(), "crsp-box-1 was not made") {
		t.Fatalf("err = %v", err)
	}
	if idx(r, "SID.Value") >= 0 {
		t.Error("the SID was read for an account that was not made")
	}
}

// An empty PowerShell answer is "account not found", never an empty success.
func TestEmptySIDOutputIsAccountNotFound(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = []sysfake.Rule{{Match: "SID.Value", Result: sys.Result{}}}
	sid, err := r.b.accountSID(t.Context(), "crsp-box-1", nil)
	if err == nil || sid != "" || !strings.Contains(err.Error(), "account not found") {
		t.Fatalf("sid %q err %v", sid, err)
	}
	if !strings.Contains(scriptAccountSID, "account not found") {
		t.Error("the script must throw account not found")
	}
}

// The clean-up never claims an account or folder that was already gone, and
// the error ends in one full stop per part.
func TestCleanupSaysOnlyWhatItDid(t *testing.T) {
	r := newRig(t)
	cause := fmt.Errorf("read SID of crsp-box-1: LookupAccountName: No mapping between account names and security IDs was done.")
	st := &startState{made: true, account: true}
	err := r.b.cleanFailedStart(t.Context(), r.spec(), backend.StartConfig{}, st, "", cause)
	if strings.Contains(err.Error(), "..") || strings.Contains(err.Error(), "Cleaned up") {
		t.Errorf("claims a clean-up of nothing: %v", err)
	}
	r.accounts["crsp-box-1"] = true
	_ = r.fs.MkdirAll(r.b.Folder("box-1"), 0o755)
	err = r.b.cleanFailedStart(t.Context(), r.spec(), backend.StartConfig{}, st, "", cause)
	if !strings.Contains(err.Error(), "Cleaned up: account crsp-box-1, folder ") || strings.Contains(err.Error(), "..") {
		t.Errorf("err = %v", err)
	}
}

// An add after a failed runner was removed uses the account and folder of its
// own name, and nothing of the removed one.
func TestAddAfterRemovedFailedRunnerUsesNewAccountAndFolder(t *testing.T) {
	r := newRig(t)
	r.nativeLookup()
	old := r.spec()
	old.Name = "gameroompc-pool-1-r1378816711"
	r.cmd.Rules = []sysfake.Rule{{Match: "New-Service", Result: sys.Result{ExitCode: 1, Stderr: errMarker + "logon failure"}}}
	if err := start(t, r, old, backend.StartConfig{Registration: "REGTOKEN"}); err == nil {
		t.Fatal("the first add should fail")
	}
	r.cmd.Rules = nil
	n := len(r.cmd.Calls)
	fresh := r.spec()
	fresh.Name = "gameroompc-pool-2-r42"
	if err := start(t, r, fresh, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	oldAccount, account := runners.AccountName(old.Name, nil), runners.AccountName(fresh.Name, nil)
	if oldAccount == account || !r.accounts[account] || r.accounts[oldAccount] {
		t.Fatalf("accounts: %v", r.accounts)
	}
	for _, c := range r.cmd.Calls[n:] {
		if strings.Contains(c.String()+c.Env["CRSP_ACCOUNT"]+c.Env["CRSP_FOLDER"], old.Name) || c.Env["CRSP_ACCOUNT"] == oldAccount {
			t.Errorf("the new add touched the old runner: %v", c.Env)
		}
	}
}
