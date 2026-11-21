package checks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func i64(v int64) *int64 { return &v }

type fleet struct {
	machines []doctor.Machine
	runners  []doctor.Runner
	services map[string][]doctor.OSService
	svcErr   map[string]error
	repos    []doctor.Repository
}

func (f *fleet) Machines(context.Context) ([]doctor.Machine, error)        { return f.machines, nil }
func (f *fleet) Runners(context.Context) ([]doctor.Runner, error)          { return f.runners, nil }
func (f *fleet) Repositories(context.Context) ([]doctor.Repository, error) { return f.repos, nil }
func (f *fleet) Services(_ context.Context, id string) ([]doctor.OSService, error) {
	if err := f.svcErr[id]; err != nil {
		return nil, err
	}
	return f.services[id], nil
}

// gh is a fake GitHub: read-only by construction (doctor.GitHub has no write
// method) and it counts calls so tests can check R6.2.
type gh struct {
	mu        sync.Mutex
	calls     map[string]int
	app       github.App
	inst      map[int64]github.Installation
	instErr   map[int64]error
	runners   map[string][]github.Runner // key owner/repo or org
	runnerErr error
	vars      map[string][]github.Variable
	varsErr   error
	files     map[string][]github.WorkflowFile
}

func newGH() *gh {
	return &gh{calls: map[string]int{}, inst: map[int64]github.Installation{}, instErr: map[int64]error{},
		runners: map[string][]github.Runner{}, vars: map[string][]github.Variable{}, files: map[string][]github.WorkflowFile{}}
}

func (g *gh) count(k string) { g.mu.Lock(); g.calls[k]++; g.mu.Unlock() }

func (g *gh) GetApp(context.Context) (github.App, error) { g.count("app"); return g.app, nil }
func (g *gh) GetInstallation(_ context.Context, id int64) (github.Installation, error) {
	g.count("installation")
	return g.inst[id], g.instErr[id]
}
func (g *gh) ListRunners(_ context.Context, _ int64, sc github.RunnerScope) ([]github.Runner, error) {
	g.count("runners")
	if g.runnerErr != nil {
		return nil, g.runnerErr
	}
	if sc.Organization != "" {
		return g.runners[sc.Organization], nil
	}
	return g.runners[sc.Owner+"/"+sc.Repo], nil
}
func (g *gh) ListVariables(_ context.Context, _ int64, o, r string) ([]github.Variable, error) {
	g.count("variables")
	return g.vars[o+"/"+r], g.varsErr
}
func (g *gh) ListWorkflowFiles(_ context.Context, _ int64, o, r string, _ map[string]string) ([]github.WorkflowFile, error) {
	g.count("workflows")
	return g.files[o+"/"+r], nil
}

func healthyMachine() doctor.Machine {
	return doctor.Machine{ID: "m1", Name: "alpha", OS: "linux", Online: true, LastSeen: now.Add(-5 * time.Second), State: "active",
		Load:            &doctor.Load{MemoryPct: 30, CPUPct: 10, MemoryKnown: true, CPUKnown: true, SampledAt: now.Add(-5 * time.Second)},
		BudgetMemoryPct: 85, BudgetCPUPct: 60,
		DiskFreeBytes: i64(300 * gib), DiskTotalBytes: i64(500 * gib)}
}

// run runs the registered checks whose ID starts with prefix through the real engine.
func run(t *testing.T, env doctor.Env, prefix string) []doctor.Finding {
	t.Helper()
	reg := doctor.NewRegistry()
	Register(reg)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return env, nil }}
	r, err := e.Run(context.Background(), doctor.Scope{Checks: []string{prefix}}, doctor.TriggerCLI, "test")
	if err != nil {
		t.Fatal(err)
	}
	return r.Findings
}

// forCheck keeps the findings of one check; a prefix scope also takes its sub-checks.
func forCheck(fs []doctor.Finding, check string) []doctor.Finding {
	var out []doctor.Finding
	for _, f := range fs {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

func only(t *testing.T, fs []doctor.Finding, check string) doctor.Finding {
	t.Helper()
	var got []doctor.Finding
	for _, f := range fs {
		if f.Check == check {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one %s finding, got %d: %+v", check, len(got), fs)
	}
	return got[0]
}

func TestEveryRegisteredCheckIsM1AndHasAnID(t *testing.T) {
	reg := doctor.NewRegistry()
	Register(reg)
	if len(reg.All()) != 51 {
		t.Fatalf("checks: %v", reg.SortedIDs())
	}
	for _, c := range reg.All() {
		if c.Milestone() != 1 || c.ID() == "" {
			t.Errorf("%s: milestone %d", c.ID(), c.Milestone())
		}
	}
}

func TestMachineOnline(t *testing.T) {
	off := healthyMachine()
	off.ID, off.Name, off.Online = "m2", "beta", false
	off.LastSeen = now.Add(-10 * time.Minute)
	stale := healthyMachine()
	stale.ID, stale.Name, stale.LastSeen = "m3", "gamma", now.Add(-2*time.Minute) // online flag lags the clock
	never := healthyMachine()
	never.ID, never.Name, never.Online, never.LastSeen = "m4", "delta", false, time.Time{}
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine(), off, stale, never}}}, "machine.online")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
	}
	if sev["alpha"] != doctor.OK || sev["beta"] != doctor.Fail || sev["gamma"] != doctor.Fail || sev["delta"] != doctor.Fail {
		t.Fatalf("severities = %v", sev)
	}
	for _, f := range fs {
		if f.Severity == doctor.Fail && (f.Fix.Text == "" || f.Fix.Link == "") {
			t.Errorf("fail without fix: %+v", f)
		}
	}
}

func TestLoadFresh(t *testing.T) {
	old := healthyMachine()
	old.ID, old.Name = "m2", "beta"
	old.Load.SampledAt = now.Add(-2 * time.Minute)
	none := healthyMachine()
	none.ID, none.Name, none.Load = "m3", "gamma", nil
	off := healthyMachine()
	off.ID, off.Name, off.Online = "m4", "delta", false
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine(), old, none, off}}}, "machine.load.fresh")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
	}
	if sev["alpha"] != doctor.OK || sev["beta"] != doctor.Fail || sev["gamma"] != doctor.Fail || sev["delta"] != doctor.Skip {
		t.Fatalf("severities = %v", sev)
	}
}

