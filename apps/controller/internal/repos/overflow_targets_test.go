package repos

import (
	"context"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
	"testing"
	"time"
)

func targetsEnv(t *testing.T, ts ...OverflowTarget) (*env, *upSrc) {
	t.Helper()
	e, up := fallbackEnv(t)
	e.svc.cfg.OverflowTargets = func() []OverflowTarget { return ts }
	optIn(t, e, 101, 0)
	up.m["Linux"] = false
	return e, up
}

func runFallback(t *testing.T, e *env, rounds int) {
	t.Helper()
	for i := 0; i < rounds; i++ {
		if err := e.svc.EvaluateFallback(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.advance(4 * time.Minute)
	}
}

var twoTargets = []OverflowTarget{
	{Kind: "github_hosted", CapMinutes: 100},
	{Kind: "aws_ec2", Label: "ec2-pool-4x", Systems: []string{"linux"}},
}

func TestOverflowTargetsFirstTargetWins(t *testing.T) { // R5.114
	e, _ := targetsEnv(t, twoTargets...)
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("first target not used: %s", got)
	}
}

func TestOverflowTargetsNextWhenCapUsed(t *testing.T) { // R5.114
	e, _ := targetsEnv(t, twoTargets...)
	ctx := context.Background()
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("start: %s", got)
	}
	// The free minutes of GitHub-hosted run out: the repository moves to EC2.
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, 101, monthKey(e.svc.cfg.Now())+"#github_hosted", 100*60); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 1)
	if got := linuxVar(e); got != `["ec2-pool-4x"]` {
		t.Fatalf("not retargeted: %s", got)
	}
	if !e.audit.has("fallback.retargeted") {
		t.Fatalf("audit %v", e.audit.events)
	}
}

func TestOverflowTargetsAllUsedWaits(t *testing.T) { // R5.114
	e, _ := targetsEnv(t, OverflowTarget{Kind: "github_hosted", CapMinutes: 10})
	ctx := context.Background()
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, 101, monthKey(e.svc.cfg.Now())+"#github_hosted", 10*60); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 2)
	if got := linuxVar(e); got == `["ubuntu-latest"]` {
		t.Fatalf("fell back although the only target is used up: %s", got)
	}
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Where != WhereWaits || rt.Why != WhyCap {
		t.Fatalf("routing %+v", rt)
	}
}

func TestOverflowTargetsSystemFilter(t *testing.T) { // R5.114
	s := &Service{}
	s.cfg.OverflowTargets = func() []OverflowTarget {
		return []OverflowTarget{{Kind: "aws_ec2", Label: "ec2-l", Systems: []string{"linux"}}, {Kind: "github_hosted"}}
	}
	ctx := context.Background()
	l, k, ok, _ := s.pickTarget(ctx, Repository{ID: 1}, Policy{}, "linux")
	if !ok || l != "ec2-l" || k != "aws_ec2" {
		t.Fatalf("linux: %s %s %v", l, k, ok)
	}
	l, k, ok, _ = s.pickTarget(ctx, Repository{ID: 1}, Policy{}, "windows")
	if !ok || l != "windows-latest" || k != "github_hosted" {
		t.Fatalf("windows: %s %s %v", l, k, ok)
	}
	l, _, _, _ = s.pickTarget(ctx, Repository{ID: 1}, Policy{HostedLabels: map[string]string{"linux": "own"}}, "linux")
	if l != "own" {
		t.Fatalf("policy label first: %s", l)
	}
}

func TestOverflowTotalCapMovesToNextTarget(t *testing.T) { // R5.115
	e, _ := targetsEnv(t,
		OverflowTarget{Kind: "github_hosted", TotalCapMinutes: 50},
		OverflowTarget{Kind: "aws_ec2", Label: "ec2-pool-4x"})
	ctx := context.Background()
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("start: %s", got)
	}
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Target != "github_hosted" {
		t.Fatalf("target in use %q", rt.Target)
	}
	// Other repositories used up the total: this one moves on though its own cap is free.
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, totalKey(e.svc.cfg.Now(), "github_hosted"), 50*60); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 1)
	if got := linuxVar(e); got != `["ec2-pool-4x"]` {
		t.Fatalf("not retargeted: %s", got)
	}
	u, err := e.svc.OverflowUsage(ctx, e.svc.cfg.Now())
	if err != nil || len(u) != 2 || u[0].UsedMinutes != 50 || u[1].UsedMinutes != 0 {
		t.Fatalf("usage %+v %v", u, err)
	}
}

