package repos

import (
	"context"
	"strings"
	"testing"
)

func lightVar(e *env, repo string) (string, bool) {
	var v string
	var ok bool
	e.fake.Lock(func() { v, ok = e.fake.Variables[repo][VarLight] })
	return v, ok
}

// OPS49 (D110): a RUNS_ON_LIGHT that still names the removed light runner
// label is rewritten to the pool's normal labels for the OS.
func TestLightVariableRewrittenToNormalLabels(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.SetVariable("alice/secret", VarLight, LightValue)
	ctx := context.Background()
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	got, ok := lightVar(e, "alice/secret")
	linux := e.variable("CRSP_RUNS_ON_LINUX")
	if !ok || got != linux || strings.Contains(got, "crsp-light") {
		t.Fatalf("RUNS_ON_LIGHT = %q, want %q", got, linux)
	}
	if !e.audit.has("variable.migrated|controller|alice/secret") {
		t.Fatalf("audit: %v", e.audit.events)
	}
	n := len(e.audit.events)
	// A second pass changes nothing.
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if len(e.audit.events) != n {
		t.Fatalf("second pass wrote: %v", e.audit.events[n:])
	}
}

// With no light variable nothing is written: the variable is never created.
func TestLightVariableNotCreated(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	if err := e.svc.ReconcileVariables(context.Background(), 101); err != nil {
		t.Fatal(err)
	}
	if v, ok := lightVar(e, "alice/secret"); ok {
		t.Fatalf("RUNS_ON_LIGHT = %q was created", v)
	}
}

// A value a person set by hand, not ours and not the old label, is left alone.
func TestLightVariableByHandIsKept(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.SetVariable("alice/secret", VarLight, `["self-hosted","mine"]`)
	if err := e.svc.ReconcileVariables(context.Background(), 101); err != nil {
		t.Fatal(err)
	}
	if v, _ := lightVar(e, "alice/secret"); v != `["self-hosted","mine"]` {
		t.Fatalf("RUNS_ON_LIGHT = %q", v)
	}
}

// A repository the pool serves on no OS has nothing to point at: left as is.
func TestLightVariableLeftWhenNoOSServed(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.cap.m = map[string][]string{}
	e.fake.SetVariable("alice/secret", VarLight, LightValue)
	if err := e.svc.ReconcileVariables(context.Background(), 101); err != nil {
		t.Fatal(err)
	}
	if v, _ := lightVar(e, "alice/secret"); v != LightValue {
		t.Fatalf("RUNS_ON_LIGHT = %q", v)
	}
}

// An old saved state that still carries LightRunner loads without failing.
func TestOldPolicyWithLightRunnerKeyStillLoads(t *testing.T) {
	m := NewMemStore()
	body := `{"policies":{"101":{"Priority":2,"LightRunner":true,"MaxParallelJobs":2}}}`
	if err := m.restore([]byte(body)); err != nil {
		t.Fatal(err)
	}
	pol, err := m.GetPolicy(context.Background(), 101)
	if err != nil || pol.Priority != 2 {
		t.Fatalf("policy %+v, %v", pol, err)
	}
}
