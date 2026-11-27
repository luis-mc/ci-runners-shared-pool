package checks

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

type fakePkgSrc struct {
	fs  []PackageFailure
	err error
}

func (f fakePkgSrc) PackageFailures(context.Context) ([]PackageFailure, error) { return f.fs, f.err }

func runPkgCheck(t *testing.T, src PackageSource) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	RegisterPackages(reg, src)
	for _, c := range reg.All() {
		if c.ID() == "runner.packages" {
			return c.Run(context.Background(), doctor.Env{})
		}
	}
	t.Fatal("check not registered")
	return nil
}

func TestPackagesCheckReportsFailure(t *testing.T) {
	got := runPkgCheck(t, fakePkgSrc{fs: []PackageFailure{{MachineID: "m1", MachineName: "box", SetHash: "abc", Names: []string{"nope"}, Error: "apt-get failed"}}})
	if len(got) != 1 || got[0].Severity != doctor.Fail || got[0].Subject.ID != "m1" {
		t.Fatalf("%+v", got)
	}
	if got := runPkgCheck(t, fakePkgSrc{}); len(got) != 0 {
		t.Fatalf("clean machine reported: %+v", got)
	}
}