func TestOverflowTotalCapAllUsedWaits(t *testing.T) { // R5.115
	e, _ := targetsEnv(t, OverflowTarget{Kind: "github_hosted", TotalCapMinutes: 10})
	ctx := context.Background()
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, totalKey(e.svc.cfg.Now(), "github_hosted"), 10*60); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 2)
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Where != WhereWaits || rt.Why != WhyCap {
		t.Fatalf("routing %+v", rt)
	}
}

// rates gives github_hosted a rate of 0.01 dollars a minute and aws_ec2 none, as savings does.
func hostedRate(_ context.Context, kind, _ string) (float64, bool) {
	return 0.01, kind == "github_hosted"
}

func TestOverflowBudgetMovesToNextTarget(t *testing.T) { // R5.115, Q24
	e, _ := targetsEnv(t,
		OverflowTarget{Kind: "github_hosted", BudgetUSD: 5},
		OverflowTarget{Kind: "aws_ec2", Label: "ec2-pool-4x"})
	e.svc.cfg.Rate = hostedRate
	ctx := context.Background()
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("start: %s", got)
	}
	// Money used reaches the budget (5 dollars = 5,000,000 millionths): the next target takes over.
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, usdKey(e.svc.cfg.Now(), "github_hosted"), 5_000_000); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 1)
	if got := linuxVar(e); got != `["ec2-pool-4x"]` {
		t.Fatalf("not retargeted: %s", got)
	}
	u, err := e.svc.OverflowUsage(ctx, e.svc.cfg.Now())
	if err != nil || len(u) != 2 || u[0].UsedUSD != 5 || !u[0].Priced || u[1].Priced {
		t.Fatalf("usage %+v %v", u, err)
	}
}

func TestOverflowBudgetAllUsedWaits(t *testing.T) { // R5.115
	e, _ := targetsEnv(t, OverflowTarget{Kind: "github_hosted", BudgetUSD: 1})
	e.svc.cfg.Rate = hostedRate
	ctx := context.Background()
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, usdKey(e.svc.cfg.Now(), "github_hosted"), 1_000_000); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 2)
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Where != WhereWaits || rt.Why != WhyCap {
		t.Fatalf("routing %+v", rt)
	}
}

func TestOverflowBudgetNotEnforcedWithoutRate(t *testing.T) { // R5.115
	e, _ := targetsEnv(t, OverflowTarget{Kind: "aws_ec2", Label: "ec2-pool-4x", BudgetUSD: 1})
	e.svc.cfg.Rate = hostedRate
	ctx := context.Background()
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, usdKey(e.svc.cfg.Now(), "aws_ec2"), 9_000_000); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ec2-pool-4x"]` {
		t.Fatalf("a target with no rate must still be used: %s", got)
	}
}

// tableRate is a rate table: windows costs 0.016 and linux 0.008 a minute at GitHub-hosted; aws_ec2 has none.
func tableRate(_ context.Context, kind, os string) (float64, bool) {
	if kind != "github_hosted" {
		return 0, false
	}
	if os == "windows" {
		return 0.016, true
	}
	return 0.008, true
}

