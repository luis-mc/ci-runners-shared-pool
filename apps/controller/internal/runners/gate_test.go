package runners

import (
	"context"
	"errors"
	"testing"
)

// CRSP131 (github-integration R5.55): the gate refuses a scope before anything
// is planned, and again when a plan made earlier is applied.
func TestGateRefusesAddBeforeAndAtApply(t *testing.T) {
	ctx := context.Background()
	errOff := errors.New("public repositories are off")
	r := newRig(dockerMachine(true))
	open := true
	r.svc.Gate = func(_ context.Context, sc Scope) error {
		if sc.RepositoryID == 1 && !open {
			return errOff
		}
		return nil
	}
	req := AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: repoScope}
	p, err := r.svc.PlanAdd(ctx, req, "u")
	if err != nil {
		t.Fatal(err)
	}
	open = false
	if _, err := r.svc.StartApply(ctx, p.ID, nil, "u"); !errors.Is(err, errOff) {
		t.Fatalf("apply after the gate closed: %v", err)
	}
	if _, err := r.svc.PlanAdd(ctx, req, "u"); !errors.Is(err, errOff) {
		t.Fatalf("plan: %v", err)
	}
	if len(r.ag.added) != 0 {
		t.Errorf("a refused add started a runner: %v", r.ag.added)
	}
	// Another repository passes the same gate.
	other := req
	other.Scope = Scope{Kind: "repository", RepositoryID: 2}
	if _, err := r.svc.PlanAdd(ctx, other, "u"); err != nil {
		t.Errorf("other repository: %v", err)
	}
}