// R5.9 (scheduling R5.22, D53): a machine that cannot measure its load is a fail.
func TestLoadMeasured(t *testing.T) {
	blind := healthyMachine()
	blind.ID, blind.Name = "m2", "beta"
	blind.Load.MemoryKnown, blind.Load.CPUKnown = false, false
	half := healthyMachine()
	half.ID, half.Name = "m3", "gamma"
	half.Load.CPUKnown = false
	off := healthyMachine()
	off.ID, off.Name, off.Online = "m4", "delta", false
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine(), blind, half, off}}}, "machine.load.measured")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
	}
	if sev["alpha"] != doctor.OK || sev["beta"] != doctor.Fail || sev["gamma"] != doctor.OK || sev["delta"] != doctor.Skip {
		t.Fatalf("severities = %v", sev)
	}
}

func TestLoadBudget(t *testing.T) {
	over := healthyMachine()
	over.ID, over.Name, over.OverBudgetSince = "m2", "beta", now.Add(-11*time.Minute)
	brief := healthyMachine()
	brief.ID, brief.Name, brief.OverBudgetSince = "m3", "gamma", now.Add(-5*time.Minute)
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine(), over, brief}}}, "machine.load.budget")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
	}
	if sev["alpha"] != doctor.OK || sev["beta"] != doctor.Warn || sev["gamma"] != doctor.OK {
		t.Fatalf("severities = %v", sev)
	}
}

// Scheduling R5.77: a warning from the load watch warns at once and names the
// figures, the budget and the running jobs.
func TestLoadBudgetWarnsWhileJobsRun(t *testing.T) {
	m := healthyMachine()
	m.Load = &doctor.Load{MemoryPct: 96, MemoryKnown: true, CPUPct: 20, CPUKnown: true, SampledAt: now}
	m.LoadWarnSince = now.Add(-10 * time.Second)
	m.RunningJobs = []string{"o/r build", "o/r e2e"}
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{m}}}, "machine.load.budget")
	if len(fs) != 1 || fs[0].Severity != doctor.Warn {
		t.Fatalf("findings %+v", fs)
	}
	for _, want := range []string{"Memory 96% of 85%", "CPU 20% of 60%", "o/r build, o/r e2e", "not stopped"} {
		if !strings.Contains(fs[0].Detail, want) {
			t.Fatalf("detail %q lacks %q", fs[0].Detail, want)
		}
	}
}

func TestDisk(t *testing.T) {
	mk := func(name string, free, total int64) doctor.Machine {
		m := healthyMachine()
		m.ID, m.Name, m.DiskFreeBytes, m.DiskTotalBytes = name, name, i64(free), i64(total)
		return m
	}
	unknown := healthyMachine()
	unknown.ID, unknown.Name, unknown.DiskFreeBytes = "u", "u", nil
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{
		mk("fine", 300*gib, 500*gib),
		mk("pctwarn", 70*gib, 500*gib), // 14%
		mk("gbwarn", 9*gib, 100*gib),   // under 10 GB but over 5%... 9%: warn
		mk("pctfail", 20*gib, 500*gib), // 4%
		mk("gbfail", 1*gib, 15*gib),    // 1 GB: fail
		unknown,
	}}}, "machine.disk")
	want := map[string]doctor.Severity{"fine": doctor.OK, "pctwarn": doctor.Warn, "gbwarn": doctor.Warn, "pctfail": doctor.Fail, "gbfail": doctor.Fail, "u": doctor.Skip}
	for _, f := range fs {
		if want[f.Subject.Name] != f.Severity {
			t.Errorf("%s = %s, want %s", f.Subject.Name, f.Severity, want[f.Subject.Name])
		}
		if f.Severity == doctor.Fail && !strings.Contains(f.Fix.Command, "clean-work --machine "+f.Subject.Name) {
			t.Errorf("fix command = %q", f.Fix.Command)
		}
	}
}

// Caches R5.38: the doctor reports a machine whose volume is under the guard limit.
func TestDiskGuard(t *testing.T) {
	mk := func(name string, free, total int64) doctor.Machine {
		m := healthyMachine()
		m.ID, m.Name, m.DiskFreeBytes, m.DiskTotalBytes = name, name, i64(free), i64(total)
		return m
	}
	unknown := healthyMachine()
	unknown.ID, unknown.Name, unknown.DiskFreeBytes = "u", "u", nil
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{
		mk("fine", 300*gib, 500*gib),
		mk("edge", 50*gib, 500*gib), // exactly 10%: not held
		mk("held", 49*gib, 500*gib), // 9.8%
		unknown,
	}}}, "machine.guard.disk")
	want := map[string]doctor.Severity{"fine": doctor.OK, "edge": doctor.OK, "held": doctor.Fail, "u": doctor.Skip}
	for _, f := range fs {
		if want[f.Subject.Name] != f.Severity {
			t.Errorf("%s = %s, want %s", f.Subject.Name, f.Severity, want[f.Subject.Name])
		}
		if f.Subject.Name == "held" && !strings.Contains(f.Title, "held") {
			t.Errorf("title %q", f.Title)
		}
	}
}

func persistent(id, name string) doctor.Runner {
	return doctor.Runner{ID: id, Name: name, MachineID: "m1", MachineName: "alpha", OS: "linux", Kind: "persistent",
		ServiceName: "actions.runner.o-r." + name, InstallationID: 7, GitHubRunnerID: 100,
		Scope: github.RunnerScope{Owner: "o", Repo: "r"}, FolderReadable: true,
		Labels: []string{"self-hosted", "Linux", "X64"}}
}

