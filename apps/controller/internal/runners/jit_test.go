package runners

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

func jobRunner() JobRunner {
	return JobRunner{Name: "crsp-job-1", MachineID: "m1", Arch: "x64", Backend: "docker",
		Labels: []string{"self-hosted", "linux", "x64"}, Scope: Scope{Kind: "repository", RepositoryID: 1}}
}

// R5.25: one runner per job; the JIT config goes to the agent once and is kept
// nowhere else.
func TestJitConfigNotStored(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "JIT-CONFIG-SECRET-1"
	if len(r.ag.cfgs) != 1 || r.ag.cfgs[0].JITConfig != secret || r.ag.cfgs[0].Registration != "" {
		t.Fatalf("the agent must get the JIT config and no registration token: %+v", r.ag.cfgs)
	}
	if len(r.gh.jitCalls) != 1 || r.gh.regCalls != 0 {
		t.Fatalf("one JIT call, no registration token: %d %d", len(r.gh.jitCalls), r.gh.regCalls)
	}
	if rec.Kind != "ephemeral" || rec.Isolation != "container" || rec.GitHubRunnerID != 4242 {
		t.Fatalf("record %+v", rec)
	}
	// Nothing the controller keeps or shows holds the config.
	stored, _ := r.store.ListRunners(context.Background(), Filter{})
	dump := fmt.Sprintf("%+v %+v %v %v %+v", stored, rec, r.store.events, r.au.entries, r.ag.added)
	if strings.Contains(dump, secret) {
		t.Fatalf("the JIT config reached the store, an event, the audit or the spec: %s", dump)
	}
}

func TestJitFailureIsNamedAndLeavesNoSecret(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.gh.jitErr = errors.New("github: 403")
	_, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc")
	if err == nil || !strings.Contains(err.Error(), "JIT config") {
		t.Fatalf("err = %v", err)
	}
	if len(r.ag.added) != 0 {
		t.Fatal("the agent must not start a runner with no config")
	}
	stored, _ := r.store.ListRunners(context.Background(), Filter{})
	if len(stored) != 1 || stored[0].State != Failed {
		t.Fatalf("the failed record must say Failed: %+v", stored)
	}
}

func TestJitRunnerRefusesProjectLabels(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	j := jobRunner()
	j.Labels = []string{"self-hosted", "my-project"}
	if _, err := r.svc.StartJobRunner(context.Background(), j, "u"); err == nil {
		t.Fatal("a project label must be refused")
	}
	if len(r.gh.jitCalls) != 0 {
		t.Fatal("no JIT call for a refused request")
	}
}

// Question 33: the pool size of the machine goes to the agent as the limits
// of the repository's pool container.
func TestJitRunnerCarriesPoolSize(t *testing.T) {
	m := machine("linux", "x64", true)
	m.PoolCPUs, m.PoolMemoryGiB = 6, 12
	r := newRig(m)
	if _, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc"); err != nil {
		t.Fatal(err)
	}
	if got := r.ag.added[0]; got.PoolCPUs != 6 || got.PoolMemoryMB != 12288 {
		t.Fatalf("pool size not passed: %+v", got)
	}
}

func TestJitRunnerWithNoPoolSizeSetsNoLimit(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	if _, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc"); err != nil {
		t.Fatal(err)
	}
	if got := r.ag.added[0]; got.PoolCPUs != 0 || got.PoolMemoryMB != 0 {
		t.Fatalf("a limit was set: %+v", got)
	}
}

// CRSP68: the pool settings, the repository id (the lane) and the CPU count
// of the machine reach the agent in the runner spec.
func TestJitRunnerCarriesPoolSetupAndCPUCount(t *testing.T) {
	m := machine("linux", "x64", true)
	m.PoolCPUs, m.JobSlots = 8, 2
	r := newRig(m)
	r.svc.PoolSettings = func(_ context.Context, sc Scope) (backend.PoolSetup, error) {
		if sc.RepositoryID != 1 {
			t.Errorf("settings asked for %+v", sc)
		}
		return backend.PoolSetup{Layers: []string{"browsers"}, Database: &backend.PoolDatabase{CreateDB: true}}, nil
	}
	if _, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc"); err != nil {
		t.Fatal(err)
	}
	got := r.ag.added[0]
	if got.MaxParallel != 4 || got.Pool.RepoID != "1" || len(got.Pool.Layers) != 1 || got.Pool.Database == nil || !got.Pool.Database.CreateDB {
		t.Fatalf("spec = %+v", got)
	}
}

func TestJitRunnerPoolSettingsFailureStartsNothing(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.svc.PoolSettings = func(context.Context, Scope) (backend.PoolSetup, error) {
		return backend.PoolSetup{}, errors.New("store down")
	}
	if _, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "luis-mc"); err == nil || !strings.Contains(err.Error(), "pool settings") {
		t.Fatalf("err = %v", err)
	}
	if len(r.ag.added) != 0 {
		t.Fatal("a runner started with unknown pool settings")
	}
}

func TestRoutingLabelPassesValidation(t *testing.T) {
	got, err := ValidateLabels([]string{"self-hosted", "linux", "crsp-e2e-ab12"}, "crsp-home", nil)
	if err != nil || len(got) != 3 {
		t.Fatalf("a job's routing label is generic: %v %v", got, err)
	}
	if _, err := ValidateLabels([]string{"self-hosted", "my-project"}, "crsp-home", nil); err == nil {
		t.Fatal("a project label stays refused")
	}
	many := make([]string, 0, MaxLabels+1)
	for i := 0; i <= MaxLabels; i++ {
		many = append(many, fmt.Sprintf("crsp-j%d-%04x", i, i))
	}
	if _, err := ValidateLabels(many, "", nil); err == nil {
		t.Fatal("the 101st label is refused")
	}
}

// OPS89: a start the agent refuses deletes the GitHub registration of the JIT
// runner, so the next try does not meet 409.
func TestFailedStartDeletesTheRegistration(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	r.ag.addErr = errors.New("own lane: chown not found")
	if _, err := r.svc.StartJobRunner(context.Background(), jobRunner(), "u"); err == nil {
		t.Fatal("the start must fail")
	}
	if len(r.gh.deleted) != 1 || r.gh.deleted[0] != 4242 {
		t.Fatalf("registration not deleted: %v", r.gh.deleted)
	}
}

// OPS89: the tries of one job leave one failed row, and the end of the job clears it.
func TestOneFailedRowForEachJob(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	r.ag.addErr = errors.New("engine down")
	for i := 1; i <= 3; i++ {
		j := jobRunner()
		j.Name, j.JobKey = fmt.Sprintf("crsp-job-9-%d", i), "crsp-job-9-"
		_, _ = r.svc.StartJobRunner(ctx, j, "u")
	}
	other := jobRunner()
	other.Name, other.JobKey = "crsp-job-10-1", "crsp-job-10-"
	_, _ = r.svc.StartJobRunner(ctx, other, "u")
	rows, _ := r.store.ListRunners(ctx, Filter{State: string(Failed)})
	if len(rows) != 2 {
		t.Fatalf("failed rows = %d, want 1 for each of 2 jobs: %+v", len(rows), rows)
	}
	if err := r.svc.ClearFailedStarts(ctx, "crsp-job-9-"); err != nil {
		t.Fatal(err)
	}
	rows, _ = r.store.ListRunners(ctx, Filter{State: string(Failed)})
	if len(rows) != 1 || rows[0].Name != "crsp-job-10-1" {
		t.Fatalf("after the job ended: %+v", rows)
	}
}
