package checks

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

type gateFake struct{ on bool }

func (g gateFake) GateEnforced(context.Context) (bool, error) { return g.on, nil }

// Doctor R5.66: observe mode is an info finding, never a warning.
func TestListenerGateMode(t *testing.T) {
	off := run(t, doctor.Env{ListenerGate: gateFake{}}, "listener.gate")
	if len(off) != 1 || off[0].Severity != doctor.Info || off[0].Fix.Link != "/settings" {
		t.Fatalf("observe: %+v", off)
	}
	on := run(t, doctor.Env{ListenerGate: gateFake{on: true}}, "listener.gate")
	if len(on) != 1 || on[0].Severity != doctor.OK {
		t.Fatalf("enforce: %+v", on)
	}
	none := run(t, doctor.Env{}, "listener.gate")
	if len(none) != 1 || none[0].Severity != doctor.Skip {
		t.Fatalf("no source: %+v", none)
	}
}
