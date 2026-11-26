package repos

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github/fake"
)

type upSrc struct{ m map[string]bool }

func (u *upSrc) UpOS(context.Context) (map[string]bool, error) { return u.m, nil }

func fallbackEnv(t *testing.T) (*env, *upSrc) {
	t.Helper()
	e := newEnv(t)
	up := &upSrc{m: map[string]bool{"Linux": true}}
	e.svc.cfg.Up = up
	e.world()
	e.turnOn(101)
	return e, up
}

func optIn(t *testing.T, e *env, id int64, capMin int) {
	t.Helper()
	on := true
	u := PolicyUpdate{HostedFallback: &on, ConfirmFallbackCost: true}
	if capMin > 0 {
		u.FallbackCapMinutes = &capMin
	}
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, id, u); err != nil {
		t.Fatal(err)
	}
}

func linuxVar(e *env) string {
	v, _ := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX")
	return v
}

const crspLinux = `["self-hosted","linux","x64","docker","crsp-home"]`

func TestFallbackDefaultOffNeverRewritten(t *testing.T) { // R5.42
	e, up := fallbackEnv(t)
	ctx := context.Background()
	if pol, _ := e.svc.policyOrDefault(ctx, 101); pol.HostedFallback {
		t.Fatal("default is off")
	}
	before := linuxVar(e)
	up.m["Linux"] = false
	for i := 0; i < 5; i++ {
		if err := e.svc.EvaluateFallback(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(10 * time.Minute)
	}
	if linuxVar(e) != before || e.audit.has("fallback.") {
		t.Fatalf("variable %s, audit %v", linuxVar(e), e.audit.events)
	}
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Where != WhereWaits || rt.Why != WhyFallbackOff {
		t.Fatalf("%+v", rt)
	}
}

func TestFallbackNeedsConfirmAndRefusesPublic(t *testing.T) { // R5.42
	e, _ := fallbackEnv(t)
	on := true
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 101, PolicyUpdate{HostedFallback: &on}); !errors.Is(err, ErrFallbackNeedsConfirm) {
		t.Fatalf("err %v", err)
	}
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 100, PolicyUpdate{HostedFallback: &on, ConfirmFallbackCost: true}); err == nil {
		t.Fatal("a public repository never falls back")
	}
}

func TestFallbackFlipsAfterGraceAndBack(t *testing.T) { // R5.43
	e, up := fallbackEnv(t)
	ctx := context.Background()
	optIn(t, e, 101, 0)
	up.m["Linux"] = false
	eval := func() {
		t.Helper()
		if err := e.svc.EvaluateFallback(ctx); err != nil {
			t.Fatal(err)
		}
	}
	eval()
	e.advance(2 * time.Minute)
	eval()
	if linuxVar(e) != crspLinux {
		t.Fatalf("inside the grace time: %s", linuxVar(e))
	}
	e.advance(61 * time.Second)
	eval()
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("after the grace time: %s", got)
	}
	if !e.audit.has("fallback.started") {
		t.Fatal("the switch is logged")
	}
	if rt, _ := e.svc.RoutingOf(ctx, 101); rt.Where != WhereHosted || rt.Why != WhyFallbackOn {
		t.Fatalf("%+v", rt)
	}
	// Reconcile does not undo the fallback.
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if got := linuxVar(e); got != `["ubuntu-latest"]` {
		t.Fatalf("reconcile: %s", got)
	}
	// A machine is up again: back at once.
	up.m["Linux"] = true
	eval()
	if linuxVar(e) != crspLinux || !e.audit.has("fallback.ended") {
		t.Fatalf("back: %s", linuxVar(e))
	}
	if rt, _ := e.svc.RoutingOf(ctx, 101); rt.Where != WhereCrsp {
		t.Fatalf("%+v", rt)
	}
}

