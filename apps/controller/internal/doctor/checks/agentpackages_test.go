package checks

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type agentPkgFake struct {
	st  doctor.AgentPackageStatus
	err error
}

func (a agentPkgFake) AgentPackages(context.Context) (doctor.AgentPackageStatus, error) {
	return a.st, a.err
}

// CRSP348: ok when the release holds archives and native packages, warn for a missing native package,
// fail for a missing archive, skip when the release is unknown.
func TestAgentPackages(t *testing.T) {
	cases := []struct {
		name string
		env  doctor.Env
		want doctor.Severity
	}{
		{"no source", doctor.Env{}, doctor.Skip},
		{"release unknown", doctor.Env{AgentPackages: agentPkgFake{err: errors.New("offline")}}, doctor.Skip},
		{"complete", doctor.Env{AgentPackages: agentPkgFake{st: doctor.AgentPackageStatus{Version: "1.2.3"}}}, doctor.OK},
		{"native missing", doctor.Env{AgentPackages: agentPkgFake{st: doctor.AgentPackageStatus{Version: "1.2.3", MissingNative: []string{"win-x64.msi"}}}}, doctor.Warn},
		{"archive missing", doctor.Env{AgentPackages: agentPkgFake{st: doctor.AgentPackageStatus{Version: "1.2.3", MissingArchives: []string{"linux-arm64"}, MissingNative: []string{"linux-arm64.deb"}}}}, doctor.Fail},
	}
	for _, c := range cases {
		got := run(t, c.env, "install.agent_packages")
		if len(got) != 1 || got[0].Severity != c.want {
			t.Fatalf("%s: %+v", c.name, got)
		}
	}
}
