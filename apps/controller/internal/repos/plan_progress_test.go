package repos

import (
	"context"
	"testing"
)

// CRSP405: the plan reports each step as it starts and as it ends, so a page that reads it sees the bar move.
func TestApplyPlanReportsEachStepAsItHappens(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	for _, pool := range []bool{true, false} {
		plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: pool}})
		if err != nil {
			t.Fatal(err)
		}
		var seen [][]string
		e.svc.cfg.OnProgress = func(p Plan) {
			row := make([]string, 0, len(p.Steps))
			for _, st := range p.Steps {
				row = append(row, st.State)
			}
			seen = append(seen, row)
		}
		if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, yesIDs(plan)); err != nil {
			t.Fatal(err)
		}
		// Every step is seen running (saved too) before it is done, one at a time.
		running := map[int]bool{}
		for _, row := range seen {
			n := 0
			for i, s := range row {
				if s == StepRunning {
					running[i] = true
					n++
				}
			}
			if n > 1 {
				t.Errorf("pool=%v: two steps running at once: %v", pool, row)
			}
		}
		if len(running) != len(plan.Steps) {
			t.Errorf("pool=%v: %d of %d steps were seen running", pool, len(running), len(plan.Steps))
		}
		actions := map[string]bool{}
		for _, st := range plan.Steps {
			actions[st.Action] = true
		}
		if !actions[ActSyncRunners] {
			t.Errorf("pool=%v: no runners step", pool)
		}
		if pool && !actions[ActCheckRunsOn] {
			t.Error("a turn on has no check step")
		}
	}
}

func yesIDs(p Plan) []string {
	var out []string
	for _, st := range p.Steps {
		if st.NeedsYes {
			out = append(out, st.ID)
		}
	}
	return out
}