func TestRunnerService(t *testing.T) {
	rs := []doctor.Runner{persistent("r1", "crsp-ok"), persistent("r2", "crsp-missing"), persistent("r3", "crsp-stopped"), persistent("r4", "crsp-manual")}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: rs, services: map[string][]doctor.OSService{"m1": {
		{Name: "actions.runner.o-r.crsp-ok", RunnerName: "crsp-ok", Running: true, Automatic: true, AutomaticKnown: true},
		{Name: "actions.runner.o-r.crsp-stopped", RunnerName: "crsp-stopped", Running: false, Automatic: true, AutomaticKnown: true},
		{Name: "actions.runner.o-r.crsp-manual", RunnerName: "crsp-manual", Running: true, Automatic: false, AutomaticKnown: true},
	}}}
	fs := run(t, doctor.Env{Fleet: fl}, "runner.service")
	want := map[string]doctor.Severity{"crsp-ok": doctor.OK, "crsp-prompts": doctor.Warn, "crsp-missing": doctor.Fail, "crsp-stopped": doctor.Fail, "crsp-manual": doctor.Fail}
	for _, f := range fs {
		if want[f.Subject.Name] != f.Severity {
			t.Errorf("%s = %s", f.Subject.Name, f.Severity)
		}
	}
	// An unreachable agent is skip, never ok (R5.5).
	fl.svcErr = map[string]error{"m1": doctor.ErrUnreachable}
	for _, f := range run(t, doctor.Env{Fleet: fl}, "runner.service") {
		if f.Severity != doctor.Skip {
			t.Errorf("unreachable agent gave %s", f.Severity)
		}
	}
}

func TestRunnerDetected(t *testing.T) {
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{persistent("r1", "crsp-a")},
		services: map[string][]doctor.OSService{"m1": {
			{Name: "actions.runner.o-r.crsp-a", RunnerName: "crsp-a", Running: true},
			{Name: "actions.runner.o-r.legacy", RunnerName: "legacy", Scope: "o-r", Running: true},
		}}}
	fs := run(t, doctor.Env{Fleet: fl}, "runner.detected")
	if len(fs) != 1 || fs[0].Severity != doctor.Info || fs[0].Subject.Kind != "runner_service" || !strings.Contains(fs[0].Detail, "legacy") {
		t.Fatalf("got %+v", fs)
	}
	fl.services["m1"] = fl.services["m1"][:1]
	fs = run(t, doctor.Env{Fleet: fl}, "runner.detected")
	if len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("all managed: %+v", fs)
	}
}

func TestRunnerRegistered(t *testing.T) {
	g := newGH()
	g.runners["o/r"] = []github.Runner{
		{ID: 100, Name: "crsp-up", Status: "online"},
		{ID: 101, Name: "crsp-down", Status: "offline"},
	}
	up, down, gone := persistent("r1", "crsp-up"), persistent("r2", "crsp-down"), persistent("r3", "crsp-gone")
	down.GitHubRunnerID, gone.GitHubRunnerID = 101, 999
	up.GitHubRunnerID = 100
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{up, down, gone}}}, "runner.registered")
	want := map[string]doctor.Severity{"crsp-up": doctor.OK, "crsp-down": doctor.Fail, "crsp-gone": doctor.Fail}
	for _, f := range fs {
		if want[f.Subject.Name] != f.Severity {
			t.Errorf("%s = %s", f.Subject.Name, f.Severity)
		}
	}
	if g.calls["runners"] != 1 {
		t.Errorf("runner list read %d times, want once per run", g.calls["runners"])
	}
	// GitHub not connected and GitHub down are both skip.
	fs = run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{up}}}, "runner.registered")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("no github: %+v", fs)
	}
	g.runnerErr = github.ErrUnavailable
	fs = run(t, doctor.Env{GitHub: g, Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{up}}}, "runner.registered")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("github down: %+v", fs)
	}
}

func TestRunnerOrphan(t *testing.T) {
	g := newGH()
	g.runners["o/r"] = []github.Runner{
		{ID: 100, Name: "crsp-mine", Status: "online"},
		{ID: 555, Name: "crsp-stray", Status: "offline"},
		{ID: 556, Name: "somebody-elses", Status: "online"},
	}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{persistent("r1", "crsp-mine")}}
	fs := run(t, doctor.Env{GitHub: g, Fleet: fl}, "runner.orphan")
	if len(fs) != 1 || fs[0].Severity != doctor.Warn || fs[0].Subject.Name != "crsp-stray" {
		t.Fatalf("got %+v", fs)
	}
	g.runners["o/r"] = g.runners["o/r"][:1]
	fs = run(t, doctor.Env{GitHub: g, Fleet: fl}, "runner.orphan")
	if len(fs) != 1 || fs[0].Severity != doctor.OK {
		t.Fatalf("no strays: %+v", fs)
	}
}

// R5.13: a locked runner folder gives skip with needs_admin and the runner is
// still found by the service checks. Port of test_dl318 locked-runner tests.
func TestRunnerHooksNeedAdmin(t *testing.T) {
	locked, bad, good := persistent("r1", "crsp-locked"), persistent("r2", "crsp-bad"), persistent("r3", "crsp-good")
	locked.FolderReadable = false
	bad.HookProblems = []string{"job-started hook is a .cmd file"}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{locked, bad, good},
		services: map[string][]doctor.OSService{"m1": {{Name: locked.ServiceName, RunnerName: "crsp-locked", Running: true, Automatic: true, AutomaticKnown: true}}}}
	fs := run(t, doctor.Env{Fleet: fl}, "runner.hooks")
	got := map[string]doctor.Finding{}
	for _, f := range fs {
		got[f.Subject.Name] = f
	}
	if f := got["crsp-locked"]; f.Severity != doctor.Skip || !f.NeedsAdmin || !strings.Contains(f.Fix.Command, "--local") {
		t.Errorf("locked = %+v", f)
	}
	if got["crsp-bad"].Severity != doctor.Fail || got["crsp-bad"].NeedsAdmin || got["crsp-good"].Severity != doctor.OK {
		t.Errorf("bad=%+v good=%+v", got["crsp-bad"], got["crsp-good"])
	}
	// The locked runner's service is still found: the hook check never says "no runner".
	svc := run(t, doctor.Env{Fleet: fl}, "runner.service")
	for _, f := range svc {
		if f.Subject.Name == "crsp-locked" && f.Severity != doctor.OK {
			t.Errorf("service of locked runner = %+v", f)
		}
	}
}