func TestFallbackOtherRepositoriesUntouched(t *testing.T) { // R5.42
	e, up := fallbackEnv(t)
	ctx := context.Background()
	e.turnOn(100)
	optIn(t, e, 101, 0)
	before, _ := e.fake.Variable("alice/web", "CRSP_RUNS_ON_LINUX")
	up.m["Linux"] = false
	_ = e.svc.EvaluateFallback(ctx)
	e.advance(4 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	if got, _ := e.fake.Variable("alice/web", "CRSP_RUNS_ON_LINUX"); got != before {
		t.Fatalf("a repository that did not opt in changed: %s", got)
	}
}

func TestFallbackOptOutWhileOnRestores(t *testing.T) { // R5.42
	e, up := fallbackEnv(t)
	ctx := context.Background()
	optIn(t, e, 101, 0)
	up.m["Linux"] = false
	_ = e.svc.EvaluateFallback(ctx)
	e.advance(4 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	off := false
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{HostedFallback: &off}); err != nil {
		t.Fatal(err)
	}
	if linuxVar(e) != crspLinux {
		t.Fatalf("%s", linuxVar(e))
	}
}

func TestFallbackCapStopsFallingBack(t *testing.T) { // R5.45
	e, up := fallbackEnv(t)
	ctx := context.Background()
	optIn(t, e, 101, 5)
	up.m["Linux"] = false
	_ = e.svc.EvaluateFallback(ctx)
	e.advance(4 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	if linuxVar(e) != `["ubuntu-latest"]` {
		t.Fatalf("%s", linuxVar(e))
	}
	e.fake.AddJob(fake.Job{ID: 7, RunID: 70, Repo: "alice/secret", Name: "build", Status: "in_progress", Event: "push", Labels: []string{"ubuntu-latest"}})
	_ = e.svc.EvaluateFallback(ctx) // first sight of the job: nothing counted yet
	e.advance(3 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	if used, _ := e.svc.FallbackUsedMinutes(ctx, 101, e.clock()); used != 3 || linuxVar(e) != `["ubuntu-latest"]` {
		t.Fatalf("used %d, variable %s", used, linuxVar(e))
	}
	e.advance(3 * time.Minute)
	_ = e.svc.EvaluateFallback(ctx)
	if got := linuxVar(e); got != crspLinux {
		t.Fatalf("cap reached, waits again: %s", got)
	}
	rt, _ := e.svc.RoutingOf(ctx, 101)
	if rt.Where != WhereWaits || rt.Why != WhyCap {
		t.Fatalf("%+v", rt)
	}
	// The count is per month: another month starts from zero.
	if used, _ := e.svc.FallbackUsedMinutes(ctx, 101, e.clock().AddDate(0, 1, 0)); used != 0 {
		t.Fatalf("used %d", used)
	}
}

// CRSP261: the overflow label is any cloud label, not only a GitHub-hosted one.
func TestOverflowLabelReplacesHostedDefault(t *testing.T) { // R5.42
	e, up := fallbackEnv(t)
	ctx := context.Background()
	e.svc.cfg.OverflowLabel = func() string { return " acme-cloud-4x " }
	optIn(t, e, 101, 0)
	up.m["Linux"] = false
	for i := 0; i < 2; i++ {
		if err := e.svc.EvaluateFallback(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(4 * time.Minute)
	}
	if got := linuxVar(e); got != `["acme-cloud-4x"]` {
		t.Fatalf("overflow label not used: %s", got)
	}
	up.m["Linux"] = true
	if err := e.svc.EvaluateFallback(ctx); err != nil {
		t.Fatal(err)
	}
	if linuxVar(e) != crspLinux {
		t.Fatalf("back: %s", linuxVar(e))
	}
}

func TestOverflowLabelPolicyLabelWins(t *testing.T) { // R5.42
	pol := Policy{HostedLabels: map[string]string{"linux": "repo-own"}}
	s := &Service{}
	s.cfg.OverflowLabel = func() string { return "global-cloud" }
	if got := s.hostedLabelFor(pol, "linux"); got != "repo-own" {
		t.Fatalf("policy first: %s", got)
	}
	if got := s.hostedLabelFor(pol, "windows"); got != "global-cloud" {
		t.Fatalf("overflow second: %s", got)
	}
	s.cfg.OverflowLabel = nil
	if got := s.hostedLabelFor(Policy{}, "macos"); got != "macos-latest" {
		t.Fatalf("default last: %s", got)
	}
}
