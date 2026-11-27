package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func TestSpecFromProtoCarriesPackages(t *testing.T) {
	s := SpecFromProto(&agentv1.RunnerSpec{ExtraPackages: []string{"git", "jq"}})
	if len(s.ExtraPackages) != 2 || s.ExtraPackages[1] != "jq" {
		t.Fatalf("%v", s.ExtraPackages)
	}
}

func TestPreparePackages(t *testing.T) {
	var got []string
	l := &link{cfg: Config{InstallPackages: func(_ context.Context, s backend.RunnerSpec) error {
		got = s.ExtraPackages
		return nil
	}}}
	ctx := context.Background()
	if err := l.preparePackages(ctx, backend.RunnerSpec{ExtraPackages: []string{"git; reboot"}}, backend.StartConfig{Registration: "x"}); !errors.Is(err, runners.ErrPackageName) || got != nil {
		t.Fatalf("bad name: %v %v", err, got)
	}
	if err := l.preparePackages(ctx, backend.RunnerSpec{ExtraPackages: []string{"git"}}, backend.StartConfig{Registration: "x"}); err != nil || len(got) != 1 {
		t.Fatalf("persistent: %v %v", err, got)
	}
	got = nil
	if err := l.preparePackages(ctx, backend.RunnerSpec{ExtraPackages: []string{"git"}}, backend.StartConfig{JITConfig: "j"}); err != nil || got != nil {
		t.Fatalf("container installs here: %v %v", err, got)
	}
	none := &link{}
	if err := none.preparePackages(ctx, backend.RunnerSpec{ExtraPackages: []string{"git"}}, backend.StartConfig{Registration: "x"}); err == nil {
		t.Fatal("agent without a hook accepted packages")
	}
}

func TestMachineReportCarriesPackageFailures(t *testing.T) {
	at := time.Unix(100, 0)
	fails := func() []runners.PackageFailure {
		return []runners.PackageFailure{{SetHash: "h", Names: []string{"nope"}, Error: "boom", At: at}}
	}
	rep := buildMachineReport(context.Background(), OSSys{}, &agentv1.Facts{}, nil, fails, at)
	if len(rep.PackageFailures) != 1 || rep.PackageFailures[0].Names[0] != "nope" || rep.PackageFailures[0].FailedUnixMs != at.UnixMilli() {
		t.Fatalf("%+v", rep.PackageFailures)
	}
}
