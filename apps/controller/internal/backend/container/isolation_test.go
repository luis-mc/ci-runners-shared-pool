package container

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

// probeBackend is a backend whose engine and host answers are fake (R5.65).
func probeBackend(osType, hypervisor string) (*Backend, *sysfake.Commands) {
	cmd := &sysfake.Commands{Rules: []sysfake.Rule{
		{Match: "powershell", Result: sys.Result{Stdout: hypervisor + "\r\n"}},
	}}
	f := newFake()
	eng := &osTypeEngine{fakeDocker: f, osType: osType}
	return New(sys.Deps{Cmd: cmd}, eng, Config{}), cmd
}

type osTypeEngine struct {
	*fakeDocker
	osType string
}

func (e *osTypeEngine) Run(_ context.Context, args ...string) (sys.Result, error) {
	if len(args) > 0 && args[0] == "info" {
		return sys.Result{Stdout: e.osType + "\n"}, nil
	}
	return sys.Result{}, nil
}

func TestHyperVFlagBuilt(t *testing.T) {
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "img", Isolation: IsolationHyperV})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(a, "--isolation=hyperv") {
		t.Fatalf("flag missing: %v", a)
	}
}

func TestProcessIsolationStaysTheDefault(t *testing.T) {
	for _, s := range []string{"", "process", " Process "} {
		i, err := ParseIsolation(s)
		if err != nil || i != IsolationProcess {
			t.Fatalf("%q -> %q, %v", s, i, err)
		}
	}
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "img"})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range a {
		if strings.HasPrefix(x, "--isolation") {
			t.Fatalf("default adds %s", x)
		}
	}
	if _, err := ParseIsolation("vm"); !errors.Is(err, ErrIsolation) {
		t.Fatalf("unknown mode: %v", err)
	}
	plan, err := PlanPool(Config{}, backend.RunnerSpec{Pool: backend.PoolSetup{Isolation: "hyperv"}}, "o", "r", KVMFacts{})
	if err != nil || plan.Pool.Isolation != IsolationHyperV {
		t.Fatalf("plan %v %v", plan.Pool.Isolation, err)
	}
}

func TestHyperVProbeRefusalMessages(t *testing.T) {
	cases := []struct{ osType, hv, want string }{
		{"linux", "True", ReasonHyperVNotWindows},
		{"windows", "False", ReasonHyperVNoHypervisor},
	}
	for _, c := range cases {
		b, _ := probeBackend(c.osType, c.hv)
		err := b.ProbeHyperV(ctx)
		if !errors.Is(err, ErrIsolation) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s/%s: %v", c.osType, c.hv, err)
		}
	}
	b, _ := probeBackend("windows", "True")
	if err := b.ProbeHyperV(ctx); err != nil {
		t.Fatalf("usable host refused: %v", err)
	}
}

func TestProbeOnlyReads(t *testing.T) {
	b, cmd := probeBackend("windows", "True")
	if err := b.ProbeHyperV(ctx); err != nil {
		t.Fatal(err)
	}
	for _, x := range cmd.Texts() {
		for _, bad := range []string{"Enable-", "dism", "bcdedit", "Set-"} {
			if strings.Contains(x, bad) {
				t.Errorf("probe changes the host: %s", x)
			}
		}
	}
}

func TestLimitsStillAppliedWithHyperV(t *testing.T) {
	a, err := RunArgs(PoolSpec{Owner: "o", Repo: "r", ImageTag: "img", Isolation: IsolationHyperV,
		Limits: Limits{MemoryMB: 2048, CPUs: 2}, CPUSet: "0-1", CrspHostPath: "/opt/crsp"})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(a, " ")
	for _, w := range []string{"--memory 2048m", "--memory-swap 2048m", "--cpuset-cpus 0-1", "--cpus 2",
		"--mount type=bind,source=/opt,target=" + CrspDirInContainer + ",readonly"} {
		if !strings.Contains(line, w) {
			t.Errorf("missing %q in %s", w, line)
		}
	}
}
