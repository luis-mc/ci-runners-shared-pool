package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
)

// winDockerMachine is a Windows machine with Docker Desktop: persistent runner
// allowed, container backend usable, and the engine's container OS as given.
func winDockerMachine(containerOS string, dockerAllowed bool) Machine {
	m := machine("windows", "x64", true)
	m.ContainerOS = containerOS
	os := "windows"
	if containerOS != "" {
		os = containerOS
	}
	m.Backends = append(m.Backends, BackendOption{Name: backend.Docker, Usable: true, Allowed: dockerAllowed,
		Kinds: []string{"ephemeral"}, OS: []string{os}})
	return m
}

// CRSP127, R5.47: a Windows machine whose engine runs Linux containers takes a
// Linux container runner (self-hosted, linux, x64 and the pool label); a
// Windows machine without such an engine refuses Linux.
func TestLinuxOfferedOnWindowsWithLinuxContainers(t *testing.T) {
	ctx := context.Background()
	req := AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: repoScope}

	r := newRig(winDockerMachine("linux", true))
	p, err := r.svc.PlanAdd(ctx, req, "u")
	if err != nil {
		t.Fatal(err)
	}
	a := p.Adds[0]
	if a.Backend != "docker" || a.OS != "linux" || a.Kind != "ephemeral" {
		t.Fatalf("spec %+v", a)
	}
	if got := strings.Join(a.Labels, ","); got != "ephemeral,linux,pool-a,self-hosted,x64" {
		t.Fatalf("labels %s", got)
	}
	if !strings.Contains(p.Steps[0].Text, "Linux") || !strings.Contains(p.Steps[0].Text, "container") {
		t.Fatalf("text %q", p.Steps[0].Text)
	}

	// Windows without an engine that runs Linux containers: refused.
	for name, m := range map[string]Machine{
		"no engine":          winDockerMachine("", true),
		"windows containers": winDockerMachine("windows", true),
	} {
		_, err := newRig(m).svc.PlanAdd(ctx, req, "u")
		var re *RefusalError
		if !errors.As(err, &re) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The container backend is off by default: refused, naming it.
	_, err = newRig(winDockerMachine("linux", false)).svc.PlanAdd(ctx, req, "u")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "container backend is not allowed") {
		t.Fatalf("not allowed: %v", err)
	}
	// A persistent Linux runner is accepted on Windows (R5.93): a long-lived container.
	req.Kind = "persistent"
	pp, err := newRig(winDockerMachine("linux", true)).svc.PlanAdd(ctx, req, "u")
	if err != nil || pp.Adds[0].Backend != "docker" || !pp.Adds[0].KeepDependencies {
		t.Fatalf("persistent: %+v %v", pp, err)
	}
	for _, l := range pp.Adds[0].Labels {
		if l == EphemeralLabel {
			t.Fatalf("a persistent runner carries %q: %v", l, pp.Adds[0].Labels)
		}
	}
	// Refused with a reason when the container backend is off.
	if _, err = newRig(winDockerMachine("linux", false)).svc.PlanAdd(ctx, req, "u"); !errors.As(err, &re) || !strings.Contains(re.Reason, "container backend is not allowed") {
		t.Fatalf("persistent, backend off: %v", err)
	}
	// Windows runners on that machine keep working as before.
	req.OS = "windows"
	if p, err := newRig(winDockerMachine("linux", true)).svc.PlanAdd(ctx, req, "u"); err != nil || p.Adds[0].Backend != "persistent" {
		t.Fatalf("windows runner: %+v %v", p, err)
	}
}

// OPS27: the console adds a Linux runner of the pool to a Windows machine as an ephemeral container runner. The
// plan accepts it, applying stores a pool runner, and a refusal comes at plan time with its reason.
func TestPoolLinuxContainerRunnerOnWindowsPlansAndApplies(t *testing.T) {
	ctx := context.Background()
	poolReq := AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}

	r := newRig(winDockerMachine("linux", true))
	p, err := r.svc.PlanAdd(ctx, poolReq, "u")
	if err != nil || len(p.Steps) != 1 {
		t.Fatalf("plan %+v %v", p, err)
	}
	if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
		t.Fatalf("apply %+v", got)
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	if len(all) != 1 || !IsPoolRunner(all[0]) || all[0].Kind != "ephemeral" || all[0].Backend != "docker" {
		t.Fatalf("runners %+v", all)
	}

	// Not allowed on the machine: refused when the plan is made, naming the backend.
	var re *RefusalError
	_, err = newRig(winDockerMachine("linux", false)).svc.PlanAdd(ctx, poolReq, "u")
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "container backend is not allowed") {
		t.Fatalf("not allowed: %v", err)
	}
}

// OPS87 (R5.93): a persistent Linux container runner counts against the machine's job slots like any other
// runner, so a plan over the slots is refused.
func TestPersistentLinuxOnWindowsCountsAgainstTheJobSlots(t *testing.T) {
	ctx := context.Background()
	m := winDockerMachine("linux", true)
	m.JobSlots = 2
	r := newRig(m)
	req := AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 2, Scope: Scope{Kind: ScopePool}}
	p, err := r.svc.PlanAdd(ctx, req, "u")
	if err != nil {
		t.Fatalf("two on two slots: %v", err)
	}
	if got, _ := r.svc.Apply(ctx, p.ID, nil, "u"); got.State != PlanDone {
		t.Fatalf("apply %+v", got)
	}
	req.Count = 1
	_, err = r.svc.PlanAdd(ctx, req, "u")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "2 job slots") {
		t.Fatalf("third runner on two slots: %v", err)
	}
}

// OPS87: a default pool name carries its system, so a Windows runner never takes the name a removed Linux runner had.
func TestDefaultPoolNameCarriesTheSystem(t *testing.T) {
	ctx := context.Background()
	pool := Scope{Kind: ScopePool}
	for _, c := range []struct{ os, want string }{{"linux", "win-box-pool-1"}, {"windows", "win-box-win-1"}} {
		m := winDockerMachine("linux", true)
		m.Name = "win-box"
		p, err := newRig(m).svc.PlanAdd(ctx, AddRequest{OS: c.os, Kind: "persistent", MachineID: "m1", Count: 1, Scope: pool}, "u")
		if err != nil || p.Adds[0].Name != c.want {
			t.Fatalf("%s: %+v %v", c.os, p, err)
		}
	}
	if got := nameInfix(pool, "macos"); got != "-mac" {
		t.Fatalf("macos %q", got)
	}
	if got := nameInfix(Scope{Kind: "repository"}, "windows"); got != "" {
		t.Fatalf("repository scope %q", got)
	}
}
