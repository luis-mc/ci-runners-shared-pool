package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// OPS55 R5.73: runners for one repository on a machine never exceed its job slots.
func TestPlanAddRefusesMoreRunnersThanSlots(t *testing.T) {
	ctx := context.Background()
	m := kindMachine("linux", "", false)
	m.JobSlots = 2
	r := newRig(m)
	pool := Scope{Kind: ScopePool}
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 2, Scope: pool}, "u"); err != nil {
		t.Fatalf("two runners on two slots: %v", err)
	}
	_, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 3, Scope: pool}, "u")
	var re *RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "2 job slots") || !strings.Contains(re.Reason, "Job slots") {
		t.Fatalf("three runners on two slots: %v", err)
	}
	m.JobSlots = 0
	r = newRig(m)
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 5, Scope: pool}, "u"); err != nil {
		t.Fatalf("unknown slots are not checked: %v", err)
	}
}

func TestPeakCountsPoolPlusBusiestRepositoryAndSkipsRegistrationsAndFailed(t *testing.T) {
	rs := []Record{
		{MachineID: "m1", Scope: Scope{Kind: ScopePool}},
		{MachineID: "m1", Scope: Scope{Kind: ScopePool}, State: Failed},
		{MachineID: "m1", Scope: Scope{Kind: "repository", RepositoryID: 7}, PoolRunnerID: "x"},
		{MachineID: "m1", Scope: Scope{Kind: "repository", RepositoryID: 7}},
		{MachineID: "m1", Scope: Scope{Kind: "repository", RepositoryID: 8}},
		{MachineID: "m2", Scope: Scope{Kind: ScopePool}},
	}
	n, key := Peak(rs, "m1")
	if n != 2 || key != "repo:7" {
		t.Fatalf("peak = %d %q", n, key)
	}
}
