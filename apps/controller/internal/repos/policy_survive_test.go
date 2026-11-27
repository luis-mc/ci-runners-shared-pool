package repos

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// RES6, github-integration R5.25, R5.26, R5.28, R5.42: the policy a user set on a
// repository (priority, parallel jobs, labels, budgets, fallback,
// cap, hosted labels) is never changed by what the controller reads or writes on
// its own: a sync, the installation reconcile, the 10-minute CRSP_RUNS_ON restore, a
// change of the machines' labels, the fallback pass, or a restart.
func TestRepositoryPolicyNeverRevertsOnItsOwn(t *testing.T) {
	e, up := fallbackEnv(t)
	ctx := context.Background()
	u := PolicyUpdate{
		Priority: ip(7), MaxParallelJobs: ip(5), Labels: sp("gpu"), BudgetMemoryPct: ip(50), BudgetCPUPct: ip(40),
		HostedFallback: bp(true), ConfirmFallbackCost: true, FallbackCapMinutes: ip(90),
		HostedLabels: &map[string]string{"linux": "ubuntu-24.04"},
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, u); err != nil {
		t.Fatal(err)
	}
	want, err := e.store.GetPolicy(ctx, 101)
	if err != nil {
		t.Fatal(err)
	}
	same := func(when string) {
		t.Helper()
		got, err := e.store.GetPolicy(ctx, 101)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: the policy changed\n got  %+v\n want %+v", when, got, want)
		}
	}

	if err := e.svc.Sync(ctx, adminP); err != nil {
		t.Fatal(err)
	}
	same("after a sync")
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	same("after the installation reconcile")

	// Someone edits the variable on GitHub, a machine joins with other labels, and
	// the restore loop runs more than once.
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX", `["self-hosted","wrong"]`)
	e.cap.m = map[string][]string{"Linux": {"X64", "docker", "crsp-home", "gpu"}}
	for i := 0; i < 3; i++ {
		e.advance(10 * time.Minute)
		if err := e.svc.ReconcileAllVariables(ctx); err != nil {
			t.Fatal(err)
		}
		same("after the CRSP_RUNS_ON restore loop")
	}

	// The fallback goes on and off by itself; the policy, the opt-in and the cap
	// stay.
	up.m["Linux"] = false
	_ = e.svc.EvaluateFallback(ctx)
	e.advance(4 * time.Minute)
	if err := e.svc.EvaluateFallback(ctx); err != nil {
		t.Fatal(err)
	}
	up.m["Linux"] = true
	if err := e.svc.EvaluateFallback(ctx); err != nil {
		t.Fatal(err)
	}
	same("after the fallback went on and off")

	// A restart: a new service over the same store keeps the policy, and the
	// restore keeps writing what the policy says.
	restarted := New(e.svc.cfg)
	if err := restarted.ReconcileAllVariables(ctx); err != nil {
		t.Fatal(err)
	}
	same("after a restart")
	pol, err := restarted.policyOrDefault(ctx, 101)
	if err != nil || pol.MaxParallelJobs != 5 || !pol.HostedFallback || pol.FallbackCapMinutes != 90 {
		t.Fatalf("%+v %v", pol, err)
	}
}