func TestRunnerLabelsGeneric(t *testing.T) {
	proj := persistent("r1", "crsp-proj")
	proj.Labels = []string{"self-hosted", "Windows", "ARM64", "docker", "crsp-home", "myproject"}
	good := persistent("r2", "crsp-good")
	fs := run(t, doctor.Env{PoolName: "crsp-home", Fleet: &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{proj, good}}}, "runner.labels.generic")
	for _, f := range fs {
		switch f.Subject.Name {
		case "crsp-proj":
			if f.Severity != doctor.Warn || !strings.Contains(f.Detail, "myproject") || strings.Contains(f.Detail, "docker") || strings.Contains(f.Detail, "crsp-home") {
				t.Errorf("proj = %+v", f)
			}
		case "crsp-good":
			if f.Severity != doctor.OK {
				t.Errorf("good = %+v", f)
			}
		}
	}
	if !IsGenericLabel("GPU", "") || IsGenericLabel("crsp-home", "") {
		t.Error("IsGenericLabel")
	}
}

func repo(id int64, full string, expected map[string]string) doctor.Repository {
	o, n, _ := strings.Cut(full, "/")
	return doctor.Repository{ID: id, FullName: full, Owner: o, Name: n, InstallationID: 7, DefaultBranch: "main", Expected: expected}
}

func TestAppInstallationAndPermissions(t *testing.T) {
	g := newGH()
	full := map[string]string{"administration": "write", "actions": "read", "actions_variables": "write", "contents": "read", "metadata": "read", "secrets": "write"}
	g.inst[7] = github.Installation{ID: 7, AccountLogin: "o", AccountType: "User", Permissions: full}
	g.inst[8] = github.Installation{ID: 8, AccountLogin: "susp", Suspended: true, Permissions: full}
	g.instErr[9] = github.ErrNotFound
	g.inst[10] = github.Installation{ID: 10, AccountLogin: "org", AccountType: "Organization",
		Permissions: map[string]string{"administration": "read", "actions": "read", "contents": "read", "metadata": "read"}}
	repos := []doctor.Repository{repo(1, "o/a", nil), repo(2, "susp/b", nil), repo(3, "gone/c", nil), repo(4, "org/d", nil)}
	repos[1].InstallationID, repos[2].InstallationID, repos[3].InstallationID = 8, 9, 10
	env := doctor.Env{GitHub: g, Fleet: &fleet{repos: repos}}

	fs := run(t, env, "github.app.installation")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
	}
	if sev["o"] != doctor.OK || sev["susp"] != doctor.Fail || sev["gone"] != doctor.Fail || sev["org"] != doctor.OK {
		t.Fatalf("installation severities = %v", sev)
	}
	fs = run(t, env, "github.app.permissions")
	sev = map[string]doctor.Severity{}
	detail := ""
	for _, f := range fs {
		sev[f.Subject.Name] = f.Severity
		if f.Subject.Name == "org" {
			detail = f.Detail
		}
	}
	if sev["o"] != doctor.OK || sev["susp"] != doctor.OK || sev["gone"] != doctor.Skip || sev["org"] != doctor.Fail {
		t.Fatalf("permission severities = %v", sev)
	}
	for _, p := range []string{"administration (needs write, has read)", "actions_variables (needs write, has none)", "organization_self_hosted_runners"} {
		if !strings.Contains(detail, p) {
			t.Errorf("detail lacks %q: %s", p, detail)
		}
	}
}

func TestAppRateLimitIsSkipWithResetTime(t *testing.T) {
	g := newGH()
	g.instErr[7] = &github.RateLimitError{ResetAt: now.Add(20 * time.Minute)}
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/a", nil)}}}, "github.app.installation")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip || !strings.Contains(fs[0].Detail, "2026-11-14 12:20") {
		t.Fatalf("got %+v", fs)
	}
}

func TestRepoVars(t *testing.T) {
	g := newGH()
	exp := map[string]string{
		"RUNS_ON_LINUX":   `["self-hosted","linux","crsp-home"]`,
		"RUNS_ON_WINDOWS": `["self-hosted","windows","crsp-home"]`,
		"RUNS_ON_MACOS":   `["self-hosted","macos","crsp-home"]`,
	}
	g.vars["o/r"] = []github.Variable{
		{Name: "RUNS_ON_LINUX", Value: `["crsp-home","Linux","self-hosted"]`}, // same set, other order and case: fine
		{Name: "RUNS_ON_WINDOWS", Value: `not json`},
	} // MACOS unset
	g.files["o/r"] = []github.WorkflowFile{{Path: ".github/workflows/ci.yml", Content: []byte("jobs:\n  a:\n    runs-on: ${{ fromJSON(vars.RUNS_ON_MACOS) }}\n")}}
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", exp)}}}, "repo.vars")
	fs = forCheck(fs, "repo.vars")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.ID] = f.Severity
	}
	if sev["1:RUNS_ON_LINUX"] != doctor.OK || sev["1:RUNS_ON_WINDOWS"] != doctor.Fail || sev["1:RUNS_ON_MACOS"] != doctor.Fail {
		t.Fatalf("severities = %v", sev)
	}
	// Different from the product's value is warn; unset and unused is warn.
	g2 := newGH()
	g2.vars["o/r"] = []github.Variable{{Name: "RUNS_ON_LINUX", Value: `["self-hosted","linux"]`}}
	fs = run(t, doctor.Env{GitHub: g2, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", map[string]string{
		"RUNS_ON_LINUX": exp["RUNS_ON_LINUX"], "RUNS_ON_MACOS": exp["RUNS_ON_MACOS"]})}}}, "repo.vars")
	fs = forCheck(fs, "repo.vars")
	for _, f := range fs {
		if f.Severity != doctor.Warn || f.Fix.Command == "" || !strings.Contains(f.Fix.Command, "gh variable set") {
			t.Errorf("want warn with command: %+v", f)
		}
	}
}

