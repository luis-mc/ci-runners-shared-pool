package runners

import (
	"context"
	"errors"
	"testing"
)

// LOOK31 (D31): a plan with no scope serves the pool. The service takes the first
// pool repository and marks the plan pool-level; with none it refuses with a reason.
func TestPlanWithoutScopeTakesThePoolRepository(t *testing.T) {
	ctx := context.Background()
	req := AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1}

	r := newRig(dockerMachine(true))
	if _, err := r.svc.PlanAdd(ctx, req, "u"); !errors.Is(err, ErrNoPoolRepository) {
		t.Fatalf("no default scope wired: %v", err)
	}

	r.svc.DefaultScope = func(context.Context) (Scope, error) { return Scope{}, ErrNoPoolRepository }
	if _, err := r.svc.PlanAdd(ctx, req, "u"); !errors.Is(err, ErrNoPoolRepository) {
		t.Fatalf("no pool repository: %v", err)
	}

	var gated Scope
	r.svc.Gate = func(_ context.Context, sc Scope) error { gated = sc; return nil }
	r.svc.DefaultScope = func(context.Context) (Scope, error) {
		return Scope{Kind: "repository", RepositoryID: 7}, nil
	}
	p, err := r.svc.PlanAdd(ctx, req, "u")
	if err != nil {
		t.Fatal(err)
	}
	if gated.RepositoryID != 7 || p.Adds[0].Scope.RepositoryID != 7 {
		t.Errorf("scope: gate %+v, add %+v", gated, p.Adds[0].Scope)
	}
}
