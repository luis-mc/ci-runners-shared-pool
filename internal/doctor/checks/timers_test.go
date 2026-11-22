package checks

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

type timerFake struct {
	on  bool
	err error
}

func (t timerFake) TimersEnforced(context.Context) (bool, error) { return t.on, t.err }

// Doctor R5.65: observe mode is an info finding, never a warning.
func TestTimersMode(t *testing.T) {
	off := run(t, doctor.Env{Timers: timerFake{}}, "timers.mode")
	if len(off) != 1 || off[0].Severity != doctor.Info || off[0].Title != "Timers are in observe mode: jobs over the limit are reported, not stopped" || off[0].Fix.Link != "/settings" {
		t.Fatalf("observe: %+v", off)
	}
	on := run(t, doctor.Env{Timers: timerFake{on: true}}, "timers.mode")
	if len(on) != 1 || on[0].Severity != doctor.OK {
		t.Fatalf("enforce: %+v", on)
	}
	none := run(t, doctor.Env{}, "timers.mode")
	if len(none) != 1 || none[0].Severity != doctor.Skip {
		t.Fatalf("no source: %+v", none)
	}
}