func TestRepoVarsRunner(t *testing.T) {
	g := newGH()
	g.vars["o/r"] = []github.Variable{
		{Name: "RUNS_ON_LINUX", Value: `["self-hosted","linux"]`},
		{Name: "RUNS_ON_WINDOWS", Value: `["self-hosted","windows"]`},
	}
	g.runners["o/r"] = []github.Runner{
		{Name: "a", Status: "online", Labels: []string{"self-hosted", "Linux", "X64"}},
		{Name: "b", Status: "offline", Labels: []string{"self-hosted", "Windows", "X64"}}, // offline does not count
	}
	fs := run(t, doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil)}}}, "repo.vars.runner")
	sev := map[string]doctor.Severity{}
	for _, f := range fs {
		sev[f.Subject.ID] = f.Severity
	}
	if sev["1:RUNS_ON_LINUX"] != doctor.OK || sev["1:RUNS_ON_WINDOWS"] != doctor.Fail {
		t.Fatalf("severities = %v", sev)
	}
}

const ci = `name: ci
on: push
jobs:
  hosted:
    runs-on: ubuntu-latest
  literal:
    runs-on: [self-hosted, linux, arm64]
  viavar:
    runs-on: ${{ fromJSON(vars.RUNS_ON_LINUX) }}
  matrix:
    runs-on: ${{ matrix.os }}
  unsetvar:
    runs-on: ${{ fromJSON(vars.RUNS_ON_WINDOWS) }}
`

func workflowEnv() (doctor.Env, *gh) {
	g := newGH()
	g.vars["o/r"] = []github.Variable{{Name: "RUNS_ON_LINUX", Value: `["self-hosted","linux"]`}}
	g.runners["o/r"] = []github.Runner{
		{Name: "a", Status: "online", Labels: []string{"self-hosted", "Linux", "X64"}},
		{Name: "off", Status: "offline", Labels: []string{"self-hosted", "Linux", "ARM64"}},
	}
	g.files["o/r"] = []github.WorkflowFile{
		{Path: ".github/workflows/ci.yml", Content: []byte(ci)},
		{Path: ".github/workflows/notes.md", Content: []byte("# not a workflow")},
		{Path: ".github/workflows/broken.yml", Content: []byte("jobs: [unclosed")},
	}
	return doctor.Env{GitHub: g, Fleet: &fleet{repos: []doctor.Repository{repo(1, "o/r", nil)}}}, g
}

func TestWorkflowLabels(t *testing.T) {
	env, _ := workflowEnv()
	f := only(t, run(t, env, "workflow.labels"), "workflow.labels")
	if f.Severity != doctor.Fail {
		t.Fatalf("got %+v", f)
	}
	// File and line of the job nobody can run; the offline arm64 runner does not count; hosted and variable jobs are fine.
	if !strings.Contains(f.Detail, ".github/workflows/ci.yml:7 (job literal) needs [self-hosted, linux, arm64]") {
		t.Errorf("detail = %q", f.Detail)
	}
	if strings.Contains(f.Detail, "viavar") || strings.Contains(f.Detail, "hosted needs") {
		t.Errorf("detail names jobs that are fine: %q", f.Detail)
	}
	// With a runner for arm64 online it passes.
	g := env.GitHub.(*gh)
	g.runners["o/r"][1].Status = "online"
	f = only(t, run(t, env, "workflow.labels"), "workflow.labels")
	if f.Severity != doctor.OK {
		t.Fatalf("after fix: %+v", f)
	}
}

func TestWorkflowGeneric(t *testing.T) {
	env, _ := workflowEnv()
	f := only(t, run(t, env, "workflow.generic"), "workflow.generic")
	if f.Severity != doctor.Warn || !strings.Contains(f.Detail, "literal") || strings.Contains(f.Detail, "viavar") || !strings.Contains(f.Fix.Text, "fromJSON(vars.RUNS_ON") {
		t.Fatalf("got %+v", f)
	}
}

func TestWorkflowUnresolved(t *testing.T) {
	env, _ := workflowEnv()
	f := only(t, run(t, env, "workflow.unresolved"), "workflow.unresolved")
	if f.Severity != doctor.Info {
		t.Fatalf("got %+v", f)
	}
	for _, want := range []string{"matrix.os", "RUNS_ON_WINDOWS, which is not set", "broken.yml is not valid YAML"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, f.Detail)
		}
	}
}

func TestUnlistableRunnersSkip(t *testing.T) {
	env, g := workflowEnv()
	g.runnerErr = github.ErrUnavailable
	fs := run(t, env, "workflow.labels")
	if len(fs) != 1 || fs[0].Severity != doctor.Skip {
		t.Fatalf("got %+v", fs)
	}
}

// R6.2: a full run makes at most 3 REST calls per repository plus 2 per
// installation (installation read once, no extra per check).
func TestCallCountBudget(t *testing.T) {
	env, g := workflowEnv()
	g.inst[7] = github.Installation{ID: 7, AccountLogin: "o", AccountType: "User", Permissions: RequiredPermissions}
	f := env.Fleet.(*fleet)
	f.repos[0].Expected = map[string]string{"RUNS_ON_LINUX": `["self-hosted","linux"]`}
	reg := doctor.NewRegistry()
	Register(reg)
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return env, nil }}
	if _, err := e.Run(context.Background(), doctor.Scope{}, doctor.TriggerSchedule, "system"); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range g.calls {
		total += n
	}
	if g.calls["variables"] != 1 || g.calls["workflows"] != 1 || g.calls["runners"] != 1 || g.calls["installation"] != 1 || total > 3+2 {
		t.Fatalf("calls = %v", g.calls)
	}
}