func TestOverflowPriceOverridesTable(t *testing.T) { // R5.115, CRSP372
	cases := []struct {
		name   string
		target OverflowTarget
		kind   string
		os     string
		want   float64
		ok     bool
	}{
		{"hosted, no price: the table", OverflowTarget{Kind: "github_hosted"}, "github_hosted", "windows", 0.016, true},
		{"hosted, price set: the price beats the table", OverflowTarget{Kind: "github_hosted", PriceUSDPerMinute: 0.02}, "github_hosted", "linux", 0.02, true},
		{"ec2, no price: no rate", OverflowTarget{Kind: "aws_ec2", Label: "e"}, "aws_ec2", "linux", 0, false},
		{"ec2, price set: priced", OverflowTarget{Kind: "aws_ec2", Label: "e", PriceUSDPerMinute: 0.005}, "aws_ec2", "linux", 0.005, true},
		{"a price of another target is not used", OverflowTarget{Kind: "aws_ec2", Label: "e", PriceUSDPerMinute: 0.005}, "github_hosted", "linux", 0.008, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := targetsEnv(t, c.target)
			e.svc.cfg.Rate = tableRate
			got, ok := e.svc.rate(context.Background(), c.kind, c.os)
			if got != c.want || ok != c.ok {
				t.Fatalf("rate = %v, %v; want %v, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestOverflowEC2BudgetEnforcedWithPrice(t *testing.T) { // R5.115, CRSP372
	e, _ := targetsEnv(t,
		OverflowTarget{Kind: "aws_ec2", Label: "ec2-pool-4x", BudgetUSD: 1, PriceUSDPerMinute: 0.01},
		OverflowTarget{Kind: "github_hosted"})
	e.svc.cfg.Rate = tableRate
	ctx := context.Background()
	runFallback(t, e, 2)
	if got := linuxVar(e); got != `["ec2-pool-4x"]` {
		t.Fatalf("start: %s", got)
	}
	// The EC2 money used reaches its budget: with a price the budget is enforced, so the next target takes over.
	if err := e.svc.cfg.Store.AddFallbackSeconds(ctx, totalRepoID, usdKey(e.svc.cfg.Now(), "aws_ec2"), 1_000_000); err != nil {
		t.Fatal(err)
	}
	runFallback(t, e, 1)
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("not retargeted: %s", got)
	}
	u, err := e.svc.OverflowUsage(ctx, e.svc.cfg.Now())
	if err != nil || len(u) != 2 || !u[0].Priced || u[0].UsedUSD != 1 {
		t.Fatalf("usage %+v %v", u, err)
	}
}

// usedUSD runs a hosted job for three minutes and returns the money counted for github_hosted.
func usedUSD(t *testing.T, e *env, job fake.Job) float64 {
	t.Helper()
	e.svc.cfg.Rate = tableRate
	ctx := context.Background()
	runFallback(t, e, 2)
	e.fake.AddJob(job)
	_ = e.svc.EvaluateFallback(ctx) // first sight of the job: nothing counted yet
	e.advance(3 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	u, err := e.svc.OverflowUsage(ctx, e.svc.cfg.Now())
	if err != nil || len(u) == 0 {
		t.Fatalf("usage %+v %v", u, err)
	}
	return u[0].UsedUSD
}

func TestOverflowJobOSFromGitHubBeatsLabel(t *testing.T) { // R5.115, CRSP372
	e, _ := targetsEnv(t, OverflowTarget{Kind: "github_hosted"})
	// GitHub lists the job's runner as Windows; the label says nothing about it.
	e.fake.AddRunner("alice/secret", fake.Runner{Name: "win-runner-1", OS: "Windows", Status: "online"})
	got := usedUSD(t, e, fake.Job{ID: 7, RunID: 70, Repo: "alice/secret", Name: "build", Status: "in_progress", Event: "push",
		Labels: []string{"ubuntu-latest"}, RunnerName: "win-runner-1"})
	if want := 3 * 0.016; got < want-1e-6 || got > want+1e-6 {
		t.Fatalf("used %v dollars, want %v (the OS GitHub reports, windows)", got, want)
	}
}

func TestOverflowJobOSFallsBackToLabel(t *testing.T) { // R5.115, CRSP372
	// GitHub reports no OS for the runner (a standard hosted runner is not in the list): the label guess is used.
	for name, job := range map[string]fake.Job{
		"no runner name":       {Labels: []string{"windows-latest"}},
		"runner not listed":    {Labels: []string{"windows-latest"}, RunnerName: "GitHub Actions 1000001"},
		"listed without an OS": {Labels: []string{"windows-latest"}, RunnerName: "no-os"},
	} {
		t.Run(name, func(t *testing.T) {
			e, _ := targetsEnv(t, OverflowTarget{Kind: "github_hosted"})
			e.fake.AddRunner("alice/secret", fake.Runner{Name: "no-os", Status: "online"})
			job.ID, job.RunID, job.Repo, job.Name, job.Status, job.Event = 7, 70, "alice/secret", "build", "in_progress", "push"
			if got, want := usedUSD(t, e, job), 3*0.016; got < want-1e-6 || got > want+1e-6 {
				t.Fatalf("used %v dollars, want %v (the label guess, windows)", got, want)
			}
		})
	}
}
