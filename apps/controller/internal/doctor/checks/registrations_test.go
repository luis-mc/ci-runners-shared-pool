package checks

import (
	"fmt"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

func registrations(machine string, n int) []doctor.Runner {
	var out []doctor.Runner
	for i := 0; i < n; i++ {
		out = append(out, doctor.Runner{ID: fmt.Sprintf("%s-%d", machine, i), Name: fmt.Sprintf("r%d", i), MachineID: machine, Kind: "persistent"})
	}
	return out
}

// OPS23: a finding when the registrations of one machine exceed 20.
func TestRunnerRegistrationsLimit(t *testing.T) {
	busy := healthyMachine()
	busy.ID, busy.Name = "m2", "beta"
	rs := append(registrations("m1", MaxRegistrationsPerMachine), registrations("m2", MaxRegistrationsPerMachine+1)...)
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine(), busy}, runners: rs}}, "runner.registrations")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
		if f.Severity == doctor.Warn && (f.Fix.Text == "" || f.Fix.Link == "") {
			t.Errorf("warn without a fix: %+v", f)
		}
	}
	if sev["alpha"] != doctor.OK || sev["beta"] != doctor.Warn {
		t.Fatalf("20 registrations are fine, 21 are a finding: %v", sev)
	}
}

func TestRunnerRegistrationsIgnoresMachinesWithoutRunners(t *testing.T) {
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}}}, "runner.registrations")
	if len(forCheck(fs, "runner.registrations")) != 0 {
		t.Fatalf("findings = %+v", fs)
	}
}
