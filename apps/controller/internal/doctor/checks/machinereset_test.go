package checks

import (
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// OPS59 R5.61: a machine with signs of a reset warns with a fix; one without is fine.
func TestMachineResetWarns(t *testing.T) {
	fine, reset := healthyMachine(), healthyMachine()
	fine.ID, fine.Name = "m1", "fine"
	reset.ID, reset.Name, reset.ResetSigns = "m2", "pc", []string{"it joined again"}
	env := doctor.Env{Fleet: &fleet{machines: []doctor.Machine{fine, reset}}}
	sev := map[string]doctor.Finding{}
	for _, f := range forCheck(run(t, env, "machine.reset"), "machine.reset") {
		sev[f.Subject.Name] = f
	}
	if sev["fine"].Severity != doctor.OK || sev["pc"].Severity != doctor.Warn || sev["pc"].Fix.Text == "" || sev["pc"].Fix.Link == "" {
		t.Fatalf("findings = %+v", sev)
	}
}