func TestMachineScopeAppliesToMachineChecks(t *testing.T) {
	a, b := healthyMachine(), healthyMachine()
	b.ID, b.Name, b.Online = "m2", "beta", false
	reg := doctor.NewRegistry()
	Register(reg)
	env := doctor.Env{Fleet: &fleet{machines: []doctor.Machine{a, b}}}
	e := &doctor.Engine{Registry: reg, Store: doctor.NewMemStore(), Now: func() time.Time { return now },
		Env: func(context.Context) (doctor.Env, error) { return env, nil }}
	r, _ := e.Run(context.Background(), doctor.Scope{Machines: []string{"BETA"}, Checks: []string{"machine.online"}}, doctor.TriggerCLI, "")
	if len(r.Findings) != 1 || r.Findings[0].Subject.Name != "beta" {
		t.Fatalf("got %+v", r.Findings)
	}
}

func TestSkipForUnknownErrorKeepsTheReason(t *testing.T) {
	f := skipFor("x.y", controllerSubject, "Thing", errors.New("weird"))
	if f.Severity != doctor.Skip || !strings.Contains(f.Detail, "weird") {
		t.Fatalf("got %+v", f)
	}
}

// R5.29: the doctor reports what the real hook did when run as the service account.
func TestRunnerHookRun(t *testing.T) {
	mk := func(name string, p *doctor.HookProbe) doctor.Runner {
		r := persistent("id-"+name, name)
		r.HookProbe = p
		return r
	}
	rs := []doctor.Runner{
		mk("crsp-ok", &doctor.HookProbe{Output: "crsp hook ok\ncrsp hook non-interactive\n"}),
		mk("crsp-prompts", &doctor.HookProbe{Output: "crsp hook ok\n"}),
		mk("crsp-missing", &doctor.HookProbe{Output: "::warning title=crsp::crsp is not installed at /usr/local/bin/crsp, so this job ran without the queue and the load gate.\n"}),
		mk("crsp-exit", &doctor.HookProbe{ExitCode: 2, Output: "boom"}),
		mk("crsp-nostart", &doctor.HookProbe{Err: "powershell.exe: access denied"}),
		mk("crsp-silent", &doctor.HookProbe{Output: ""}),
		mk("crsp-unrun", nil),
	}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: rs}
	got := map[string]doctor.Finding{}
	for _, f := range forCheck(run(t, doctor.Env{Fleet: fl}, "runner.hookrun"), "runner.hookrun") {
		got[f.Subject.Name] = f
	}
	want := map[string]doctor.Severity{"crsp-ok": doctor.OK, "crsp-prompts": doctor.Warn, "crsp-missing": doctor.Fail, "crsp-exit": doctor.Fail,
		"crsp-nostart": doctor.Fail, "crsp-silent": doctor.Fail, "crsp-unrun": doctor.Skip}
	for name, sev := range want {
		if got[name].Severity != sev {
			t.Errorf("%s = %+v, want %s", name, got[name], sev)
		}
		if sev != doctor.OK && got[name].Fix.Text == "" {
			t.Errorf("%s has no fix", name)
		}
	}
	if d := got["crsp-missing"].Detail; !strings.Contains(d, "crsp is not installed") {
		t.Errorf("detail does not carry the annotation: %q", d)
	}
}

// R5.30: one listener per runner, under a running service.
func TestRunnerListener(t *testing.T) {
	mk := func(name string, known bool, pids []int, running bool) doctor.Runner {
		r := persistent("id-"+name, name)
		r.ListenerKnown, r.ListenerPIDs, r.ServiceRunning = known, pids, running
		return r
	}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{
		mk("one", true, []int{10}, true),
		mk("two", true, []int{10, 11}, true),
		mk("orphan", true, []int{12}, false),
		mk("empty", true, nil, true),
		mk("idle", true, nil, false),
		mk("unknown", false, nil, true),
	}}
	got := map[string]doctor.Finding{}
	for _, f := range forCheck(run(t, doctor.Env{Fleet: fl}, "runner.listener"), "runner.listener") {
		got[f.Subject.Name] = f
	}
	want := map[string]doctor.Severity{"one": doctor.OK, "two": doctor.Fail, "orphan": doctor.Fail,
		"empty": doctor.Warn, "idle": doctor.OK, "unknown": doctor.Skip}
	for name, sev := range want {
		if got[name].Severity != sev {
			t.Errorf("%s = %+v, want %s", name, got[name], sev)
		}
		if sev != doctor.OK && got[name].Fix.Text == "" {
			t.Errorf("%s has no fix", name)
		}
	}
}

// Doctor R5.39 (runners R5.67, lesson 29): a refused session is no finding
// inside the wait window after a restart, a warning after it, and a second
// listener is a failure in any case.
func TestRunnerListenerStaleSession(t *testing.T) {
	mk := func(name string, ago time.Duration, pids []int) doctor.Runner {
		r := persistent("id-"+name, name)
		r.ListenerKnown, r.ListenerPIDs, r.ServiceRunning = true, pids, true
		r.SessionRefused, r.RestartedAt = true, now.Add(-ago)
		return r
	}
	sev := func(r doctor.Runner) (doctor.Finding, []doctor.Finding) {
		fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{r}}
		fs := forCheck(run(t, doctor.Env{Fleet: fl}, "runner.listener"), "runner.listener")
		return fs[0], fs
	}
	if f, _ := sev(mk("inside", 90*time.Second, nil)); f.Severity != doctor.OK || f.Fix.Text != "" {
		t.Fatalf("inside the window: %+v", f)
	}
	f, _ := sev(mk("past", 7*time.Minute, nil))
	if f.Severity != doctor.Warn || f.Fix.Text == "" {
		t.Fatalf("past the window: %+v", f)
	}
	if f, _ := sev(mk("two", 30*time.Second, []int{1, 2})); f.Severity != doctor.Fail {
		t.Fatalf("two listeners inside the window: %+v", f)
	}
}

