package repos

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/trial"
)

func trialVarsOf(t *testing.T, e *env, repo string) map[string]string {
	t.Helper()
	m, err := e.svc.Trial(nil).Variables(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// R5.66: a canary writes through the owner code, the controller's variables
// stay as they are, the reconcile keeps the canary variables, and off puts
// the exact earlier variables back.
func TestTrialCanaryThroughOwnerCode(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	tv := e.svc.Trial(nil)
	m := trial.New(trial.Config{Vars: tv, Dispatcher: tv, KV: &trial.MemKV{}, Now: e.clock})
	before := trialVarsOf(t, e, "alice/secret")
	if before["CRSP_RUNS_ON_LINUX"] == "" {
		t.Fatalf("pool on writes the variable: %v", before)
	}
	// A repository moves one step at a time (operations R5.42): off, observe, shadow, canary.
	for _, step := range []trial.Mode{trial.Observe, trial.Shadow} {
		if _, err := m.Set(ctx, "alice/secret", step, trial.Options{}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := m.Set(ctx, "alice/secret", trial.Canary, trial.Options{Jobs: []string{"build"}})
	if err != nil {
		t.Fatal(err)
	}
	got := trialVarsOf(t, e, "alice/secret")
	if got["CRSP_RUNS_ON_CANARY_BUILD"] != before["CRSP_RUNS_ON_LINUX"] || got["CRSP_RUNS_ON_LINUX"] != before["CRSP_RUNS_ON_LINUX"] || len(st.Written) != 1 {
		t.Fatalf("after canary: %v", got)
	}
	if !e.audit.has("variable.written") {
		t.Fatal("canary write is audited")
	}
	if err := e.svc.ReconcileVariables(ctx, 101); err != nil {
		t.Fatal(err)
	}
	if _, ok := trialVarsOf(t, e, "alice/secret")["CRSP_RUNS_ON_CANARY_BUILD"]; !ok {
		t.Fatal("the reconcile removed the canary variable")
	}
	if _, err := m.Set(ctx, "alice/secret", trial.Off, trial.Options{}); err != nil {
		t.Fatal(err)
	}
	if after := trialVarsOf(t, e, "alice/secret"); !reflect.DeepEqual(after, before) {
		t.Fatalf("not restored:\n got %v\nwant %v", after, before)
	}
}

func TestTrialRefusesPublicAndForeignNames(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	tv := e.svc.Trial(nil)
	m := trial.New(trial.Config{Vars: tv, Dispatcher: tv, KV: &trial.MemKV{}, Now: e.clock})
	for _, mode := range []trial.Mode{trial.Observe, trial.Shadow, trial.Canary} {
		if _, err := m.Set(ctx, "alice/web", mode, trial.Options{Percent: 50}); !errors.Is(err, trial.ErrPublic) {
			t.Errorf("%s: %v", mode, err)
		}
	}
	if err := tv.Write(ctx, "alice/secret", "CRSP_RUNS_ON_LINUX", "x"); err == nil {
		t.Fatal("the trial may not write a controller variable")
	}
	if err := tv.Remove(ctx, "alice/secret", "CI_RUNS_ON"); err == nil {
		t.Fatal("the trial may not delete an old alias")
	}
	if err := tv.Dispatch(ctx, "alice/web", "crsp-shadow.yml", "main", nil); !errors.Is(err, trial.ErrPublic) {
		t.Fatalf("dispatch on public: %v", err)
	}
}
