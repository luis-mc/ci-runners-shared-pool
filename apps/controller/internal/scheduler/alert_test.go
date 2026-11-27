package scheduler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// R5.61: a job that ends without ever having taken a slot raises an alert; a normal job does not.
func TestJobEndedWithoutJobStartedRaisesAnAlert(t *testing.T) {
	e := newEnv(t)
	var got []scheduler.Alert
	eng := scheduler.NewEngine(e.st, scheduler.WithAlerts(func(a scheduler.Alert) { got = append(got, a) }))
	ctx := context.Background()

	// job-started never reached the gate: the hook's crsp was missing
	ghost := job("o/r", 7, "build")
	ghost.RunnerName = "crsp-runner-1"
	if freed, err := eng.Release(ctx, m1, ghost, nil); err != nil || len(freed) != 0 {
		t.Fatalf("freed %v err %v", freed, err)
	}
	if len(got) != 1 || got[0].Kind != scheduler.AlertJobStartedMissing || got[0].MachineID != m1 ||
		got[0].Job.RunnerName != "crsp-runner-1" || got[0].Job.RunID != 7 ||
		!strings.Contains(got[0].Message, "crsp doctor --check runner.hooks") || got[0].At.IsZero() {
		t.Fatalf("alerts %+v", got)
	}

	// a job that took a slot frees it and raises nothing
	got = nil
	j := job("o/r", 8, "build")
	d, err := eng.Gate(ctx, e.req("g", j, 1, quiet))
	if err != nil {
		t.Fatal(err)
	}
	goes(t, d)
	if freed, err := eng.Release(ctx, m1, j, nil); err != nil || len(freed) == 0 {
		t.Fatalf("freed %v err %v", freed, err)
	}
	if len(got) != 0 {
		t.Fatalf("alert for a normal job: %+v", got)
	}
}