// R5.35: the runner account must be able to read its tool cache.
func TestRunnerToolCache(t *testing.T) {
	ok, bad, owner, none := persistent("r1", "crsp-ok"), persistent("r2", "crsp-bad"), persistent("r3", "crsp-owner"), persistent("r4", "crsp-none")
	dir := `C:\ProgramData\crsp\runners\crsp-bad`
	ok.ToolCache = &doctor.ToolCacheProbe{Dir: dir}
	bad.ToolCache = &doctor.ToolCacheProbe{Dir: dir, File: "_work/_tool/lld-link.exe", Err: "Access is denied"}
	owner.ToolCache = &doctor.ToolCacheProbe{Dir: dir, OwnerKnown: true, WrongOwner: []string{"a.exe"}, NotInherited: []string{"b.exe"}}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{ok, bad, owner, none}}
	got := map[string]doctor.Finding{}
	for _, f := range run(t, doctor.Env{Fleet: fl}, "runner.toolcache") {
		got[f.Subject.Name] = f
	}
	if got["crsp-ok"].Severity != doctor.OK || got["crsp-none"].Severity != doctor.Skip {
		t.Errorf("ok=%+v none=%+v", got["crsp-ok"], got["crsp-none"])
	}
	for _, n := range []string{"crsp-bad", "crsp-owner"} {
		f := got[n]
		if f.Severity != doctor.Fail || !strings.Contains(f.Fix.Command, "takeown /F "+dir+" /R /D Y") || !strings.Contains(f.Fix.Command, "icacls "+dir+" /reset /T /C") {
			t.Errorf("%s = %+v", n, f)
		}
	}
	if !strings.Contains(got["crsp-bad"].Detail, "Access is denied") || !strings.Contains(got["crsp-owner"].Detail, "another owner") {
		t.Errorf("detail: %q / %q", got["crsp-bad"].Detail, got["crsp-owner"].Detail)
	}
}

// R5.42: a Windows runner whose setting asks for the symbolic link right holds
// it, or Developer Mode is on; the check only reads.
func TestRunnerSymlink(t *testing.T) {
	win := func(id, name string, wanted bool, p *doctor.SymlinkProbe) doctor.Runner {
		r := persistent(id, name)
		r.OS, r.SymlinkWanted, r.Symlink = "windows", wanted, p
		return r
	}
	lin := persistent("r0", "crsp-linux")
	lin.SymlinkWanted = true
	rs := []doctor.Runner{
		lin,
		win("r1", "crsp-held", true, &doctor.SymlinkProbe{Held: true}),
		win("r2", "crsp-dev", true, &doctor.SymlinkProbe{DeveloperMode: true}),
		win("r3", "crsp-missing", true, &doctor.SymlinkProbe{}),
		win("r4", "crsp-extra", false, &doctor.SymlinkProbe{Held: true}),
		win("r5", "crsp-none", false, &doctor.SymlinkProbe{}),
		win("r6", "crsp-unread", true, nil),
		win("r7", "crsp-quiet", false, nil),
		win("r8", "crsp-err", true, &doctor.SymlinkProbe{Err: "access denied"}),
	}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: rs}
	got := map[string]doctor.Finding{}
	for _, f := range run(t, doctor.Env{Fleet: fl}, "runner.symlink") {
		got[f.Subject.Name] = f
	}
	want := map[string]doctor.Severity{
		"crsp-held": doctor.OK, "crsp-dev": doctor.OK, "crsp-missing": doctor.Fail, "crsp-extra": doctor.Warn,
		"crsp-none": doctor.OK, "crsp-unread": doctor.Skip, "crsp-err": doctor.Skip,
	}
	for n, sev := range want {
		if got[n].Severity != sev {
			t.Errorf("%s = %+v, want %v", n, got[n], sev)
		}
	}
	if _, ok := got["crsp-linux"]; ok {
		t.Error("a Linux runner has no symbolic link finding")
	}
	if _, ok := got["crsp-quiet"]; ok {
		t.Error("a runner that asks for nothing and was not read has no finding")
	}
	if !strings.Contains(got["crsp-dev"].Title, "Developer Mode") || !strings.Contains(got["crsp-missing"].Detail, "Developer Mode is off") {
		t.Errorf("dev=%q missing=%q", got["crsp-dev"].Title, got["crsp-missing"].Detail)
	}
}

// R5.45 (runners R5.89): a Windows runner's account holds the right to log on
// as a service and its service runs; the finding names the account and the
// service and the fix is Start on the machine.
func TestRunnerLogon(t *testing.T) {
	win := func(id, name string, p *doctor.LogonProbe) doctor.Runner {
		r := persistent(id, name)
		r.OS, r.Logon = "windows", p
		return r
	}
	acct := `.\crsp-lost`
	rs := []doctor.Runner{
		persistent("r0", "crsp-linux"),
		win("r1", "crsp-ok", &doctor.LogonProbe{Service: "svc-ok", Account: `.\crsp-ok`, Known: true, Held: true, Running: true}),
		win("r2", "crsp-lost", &doctor.LogonProbe{Service: "svc-lost", Account: acct, Known: true, Held: false}),
		win("r3", "crsp-stopped", &doctor.LogonProbe{Service: "svc-stopped", Account: `.\crsp-stopped`, Known: true, Held: true, Problem: "disabled"}),
		win("r4", "crsp-unread", nil),
		win("r5", "crsp-unknown", &doctor.LogonProbe{Account: `.\crsp-unknown`}),
	}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: rs}
	got := map[string]doctor.Finding{}
	for _, f := range run(t, doctor.Env{Fleet: fl}, "runner.logon") {
		got[f.Subject.Name] = f
	}
	want := map[string]doctor.Severity{
		"crsp-ok": doctor.OK, "crsp-lost": doctor.Fail, "crsp-stopped": doctor.Fail, "crsp-unread": doctor.Skip, "crsp-unknown": doctor.Skip,
	}
	for n, sev := range want {
		if got[n].Severity != sev {
			t.Errorf("%s = %+v, want %v", n, got[n], sev)
		}
	}
	if _, ok := got["crsp-linux"]; ok {
		t.Error("a Linux runner has no logon finding")
	}
	lost := got["crsp-lost"]
	for _, w := range []string{acct, "svc-lost"} {
		if !strings.Contains(lost.Title+lost.Detail, w) {
			t.Errorf("the finding does not name %q: %+v", w, lost)
		}
	}
	if !strings.Contains(lost.Fix.Text, "Start") || lost.Fix.Link != "/machines/m1" {
		t.Errorf("fix = %+v", lost.Fix)
	}
	if !strings.Contains(got["crsp-stopped"].Detail, "disabled") {
		t.Errorf("the reason Windows gave is lost: %+v", got["crsp-stopped"])
	}
}

