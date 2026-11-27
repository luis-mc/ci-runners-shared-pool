package scheduler_test

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// Runners R5.91: a runner whose clean-up failed takes no new job; once it is
// clean the same job goes at once. A runner the lookup does not know goes.
func TestGateHoldsJobsOfAnUncleanRunner(t *testing.T) {
	e := newEnv(t)
	reason := "the folder _work could not be removed"
	e.eng.SetUncleanLookup(func(_, name string) string {
		if name == "dirty-1" {
			return reason
		}
		return ""
	})
	j := job("o/r", 1, "build")
	j.RunnerName = "dirty-1"
	d := e.gate(e.req("g1", j, 1, quiet))
	waits(t, d, scheduler.WaitPaused)
	if want := "this runner is not clean yet: " + reason; d.WaitText != want {
		t.Fatalf("text %q want %q", d.WaitText, want)
	}
	if s := e.status(m1); len(s.Slots) != 0 {
		t.Fatalf("a held job took a slot: %+v", s)
	}
	// Another runner of the machine is not held.
	other := job("o/r", 2, "build")
	other.RunnerName = "clean-1"
	goes(t, e.gate(e.req("g2", other, 1, quiet)))
	// The clean-up works again: the held job goes.
	reason = ""
	goes(t, e.gate(e.req("g1", j, 1, quiet)))
}
