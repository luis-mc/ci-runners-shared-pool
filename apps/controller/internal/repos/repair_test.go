package repos

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// CRSP388: Repair of a repository whose pool is already on plans the real work.

func (e *env) repair(ids ...int64) (Plan, error) {
	e.t.Helper()
	var cs []Change
	for _, id := range ids {
		cs = append(cs, Change{RepositoryID: id, Pool: true, Repair: true})
	}
	return e.svc.PlanChanges(context.Background(), adminP, cs)
}

func TestRepairWritesAMissingVariable(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	e.fake.Lock(func() { delete(e.fake.Variables["alice/secret"], "CRSP_RUNS_ON_LINUX") })
	plan, err := e.repair(101)
	if err != nil {
		t.Fatal(err)
	}
	if plan.State != PlanPlanned || len(plan.Steps) != 1 || plan.Steps[0].Action != ActWriteVariable || plan.Steps[0].Target != "CRSP_RUNS_ON_LINUX" {
		t.Fatalf("one write step: %+v", plan)
	}
	if !strings.Contains(plan.Steps[0].Text, "missing") {
		t.Fatalf("the step says why: %q", plan.Steps[0].Text)
	}
	plan, err = e.svc.ApplyPlan(ctx, adminP, plan.ID, nil)
	if err != nil || plan.State != PlanDone {
		t.Fatalf("apply: %v %+v", err, plan)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); !ok {
		t.Fatal("the missing variable is written")
	}
	if mv, _ := e.store.ListManagedVariables(ctx, 101); len(mv) != 1 || mv[0].Status != VarOK {
		t.Fatalf("the row is OK: %+v", mv)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.PoolOn {
		t.Fatal("a repair keeps the pool on")
	}
}

func TestRepairRewritesAWrongVariable(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX", `["ubuntu-latest"]`)
	plan, err := e.repair(101)
	if err != nil || len(plan.Steps) != 1 || !strings.Contains(plan.Steps[0].Text, "ubuntu-latest") {
		t.Fatalf("a wrong value is a write step: %v %+v", err, plan)
	}
	if _, err = e.svc.ApplyPlan(context.Background(), adminP, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if v, _ := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); strings.Contains(v, "ubuntu-latest") {
		t.Fatalf("rewritten: %s", v)
	}
}

func TestRepairDeletesAVariableThePolicyTurnsOff(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.cap.m = map[string][]string{"Linux": {"X64"}, "Windows": {"X64"}}
	e.turnOn(101)
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 101, PolicyUpdate{Labels: sp("windows")}); err != nil {
		t.Fatal(err)
	}
	e.fake.SetVariable("alice/secret", "CRSP_RUNS_ON_LINUX", `["self-hosted","linux"]`) // back by hand
	plan, err := e.repair(101)
	if err != nil || len(plan.Steps) != 1 || plan.Steps[0].Action != ActDeleteVariable || !plan.Steps[0].NeedsYes {
		t.Fatalf("one delete step that needs a yes: %v %+v", err, plan)
	}
	if _, err = e.svc.ApplyPlan(context.Background(), adminP, plan.ID, []string{plan.Steps[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.fake.Variable("alice/secret", "CRSP_RUNS_ON_LINUX"); ok {
		t.Fatal("deleted")
	}
}

func TestRepairWithNothingDifferentIsDoneNotAProblem(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	plan, err := e.repair(101)
	if err != nil {
		t.Fatalf("not ErrNothingToChange: %v", err)
	}
	if plan.State != PlanDone || len(plan.Steps) != 0 {
		t.Fatalf("done with no steps: %+v", plan)
	}
	got, err := e.svc.GetPlan(context.Background(), adminP, plan.ID)
	if err != nil || got.State != PlanDone {
		t.Fatalf("a reload reads it done: %v %+v", err, got)
	}
}

func TestRepairRefusesAPoolOffRepository(t *testing.T) {
	e := newEnv(t)
	e.world()
	if _, err := e.repair(101); err == nil || errors.Is(err, ErrNothingToChange) {
		t.Fatalf("a repair of a repository that does not use the pool is refused: %v", err)
	}
}

// CRSP388: a runner restart must not delete the variable.

func TestRestartingRunnerKeepsItsVariable(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	e.advance(CapacitySettle)
	e.svc.SetRunnerOS(func(context.Context) (map[string]bool, error) { return map[string]bool{"linux": true}, nil })
	e.cap.m = map[string][]string{} // the container restarts: no capacity, the runner record stays
	for i := 0; i < 4; i++ {
		if err := e.svc.RunnersChanged(ctx); err != nil {
			t.Fatal(err)
		}
		e.advance(DeleteHold)
	}
	if got := varNames(e, "alice/secret"); got != "CRSP_RUNS_ON_LINUX" {
		t.Fatalf("a runner record that exists keeps the variable: %q", got)
	}
	// The runner record goes too: the hold applies as before.
	e.svc.SetRunnerOS(func(context.Context) (map[string]bool, error) { return map[string]bool{}, nil })
	for i := 0; i < 2; i++ {
		_ = e.svc.RunnersChanged(ctx)
		e.advance(DeleteHold)
	}
	if got := varNames(e, "alice/secret"); got != "" {
		t.Fatalf("no runner record and no capacity for the hold: deleted: %q", got)
	}
}

func TestAnOldAbsenceDoesNotCountAfterALongPause(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	e.advance(CapacitySettle)
	e.cap.m = map[string][]string{}
	if err := e.svc.RunnersChanged(ctx); err != nil { // first missing reading
		t.Fatal(err)
	}
	// The OS came back and went unseen; the next sync is long after the first reading.
	e.advance(AbsentGap + time.Minute)
	if err := e.svc.RunnersChanged(ctx); err != nil {
		t.Fatal(err)
	}
	if got := varNames(e, "alice/secret"); got != "CRSP_RUNS_ON_LINUX" {
		t.Fatalf("a stale first reading deleted the variable: %q", got)
	}
}
