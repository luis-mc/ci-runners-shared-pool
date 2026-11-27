package linux

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

const gatedUnit = "actions.runner.luis-mc-demo.box-1"

// pgrepRules answers the listener and worker lists: exit 1 is "none".
func pgrepRules(listener, worker string) []sysfake.Rule {
	found := []sysfake.Rule{
		{Match: "list-unit-files", Result: sys.Result{Stdout: gatedUnit + ".service enabled\n"}},
		{Match: "show " + gatedUnit, Result: sys.Result{Stdout: "User=crsp-box-1\nWorkingDirectory=/var/lib/crsp/runners/box-1\nActiveState=active\n"}},
	}
	rule := func(match, out string) sysfake.Rule {
		if out == "" {
			return sysfake.Rule{Match: match, Result: sys.Result{ExitCode: 1}}
		}
		return sysfake.Rule{Match: match, Result: sys.Result{Stdout: out}}
	}
	return append(found, rule("Runner\\.Worker", worker), rule("Runner\\.Listener", listener))
}

func TestListenerStartUsesSystemctlStart(t *testing.T) {
	r := started(t)
	r.cmd.Rules = pgrepRules("", "")
	n := len(r.cmd.Texts())
	if err := r.b.StartListener(context.Background(), gatedUnit, false); err != nil {
		t.Fatal(err)
	}
	var got bool
	for _, c := range r.cmd.Texts()[n:] {
		got = got || c == "systemctl start "+gatedUnit+".service"
	}
	if !got {
		t.Errorf("no start after the check: %v", r.cmd.Texts()[n:])
	}
}

func TestListenerStopRefusedWhileAWorkerRuns(t *testing.T) {
	r := started(t)
	r.cmd.Rules = pgrepRules("100\n", "101\n")
	n := len(r.cmd.Texts())
	err := r.b.StopListener(context.Background(), gatedUnit, false)
	if !errors.Is(err, persistent.ErrJobAssigned) {
		t.Fatalf("err %v", err)
	}
	for _, c := range r.cmd.Texts()[n:] {
		if strings.HasPrefix(c, "systemctl stop") {
			t.Errorf("stopped under a job: %s", c)
		}
	}
	st, err := r.b.ListenerState(context.Background(), gatedUnit)
	if err != nil || st != backend.ListenerBusy {
		t.Errorf("state %q %v", st, err)
	}
}
