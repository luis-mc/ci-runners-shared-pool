package windows

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

// scripted answers each call from a function, so a test can fail the first
// Start-Service and let the second pass. It records like sysfake.Commands.
type scripted struct {
	mu     sync.Mutex
	calls  []sys.Command
	fn     func(n int, c sys.Command) sys.Result
	starts int
}

func (s *scripted) Run(_ context.Context, c sys.Command) (sys.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, c)
	if strings.Contains(c.Stdin, "Start-Service") {
		s.starts++
		return s.fn(s.starts, c), nil
	}
	return s.fn(0, c), nil
}

func (s *scripted) count(marker string) int {
	n := 0
	for _, c := range s.calls {
		if strings.Contains(c.Stdin, marker) {
			n++
		}
	}
	return n
}

const logon1069 = errMarker + "Cannot start service actions.runner.luis-mc-demo.box-1 on computer '.'. Logon failure: the user has not been granted the requested logon type at this computer (Windows error 1069)"

// stoppedRig is a rig whose runner is installed, plus a Find answer that says
// its service is stopped.
func stoppedRig(t *testing.T, fn func(n int, c sys.Command) sys.Result) (*rig, *scripted, string) {
	t.Helper()
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	svc := persistent.ServiceName(spec.GitHubURL, spec.Name)
	row := `{"Name":"` + svc + `","State":"Stopped","PathName":"\"C:\\ProgramData\\crsp\\runners\\box-1\\bin\\RunnerService.exe\"","StartName":".\\crsp-box-1"}`
	sc := &scripted{}
	sc.fn = func(n int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "Name LIKE") {
			return sys.Result{Stdout: row}
		}
		return fn(n, c)
	}
	d := r.b.d
	d.Cmd = sc
	r.b = New(d, Config{Root: r.b.cfg.Root, NewPassword: r.b.cfg.NewPassword})
	return r, sc, svc
}

// R5.89: the right is given again before a stopped service is started, with
// the LSA call of account creation and no other right touched.
func TestHealGivesRightBeforeStart(t *testing.T) {
	r, sc, svc := stoppedRig(t, func(_ int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "LsaAddAccountRights") {
			return sys.Result{Stdout: "regranted=True\r\n"}
		}
		return sys.Result{}
	})
	got, err := r.b.HealServices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	h := got[0]
	if h.Service != svc || h.Account != `.\crsp-box-1` || !h.Running || !h.Regranted || !h.LogonHeld || h.Problem != "" {
		t.Errorf("health = %+v", h)
	}
	var grant, startAt = -1, -1
	for i, c := range sc.calls {
		if strings.Contains(c.Stdin, "LsaAddAccountRights") && grant < 0 {
			grant = i
			if c.Env["CRSP_ACCOUNT"] != "crsp-box-1" || !strings.Contains(c.Stdin, "SeDenyInteractiveLogonRight") {
				t.Errorf("grant env %v", c.Env)
			}
			if strings.Contains(c.Stdin, "SeCreateSymbolicLinkPrivilege") {
				t.Error("the symlink right is not touched by a re-grant")
			}
		}
		if strings.Contains(c.Stdin, "Start-Service") && startAt < 0 {
			startAt = i
		}
	}
	if grant < 0 || startAt < 0 || grant > startAt {
		t.Errorf("order: grant %d, start %d", grant, startAt)
	}
}

// R5.89: a start that fails with the logon failure (1069) is retried once,
// after the right is given again, and then runs.
func TestStartRetriesOnceOnLogonFailure(t *testing.T) {
	r, sc, _ := stoppedRig(t, func(n int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "Start-Service") && n == 1 {
			return sys.Result{ExitCode: 1, Stderr: logon1069 + "\r\n"}
		}
		return sys.Result{Stdout: "regranted=False\r\n"}
	})
	got, err := r.b.HealServices(context.Background())
	if err != nil {
		t.Fatalf("a retry that works is not an error: %v", err)
	}
	if !got[0].Running || got[0].Problem != "" {
		t.Errorf("health = %+v", got[0])
	}
	if sc.starts != 2 {
		t.Errorf("starts = %d, want 2 (one retry)", sc.starts)
	}
	// Right before start, then again after the 1069.
	if n := sc.count("LsaAddAccountRights"); n != 2 {
		t.Errorf("grants = %d, want 2", n)
	}
}

