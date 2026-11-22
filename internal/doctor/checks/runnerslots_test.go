package checks

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
)

func slotMachine(id, name string, slots, peak, wait int) doctor.Machine {
	m := healthyMachine()
	m.ID, m.Name, m.JobSlots, m.RunnerPeak, m.RunnerPeakRepo, m.GateMaxWaitMin = id, name, slots, peak, "luis-mc/app", wait
	return m
}

// OPS55 R5.73, R5.75: runners above the slots, and a hold not below the job timeout, each warn with a fix.
func TestRunnerSlotsWarns(t *testing.T) {
	env := doctor.Env{Fleet: &fleet{machines: []doctor.Machine{
		slotMachine("m1", "fine", 2, 2, 2),
		slotMachine("m2", "toomany", 2, 4, 2),
		slotMachine("m3", "longhold", 2, 2, 10),
		slotMachine("m4", "unknown", 0, 9, 99),
	}}}
	sev := map[string]doctor.Severity{}
	for _, f := range forCheck(run(t, env, "runner.slots"), "runner.slots") {
		sev[f.Subject.Name] = f.Severity
		if f.Severity == doctor.Warn && (f.Fix.Text == "" || f.Fix.Link == "") {
			t.Errorf("warn without a fix: %+v", f)
		}
	}
	if sev["fine"] != doctor.OK || sev["toomany"] != doctor.Warn || sev["longhold"] != doctor.Warn {
		t.Fatalf("severities = %v", sev)
	}
	if _, ok := sev["unknown"]; ok {
		t.Fatalf("a machine with unknown slots is not checked: %v", sev)
	}
}
