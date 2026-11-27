package repos

import (
	"context"
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
