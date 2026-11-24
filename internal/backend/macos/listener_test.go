package macos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/persistent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys/sysfake"
)

// gatedRules is the launchd list plus the listener and worker lists (exit 1: none).
func gatedRules(listener, worker string) []sysfake.Rule {
	none := func(match, out string) sysfake.Rule {
		if out == "" {
			return sysfake.Rule{Match: match, Result: sys.Result{ExitCode: 1}}
		}
		return sysfake.Rule{Match: match, Result: sys.Result{Stdout: out}}
	}
	return append(findRules(), none("Runner\\.Worker", worker), none("Runner\\.Listener", listener))
}

func TestListenerStartBootstrapsTheDaemon(t *testing.T) {
	r := started(t)
	r.cmd.Rules = gatedRules("", "")
	n := len(r.cmd.Texts())
	if err := r.b.StartListener(context.Background(), label, false); err != nil {
		t.Fatal(err)
	}
	var got bool
	for _, c := range r.cmd.Texts()[n:] {
		got = got || c == "launchctl bootstrap system /Library/LaunchDaemons/"+label+".plist"
	}
	if !got {
		t.Errorf("not bootstrapped: %v", r.cmd.Texts()[n:])
	}
}

func TestListenerStopBootsOutOnlyWithoutAWorker(t *testing.T) {
	r := started(t)
	r.cmd.Rules = gatedRules("200\n", "201\n")
	n := len(r.cmd.Texts())
	if err := r.b.StopListener(context.Background(), label, false); !errors.Is(err, persistent.ErrJobAssigned) {
		t.Fatalf("err %v", err)
	}
	for _, c := range r.cmd.Texts()[n:] {
		if strings.HasPrefix(c, "launchctl bootout") {
			t.Errorf("unloaded under a job: %s", c)
		}
	}
	if st, err := r.b.ListenerState(context.Background(), label); err != nil || st != backend.ListenerBusy {
		t.Errorf("state %q %v", st, err)
	}
}
