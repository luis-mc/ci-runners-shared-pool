package repos

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
)

// web-console 11: the step that runs finishes, no later step starts, the plan
// ends stopped, and the steps that did not run say so.
func TestStopPlanWhileApplying(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if err != nil || len(plan.Steps) < 2 {
		t.Fatalf("%+v %v", plan, err)
	}
	var seen []string
	asked := false
	e.svc.cfg.OnProgress = func(p Plan) {
		seen = append(seen, p.State)
		if !asked && p.State == PlanApplying { // after the first step finished
			asked = true
			got, err := e.svc.StopPlan(ctx, adminP, p.ID)
			if err != nil || got.State != PlanApplying {
				t.Errorf("stop: %+v %v", got, err)
			}
		}
	}
	out, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.State != PlanStopped {
		t.Fatalf("state %s", out.State)
	}
	if out.Steps[0].State != StepDone {
		t.Errorf("the step that ran must finish: %s", out.Steps[0].State)
	}
	for _, st := range out.Steps[1:] {
		if st.State != StepSkipped {
			t.Errorf("step %s is %s, want skipped", st.ID, st.State)
		}
	}
	saved, _ := e.svc.GetPlan(ctx, adminP, plan.ID)
	if saved.State != PlanStopped {
		t.Errorf("saved state %s", saved.State)
	}
	if !e.audit.has("plan.stop") {
		t.Error("the stop was not audited")
	}
	if seen[len(seen)-1] != PlanStopped {
		t.Errorf("last progress %v", seen)
	}
	// A stopped plan is not applying, so a second stop is a 409.
	if _, err := e.svc.StopPlan(ctx, adminP, plan.ID); !errors.Is(err, ErrPlanNotApplying) {
		t.Errorf("second stop: %v", err)
	}
}

func TestStopPlanRefusals(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	plan, _ := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if _, err := e.svc.StopPlan(ctx, adminP, plan.ID); !errors.Is(err, ErrPlanNotApplying) {
		t.Errorf("planned plan: %v", err)
	}
	if _, err := e.svc.StopPlan(ctx, viewerP, plan.ID); !errors.Is(err, auth.ErrForbidden) {
		t.Errorf("viewer: %v", err)
	}
	if _, err := e.svc.StopPlan(ctx, adminP, "pl_none"); !errors.Is(err, auth.ErrNotFound) {
		t.Errorf("unknown plan: %v", err)
	}
	// A plan left applying by a restart is ended at once.
	plan.State = PlanApplying
	plan.Steps[0].State = StepDone
	_ = e.store.UpdatePlan(ctx, plan)
	got, err := e.svc.StopPlan(ctx, adminP, plan.ID)
	if err != nil || got.State != PlanStopped || got.Steps[0].State != StepDone || got.Steps[1].State != StepSkipped {
		t.Errorf("orphaned plan: %+v %v", got, err)
	}
}
