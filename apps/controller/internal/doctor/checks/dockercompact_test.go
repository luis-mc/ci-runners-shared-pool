package checks

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// caches R5.51 to R5.54: a Windows machine whose compact policy is on and whose agent
// has no administrator rights gets a warning with the console action; with the
// rights it is ok; with the policy off or no disk file, no finding.
func TestDockerCompactNeedsAdminRights(t *testing.T) {
	mk := func(id string, dc *doctor.DockerCompact) doctor.Machine {
		m := healthyMachine()
		m.ID, m.Name, m.OS, m.DockerCompact = id, id, "windows", dc
		return m
	}
	fl := &fleet{machines: []doctor.Machine{
		mk("noadmin", &doctor.DockerCompact{Enabled: true, CanCompact: false, FileBytes: 236 * gib, UsedBytes: 71 * gib, UsedKnown: true}),
		mk("admin", &doctor.DockerCompact{Enabled: true, CanCompact: true, FileBytes: 236 * gib}),
		mk("off", &doctor.DockerCompact{Enabled: false, CanCompact: false}),
		mk("nodisk", nil),
	}}
	fs := run(t, doctor.Env{Fleet: fl}, "machine.docker.compact")
	got := map[string]doctor.Finding{}
	for _, f := range fs {
		got[f.Subject.Name] = f
	}
	if len(got) != 2 {
		t.Fatalf("findings %+v", fs)
	}
	w := got["noadmin"]
	if w.Severity != doctor.Warn || !strings.Contains(w.Detail, "236.0 GiB") || w.Fix.Link != "/machines/noadmin" || !strings.Contains(w.Fix.Command, "--docker-compact off") {
		t.Fatalf("warning %+v", w)
	}
	if got["admin"].Severity != doctor.OK {
		t.Fatalf("admin %+v", got["admin"])
	}
}

func TestDockerCompactOfflineMachineIsSkipped(t *testing.T) {
	m := healthyMachine()
	m.Online, m.OS, m.DockerCompact = false, "windows", &doctor.DockerCompact{Enabled: true}
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{m}}}, "machine.docker.compact")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("%+v", fs)
	}
}
