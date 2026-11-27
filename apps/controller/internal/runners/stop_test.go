package runners

import (
	"context"
	"errors"
	"testing"
)

// web-console 11: the step that runs finishes, no later step starts, the plan
// ends stopped and the steps left say so.
func TestStopPlanWhileApplying(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 3, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "luis-mc")
	if err != nil || len(p.Steps) < 3 {
		t.Fatalf("%+v %v", p, err)
	}
	var seen []string
	asked := false
	r.svc.OnProgress = func(pl Plan) {
		seen = append(seen, pl.State)
		if !asked && pl.Steps[0].State == "done" {
			asked = true
			got, err := r.svc.StopPlan(ctx, pl.ID, "luis-mc")
			if err != nil || got.State != PlanApplying {
				t.Errorf("stop: %+v %v", got, err)
			}
		}
	}
	approved := []string{}
	for _, st := range p.Steps {
		approved = append(approved, st.ID)
	}
	out, err := r.svc.Apply(ctx, p.ID, approved, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if out.State != PlanStopped {
		t.Fatalf("state %s", out.State)
	}
	if out.Steps[0].State != "done" {
		t.Errorf("the step that ran must finish: %s", out.Steps[0].State)
	}
	for _, st := range out.Steps[1:] {
		if st.State != "skipped" {
			t.Errorf("step %s is %s, want skipped", st.ID, st.State)
		}
	}
	saved, _ := r.svc.GetPlan(ctx, p.ID)
	if saved.State != PlanStopped || seen[len(seen)-1] == PlanDone {
		t.Errorf("saved %s, progress %v", saved.State, seen)
	}
	// A stopped plan is not applying (409), and it is not applied again.
	var re *RefusalError
	if _, err := r.svc.StopPlan(ctx, p.ID, "u"); !errors.As(err, &re) {
		t.Errorf("second stop: %v", err)
	}
	if _, err := r.svc.Apply(ctx, p.ID, approved, "u"); !errors.As(err, &re) {
		t.Errorf("apply after stop: %v", err)
	}
}

// A replacement that waits for the new runner has no step running: a stop ends
// it at once.
func TestStopWaitingPlan(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	p, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	var re *RefusalError
	if _, err := r.svc.StopPlan(ctx, p.ID, "u"); !errors.As(err, &re) {
		t.Fatalf("planned plan: %v", err)
	}
	p.State = PlanApplying
	p.Steps[0].State = "waiting"
	_ = r.store.SavePlan(ctx, p)
	got, err := r.svc.StopPlan(ctx, p.ID, "u")
	if err != nil || got.State != PlanStopped || got.Steps[0].State != "skipped" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := r.svc.StopPlan(ctx, "nope", "u"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plan: %v", err)
	}
}