// R5.89: a second logon failure is the answer: no third start, the reason
// comes back whole for the controller to show.
func TestStartGivesUpAfterOneRetry(t *testing.T) {
	r, sc, _ := stoppedRig(t, func(_ int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "Start-Service") {
			return sys.Result{ExitCode: 1, Stderr: logon1069 + "\r\n"}
		}
		return sys.Result{Stdout: "regranted=False\r\n"}
	})
	got, err := r.b.HealServices(context.Background())
	if err == nil {
		t.Fatal("a service that does not start is an error")
	}
	if sc.starts != 2 {
		t.Errorf("starts = %d, want 2", sc.starts)
	}
	if got[0].Running || !strings.Contains(got[0].Problem, "Logon failure") {
		t.Errorf("health = %+v", got[0])
	}
}

// A start that fails for another reason is not retried.
func TestOtherStartFailureIsNotRetried(t *testing.T) {
	r, sc, _ := stoppedRig(t, func(_ int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "Start-Service") {
			return sys.Result{ExitCode: 1, Stderr: errMarker + "The service cannot be started, because it is disabled\r\n"}
		}
		return sys.Result{}
	})
	if _, err := r.b.HealServices(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if sc.starts != 1 {
		t.Errorf("starts = %d, want 1", sc.starts)
	}
}

// A running service keeps running: its right is checked, nothing is started.
func TestHealLeavesRunningServiceAlone(t *testing.T) {
	r := newRig(t)
	spec := r.spec()
	if err := start(t, r, spec, backend.StartConfig{Registration: "REGTOKEN"}); err != nil {
		t.Fatal(err)
	}
	svc := persistent.ServiceName(spec.GitHubURL, spec.Name)
	r.cmd.Calls = nil
	r.cmd.Rules = withSID([]sysfake.Rule{
		{Match: "Name LIKE", Result: sys.Result{Stdout: `{"Name":"` + svc + `","State":"Running","PathName":"\"C:\\ProgramData\\crsp\\runners\\box-1\\bin\\RunnerService.exe\"","StartName":".\\crsp-box-1"}`}},
		{Match: "LsaAddAccountRights", Result: sys.Result{Stdout: "regranted=False\r\n"}},
	})
	got, err := r.b.HealServices(context.Background())
	if err != nil || len(got) != 1 || !got[0].Running || got[0].Regranted {
		t.Fatalf("got %+v err %v", got, err)
	}
	if idx(r, "Start-Service") >= 0 {
		t.Error("a running service is not started")
	}
}

// Only managed crsp accounts are touched: a runner someone else made is left alone.
func TestHealSkipsForeignRunners(t *testing.T) {
	r := newRig(t)
	r.cmd.Rules = withSID([]sysfake.Rule{{Match: "Name LIKE", Result: sys.Result{Stdout: `{"Name":"actions.runner.a-b.x","State":"Stopped","PathName":"\"C:\\other\\bin\\RunnerService.exe\"","StartName":".\\someone"}`}}})
	got, err := r.b.HealServices(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v err %v", got, err)
	}
	if idx(r, "LsaAddAccountRights") >= 0 || idx(r, "Start-Service") >= 0 {
		t.Error("a foreign account was touched")
	}
}

func TestEnsureRefusesForeignAccount(t *testing.T) {
	r := newRig(t)
	if _, err := r.b.EnsureLogonRight(context.Background(), `.\Administrator`); err == nil {
		t.Fatal("a non-crsp account must be refused")
	}
	if len(r.cmd.Calls) != 0 {
		t.Error("nothing runs for a foreign account")
	}
}

func TestIsLogonFailure(t *testing.T) {
	for msg, want := range map[string]bool{
		logon1069: true, "Logon failure: the user has not been granted the requested logon type at this computer": true,
		"The service cannot be started, because it is disabled": false, "": false,
	} {
		if got := isLogonFailure(msg); got != want {
			t.Errorf("%q = %v", msg, got)
		}
	}
}

// R5.156: a stopped service is started only after the runner group may list
// the folders above the runner (once per pass).
func TestHealGivesTheListRightBeforeStart(t *testing.T) {
	r, sc, _ := stoppedRig(t, func(_ int, c sys.Command) sys.Result {
		if strings.Contains(c.Stdin, "LsaAddAccountRights") {
			return sys.Result{Stdout: "regranted=False\r\n"}
		}
		return sys.Result{}
	})
	if _, err := r.b.HealServices(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, startAt, n := -1, -1, 0
	for i, c := range sc.calls {
		if isListRight(c) {
			n++
			if list < 0 {
				list = i
			}
		}
		if strings.Contains(c.Stdin, "Start-Service") && startAt < 0 {
			startAt = i
		}
	}
	if n != 2 || list < 0 || startAt < list {
		t.Errorf("list rights %d (first at %d), start at %d", n, list, startAt)
	}
}
