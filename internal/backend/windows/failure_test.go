package windows

import (
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys/sysfake"
)

const logonText = "Service 'CRSP runner box-1 (actions.runner.luis-mc-demo.box-1)' cannot be started due to the following error: " +
	"Cannot start service actions.runner.luis-mc-demo.box-1 on computer '.'. | The service did not start due to a logon failure"

// The right is set before the logon check and the service, with the LSA and
// not a rewrite of the whole policy; the check and the service both get the
// password the account got.
func TestRightBeforeServiceAndPasswordPassed(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	rights, check, svc, acct := idx(r, "LsaAddAccountRights"), idx(r, "CrspLogon"), idx(r, "Start-Service"), idx(r, "New-LocalUser")
	if acct < 0 || acct >= rights || rights >= check || check >= svc {
		t.Fatalf("order: account %d, rights %d, logon check %d, service %d", acct, rights, check, svc)
	}
	if strings.Contains(r.cmd.Calls[rights].Stdin, "secedit") {
		t.Error("the right is set through the LSA, never by rewriting the policy")
	}
	for _, i := range []int{acct, check, svc} {
		if got := r.cmd.Calls[i].SecretEnv["CRSP_PASSWORD"]; got != testPassword {
			t.Errorf("call %d got password %q", i, got)
		}
	}
	if !strings.Contains(r.cmd.Calls[rights].Stdin, "SeServiceLogonRight") || !strings.Contains(r.cmd.Calls[rights].Stdin, "Has(") {
		t.Error("the right is named and read back")
	}
}

// A refused logon stops the add before the service exists, with the plain
// reason whole.
func TestLogonCheckFailureIsWhole(t *testing.T) {
	r := newRig(t)
	why := "the account crsp-box-1 cannot log on as a service: the account has no right to log on as a service (Windows error 1385: Logon failure: the user has not been granted the requested logon type at this machine.)"
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "CrspLogon", Result: sys.Result{ExitCode: 1, Stderr: errMarker + why + "\r\n"}}})
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.Contains(err.Error(), why) {
		t.Fatalf("err = %v", err)
	}
	if idx(r, "New-Service") >= 0 {
		t.Error("no service after a refused logon")
	}
}

// The text of a failed Start-Service is kept whole: the inner reason is there
// and no CLIXML is.
func TestServiceFailureTextWhole(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "New-Service", Result: sys.Result{ExitCode: 1, Stderr: errMarker + logonText + "\r\n"}}})
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	if err == nil || !strings.Contains(err.Error(), "register service: powershell exited 1: "+logonText) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "CLIXML") || strings.Contains(err.Error(), "\n") {
		t.Errorf("err not plain: %q", err)
	}
}

func TestPlainErrorDecodesCLIXML(t *testing.T) {
	x := `#< CLIXML` + "\r\n" + `<Objs Version="1.1.0.1"><S S="Error">Start-Service : Service &apos;X&apos; cannot be started_x000D__x000A_</S><S S="Error">At line:1 char:1_x000D__x000A_</S></Objs>`
	got := plainError(x, "")
	if got != "Start-Service : Service 'X' cannot be started At line:1 char:1" {
		t.Errorf("got %q", got)
	}
	if got := plainError("a\r\nb  c\n", ""); got != "a b c" {
		t.Errorf("got %q", got)
	}
}

func TestWrapScriptCatchesInner(t *testing.T) {
	s := wrapScript("Get-Date\n")
	for _, want := range []string{"try {", "catch {", "InnerException", errMarker, "exit 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("wrapper lacks %q", want)
		}
	}
	r := newRig(t)
	if _, err := r.b.ps(t.Context(), "Get-Date", nil, nil); err != nil || !strings.Contains(r.cmd.Calls[0].Stdin, "Get-Date") {
		t.Fatalf("calls = %v err = %v", r.cmd.Calls, err)
	}
}

// A failed add removes the service, the account and the folder, and says so.
func TestFailedAddCleansUp(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "New-Service", Result: sys.Result{ExitCode: 1, Stderr: errMarker + logonText}}})
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN", Removal: "RMTOKEN"})
	var sf *StartFailure
	if !errors.As(err, &sf) {
		t.Fatalf("err = %v", err)
	}
	if len(sf.Left) != 0 {
		t.Errorf("left = %v", sf.Left)
	}
	text := err.Error()
	for _, want := range []string{logonText, "Cleaned up: service ", "GitHub registration box-1", "account crsp-box-1", `folder C:\ProgramData\crsp\runners\box-1`} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q: %s", want, text)
		}
	}
	// order: service gone, registration removed with the remove token while the
	// folder still exists, then account, then folder.
	last := len(r.cmd.Calls) - 1
	if !strings.Contains(r.cmd.Calls[last].Stdin, "Remove-LocalUser") {
		t.Errorf("last call = %s", r.cmd.Calls[last].Stdin)
	}
	if r.cmd.Calls[last-1].SecretEnv["ACTIONS_RUNNER_INPUT_TOKEN"] != "RMTOKEN" || strings.Join(r.cmd.Calls[last-1].Args, " ") != "remove" {
		t.Errorf("registration not removed with the remove token: %+v", r.cmd.Calls[last-1])
	}
	if !strings.Contains(r.cmd.Calls[last-2].Stdin, "Win32_Service") {
		t.Errorf("service not deleted before: %s", r.cmd.Calls[last-2].Stdin)
	}
}

// What cannot be cleaned is named, never hidden.
func TestFailedAddNamesWhatIsLeft(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = []sysfake.Rule{
		{Match: "Remove-LocalUser", Result: sys.Result{ExitCode: 1, Stderr: errMarker + "access denied"}},
		{Match: "New-Service", Result: sys.Result{ExitCode: 1, Stderr: errMarker + logonText}},
	}
	err := start(t, r, r.spec(), backend.StartConfig{Registration: "REGTOKEN"})
	var sf *StartFailure
	if !errors.As(err, &sf) || len(sf.Left) != 1 || !strings.Contains(sf.Left[0], "account crsp-box-1") || !strings.Contains(sf.Left[0], "access denied") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "Left behind: account crsp-box-1 (remove account: powershell exited 1: access denied)") {
		t.Errorf("text = %s", err)
	}
}

// A failure before anything was made touches nothing.
func TestFailureBeforeWorkCleansNothing(t *testing.T) {
	r := newRig(t)
	if err := start(t, r, backend.RunnerSpec{Name: "box-1"}, backend.StartConfig{Registration: "T"}); err == nil {
		t.Fatal("want an error for a bad hash")
	}
	if len(r.cmd.Calls) != 0 {
		t.Errorf("calls = %v", r.cmd.Texts())
	}
}

// TestRandomPasswordLettersAndDigitsOnly: a symbol in the password broke the
// service logon on one machine ("Credential ... is invalid", runners R5.96), so
// the password is letters and digits, with upper, lower and digit all present.
func TestRandomPasswordLettersAndDigitsOnly(t *testing.T) {
	for range 200 {
		p := RandomPassword()
		if len(p) != 32 {
			t.Fatalf("length %d: %q", len(p), p)
		}
		for _, c := range p {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
				t.Fatalf("password has %q, not a letter or digit", c)
			}
		}
		if !strings.ContainsAny(p, "ABCDEFGHJKLMNPQRSTUVWXYZ") || !strings.ContainsAny(p, "abcdefghijkmnopqrstuvwxyz") || !strings.ContainsAny(p, "23456789") {
			t.Fatalf("missing a class: %q", p)
		}
	}
}
