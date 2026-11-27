package windows

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

// fakeRights is a RightsGranter that records its calls and touches no system.
type fakeRights struct {
	grants   []grantCall
	grantErr error
	status   RightsStatus
	statErr  error
}

type grantCall struct {
	account string
	symlink bool
}

func (f *fakeRights) Grant(_ context.Context, account string, symlink bool) error {
	f.grants = append(f.grants, grantCall{account, symlink})
	return f.grantErr
}

func (f *fakeRights) Status(context.Context, string) (RightsStatus, error) {
	return f.status, f.statErr
}

func rigWithRights(t *testing.T, fr *fakeRights) *rig {
	t.Helper()
	r := newRig(t)
	r.b = New(r.b.d, Config{Root: r.b.cfg.Root, NewPassword: r.b.cfg.NewPassword, Rights: fr})
	return r
}

// R5.14: the backend grants the rights of its own account through the
// interface, with the symlink right following the setting.
func TestStartGrantsRightsThroughInterface(t *testing.T) {
	for _, ask := range []bool{false, true} {
		fr := &fakeRights{}
		r := rigWithRights(t, fr)
		spec := r.spec()
		spec.WindowsSymlinkRight = ask
		if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
			t.Fatal(err)
		}
		if len(fr.grants) != 1 || fr.grants[0].account != "crsp-box-1" || fr.grants[0].symlink != ask {
			t.Errorf("ask=%v grants = %+v", ask, fr.grants)
		}
		if idx(r, "secedit") >= 0 {
			t.Error("with a fake granter no secedit script runs")
		}
	}
}

// A rerun with a changed setting grants again, and nothing else runs.
func TestRerunChangesRightThroughInterface(t *testing.T) {
	fr := &fakeRights{}
	r := rigWithRights(t, fr)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	r.cmd.Calls = nil
	spec.WindowsSymlinkRight = true
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	if len(fr.grants) != 2 || !fr.grants[1].symlink || len(r.cmd.Calls) != 0 {
		t.Errorf("grants = %+v, calls = %v", fr.grants, r.cmd.Texts())
	}
}

// A refused grant stops the start before the service exists.
func TestStartFailsWhenRightsFail(t *testing.T) {
	fr := &fakeRights{grantErr: errors.New("secedit failed")}
	r := rigWithRights(t, fr)
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.Contains(err.Error(), "secedit failed") {
		t.Fatalf("err = %v", err)
	}
	if idx(r, "New-Service") >= 0 {
		t.Error("the service must not be registered after a rights failure")
	}
}

func TestSymlinkStatusUsesGranter(t *testing.T) {
	fr := &fakeRights{status: RightsStatus{DeveloperMode: true}}
	r := rigWithRights(t, fr)
	st, err := r.b.SymlinkStatus(context.Background(), "crsp-box-1")
	if err != nil || !st.CanLink() || st.SymlinkHeld {
		t.Fatalf("status = %+v, err = %v", st, err)
	}
	if (RightsStatus{}).CanLink() {
		t.Error("no right and no Developer Mode cannot link")
	}
}

// The PowerShell granter reads, never writes: the status script has no
// secedit configure and no registry write.
func TestPSStatusReadsOnly(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = []sysfake.Rule{{Match: "symlink=", Result: sys.Result{Stdout: "symlink=True;devmode=False\n"}}}
	st, err := r.b.SymlinkStatus(context.Background(), "crsp-box-1")
	if err != nil || !st.SymlinkHeld || st.DeveloperMode {
		t.Fatalf("status = %+v, err = %v", st, err)
	}
	c := r.cmd.Calls[0]
	if c.Env["CRSP_ACCOUNT"] != "crsp-box-1" {
		t.Errorf("account = %q", c.Env["CRSP_ACCOUNT"])
	}
	for _, bad := range []string{"/configure", "Set-ItemProperty", "New-ItemProperty", "reg add"} {
		if strings.Contains(c.Stdin, bad) {
			t.Errorf("status script must not change the host: %s", bad)
		}
	}
	if got := ParseRightsStatus("symlink=False;devmode=True"); got.SymlinkHeld || !got.DeveloperMode {
		t.Errorf("parse = %+v", got)
	}
}