// CRSP83 (runners R5.83): a machine paused for disk space names what to clean.
func TestDiskFloorNamesWhatToClean(t *testing.T) {
	held := healthyMachine()
	held.ID, held.Name, held.PauseReason, held.DiskFreeBytes = "h", "held", "disk_low", i64(4*gib)
	other := healthyMachine()
	other.ID, other.Name, other.PauseReason = "o", "other", "on_battery"
	off := healthyMachine()
	off.ID, off.Name, off.Online, off.PauseReason = "x", "off", false, "disk_low"
	fs := run(t, doctor.Env{Fleet: &fleet{machines: []doctor.Machine{held, other, off, healthyMachine()}}}, "machine.floor.disk")
	want := map[string]doctor.Severity{"held": doctor.Fail, "other": doctor.OK, "off": doctor.Skip, "alpha": doctor.OK}
	for _, f := range fs {
		if want[f.Subject.Name] != f.Severity {
			t.Errorf("%s: %v", f.Subject.Name, f.Severity)
		}
		if f.Subject.Name == "held" {
			for _, w := range []string{"docker image prune", "clean-work", "4.0 GiB", "disk_floor_gib"} {
				if !strings.Contains(f.Fix.Text+f.Detail, w) {
					t.Errorf("the finding does not name %q: %+v", w, f)
				}
			}
		}
	}
	if len(fs) != 4 {
		t.Errorf("%d findings", len(fs))
	}
}

func TestSymlinkProbeFrom(t *testing.T) {
	if doctor.SymlinkProbeFrom(false, false, false, "") != nil {
		t.Fatal("unknown must be nil so the check skips")
	}
	if p := doctor.SymlinkProbeFrom(false, false, false, "boom"); p == nil || p.Err != "boom" {
		t.Fatalf("%+v", p)
	}
	if p := doctor.SymlinkProbeFrom(true, true, false, ""); !p.Held || p.DeveloperMode {
		t.Fatalf("%+v", p)
	}
	if p := doctor.SymlinkProbeFrom(true, true, true, ""); p.Held || !p.DeveloperMode {
		t.Fatalf("%+v", p)
	}
	if p := doctor.SymlinkProbeFrom(true, false, false, ""); p.Held || p.DeveloperMode {
		t.Fatalf("%+v", p)
	}
}

// Doctor R5.50: both halves of a half-registered runner offer a Remove button
// that carries what the server needs to check again; the doctor itself removes
// nothing.
func TestRegistrationHalvesOfferRemove(t *testing.T) {
	g := newGH()
	g.runners["o/r"] = []github.Runner{{ID: 555, Name: "crsp-stray", Status: "offline"}}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{persistent("r1", "crsp-mine")}}
	fs := run(t, doctor.Env{GitHub: g, Fleet: fl}, "runner.orphan")
	var stray doctor.Finding
	for _, f := range fs {
		if f.Subject.Name == "crsp-stray" {
			stray = f
		}
	}
	if stray.Action == nil || stray.Action.ID != doctor.ActionRemoveGitHubRunner || stray.Action.Args == nil ||
		stray.Action.Args.GitHubRunnerID != 555 || stray.Action.Args.Name != "crsp-stray" || stray.Action.Args.Owner != "o" || stray.Action.Args.Repo != "r" {
		t.Fatalf("GitHub runner with no record: %+v", stray)
	}
	// The record GitHub does not list.
	fs = forCheck(run(t, doctor.Env{GitHub: g, Fleet: fl}, "runner.registered"), "runner.registered")
	if len(fs) != 1 || fs[0].Severity != doctor.Fail || fs[0].Action == nil || fs[0].Action.ID != doctor.ActionRemoveRunnerRecord || fs[0].Action.Args.RunnerID != "r1" {
		t.Fatalf("record with no GitHub runner: %+v", fs)
	}
}

// Caches R5.57: a runner whose user cannot write the shared tool folders is a
// fail with the commands that fix it; one that can is ok.
func TestRunnerCacheWrite(t *testing.T) {
	bad, good, unlooked := persistent("r1", "crsp-bad"), persistent("r2", "crsp-good"), persistent("r3", "crsp-new")
	bad.OS, good.OS = "linux", "linux"
	root := "/var/lib/crsp/tools/1"
	bad.CacheWrite = &doctor.CacheWriteProbe{Root: root, Group: "crsp-repo-1", Dirs: []doctor.CacheDirProbe{
		{Dir: root + "/cargo-target", Err: "Permission denied"}, {Dir: root + "/xwin"}}}
	good.CacheWrite = &doctor.CacheWriteProbe{Root: root, Group: "crsp-repo-1", Dirs: []doctor.CacheDirProbe{{Dir: root + "/cargo-target"}}}
	fl := &fleet{machines: []doctor.Machine{healthyMachine()}, runners: []doctor.Runner{bad, good, unlooked}}
	got := map[string]doctor.Finding{}
	for _, f := range forCheck(run(t, doctor.Env{Fleet: fl}, "runner.cachewrite"), "runner.cachewrite") {
		got[f.Subject.Name] = f
	}
	b := got["crsp-bad"]
	if b.Severity != doctor.Fail || !strings.Contains(b.Detail, root+"/cargo-target (Permission denied)") || strings.Contains(b.Detail, "xwin") ||
		!strings.Contains(b.Fix.Command, "setfacl -R -d -m g:crsp-repo-1:rwX "+root) {
		t.Errorf("bad: %+v", b)
	}
	if got["crsp-good"].Severity != doctor.OK || got["crsp-new"].Severity != doctor.Skip {
		t.Errorf("good %+v, unlooked %+v", got["crsp-good"], got["crsp-new"])
	}
}
