package repos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

func ip(i int) *int            { return &i }
func bp(b bool) *bool          { return &b }
func sp(s ...string) *[]string { return &s }

func TestDefaultPolicy(t *testing.T) { // R5.25, R5.26
	p := DefaultPolicy()
	if p.MaxParallelJobs != 2 || p.Priority != 0 || p.AllowForkPRs || len(p.Labels) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestPolicyUpdateValidation(t *testing.T) { // R5.25
	base := DefaultPolicy()
	got, err := PolicyUpdate{Priority: ip(10), MaxParallelJobs: ip(4)}.Apply(base)
	if err != nil || got.Priority != 10 || got.MaxParallelJobs != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	for name, u := range map[string]PolicyUpdate{
		"priority high": {Priority: ip(101)},
		"priority low":  {Priority: ip(-101)},
		"parallel zero": {MaxParallelJobs: ip(0)},
		"parallel huge": {MaxParallelJobs: ip(257)},
		"memory budget": {BudgetMemoryPct: ip(101)},
		"empty group":   {RunnerGroups: sp(" ")},
		"project label": {Labels: sp("my-secret-project")},
	} {
		if _, err := u.Apply(base); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	got, _ = PolicyUpdate{BudgetMemoryPct: ip(50), BudgetCPUPct: ip(40)}.Apply(base)
	if got.BudgetMemoryPct != 50 || got.BudgetCPUPct != 40 {
		t.Fatalf("%+v", got)
	}
	got, _ = PolicyUpdate{BudgetMemoryPct: ip(0)}.Apply(got)
	if got.BudgetMemoryPct != 0 {
		t.Fatal("0 clears a budget")
	}
	got, _ = PolicyUpdate{Labels: sp("docker", "Docker", " gpu ")}.Apply(base)
	if strings.Join(got.Labels, ",") != "docker,gpu" {
		t.Fatalf("labels are trimmed and de-duplicated: %v", got.Labels)
	}
}

func TestForkPullRequestsAskFirst(t *testing.T) { // R5.26
	base := DefaultPolicy()
	if _, err := (PolicyUpdate{AllowForkPRs: bp(true)}).Apply(base); !errors.Is(err, ErrForkNeedsConfirm) {
		t.Fatalf("%v", err)
	}
	on, err := PolicyUpdate{AllowForkPRs: bp(true), ConfirmForkRisk: true}.Apply(base)
	if err != nil || !on.AllowForkPRs {
		t.Fatal(err)
	}
	// Turning off, or already on, needs no question.
	if p, err := (PolicyUpdate{AllowForkPRs: bp(false)}).Apply(on); err != nil || p.AllowForkPRs {
		t.Fatal(err)
	}
	if _, err := (PolicyUpdate{AllowForkPRs: bp(true)}).Apply(on); err != nil {
		t.Fatalf("an unchanged setting does not ask again: %v", err)
	}
}

func TestPolicyExtraPackages(t *testing.T) { // runners R5.51
	base := DefaultPolicy()
	text := "zlib1g-dev\n\n# note\nlibc++-dev\nzlib1g-dev"
	got, err := PolicyUpdate{ExtraPackages: &text}.Apply(base)
	if err != nil || strings.Join(got.ExtraPackages, ",") != "libc++-dev,zlib1g-dev" {
		t.Fatalf("%v %v", got.ExtraPackages, err)
	}
	bad := "zlib1g-dev\n-evil"
	kept, err := PolicyUpdate{ExtraPackages: &bad}.Apply(got)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("a bad name is refused with its line: %v", err)
	}
	if strings.Join(kept.ExtraPackages, ",") != "libc++-dev,zlib1g-dev" {
		t.Fatal("a refused list changes nothing")
	}
	same, _ := PolicyUpdate{Priority: ip(3)}.Apply(got)
	if len(same.ExtraPackages) != 2 {
		t.Fatal("another change keeps the list")
	}
	empty := ""
	cleared, _ := PolicyUpdate{ExtraPackages: &empty}.Apply(got)
	if len(cleared.ExtraPackages) != 0 {
		t.Fatal("empty text clears the list")
	}
}

func TestUpdatePolicyEndToEnd(t *testing.T) { // R5.25, R5.26, R5.28
	e := newEnv(t)
	e.world()
	e.turnOn(100)
	ctx := context.Background()
	got, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{MaxParallelJobs: ip(3), Priority: ip(5), Labels: sp("gpu")})
	if err != nil || got.MaxParallelJobs != 3 || got.UpdatedBy != "alice" {
		t.Fatalf("%+v %v", got, err)
	}
	v, _ := e.fake.Variable("alice/web", "CRSP_RUNS_ON_LINUX")
	if !strings.Contains(v, `"gpu"`) {
		t.Fatalf("a policy change rewrites the variables: %s", v)
	}
	if !e.audit.has("policy.changed") {
		t.Fatal("audit")
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{AllowForkPRs: bp(true)}); !errors.Is(err, ErrForkNeedsConfirm) {
		t.Fatalf("%v", err)
	}
	if pol, _ := e.store.GetPolicy(ctx, 100); pol.AllowForkPRs {
		t.Fatal("not saved without a yes")
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{AllowForkPRs: bp(true), ConfirmForkRisk: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{Labels: sp("project-x")}); err == nil {
		t.Fatal("non-generic label refused")
	}
}

func TestUpdatePolicyNeedsGitHubAdmin(t *testing.T) { // R5.19
	e := newEnv(t)
	e.world()
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 102, PolicyUpdate{Priority: ip(1)}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("%v", err)
	}
	if _, err := e.svc.UpdatePolicy(context.Background(), adminP, 999, PolicyUpdate{}); !errors.Is(err, auth.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestPolicyTurnedOffRestoresForkGuard(t *testing.T) { // R5.26
	e := newEnv(t)
	e.world()
	e.turnOn(100)
	ctx := context.Background()
	_, _ = e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{AllowForkPRs: bp(true), ConfirmForkRisk: true})
	e.fake.Lock(func() { e.fake.ForkApproval["alice/web"] = "first_time_contributors" })
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{AllowForkPRs: bp(false)}); err != nil {
		t.Fatal(err)
	}
	if e.fake.ForkApprovalOf("alice/web") != github.ForkApprovalAllOutside {
		t.Fatal("turning fork jobs off sets the approval guard again")
	}
}

func TestViewDefaults(t *testing.T) {
	e := newEnv(t)
	e.world()
	v, err := e.svc.Get(context.Background(), viewerP, 100)
	if err != nil || v.Policy.MaxParallelJobs != 2 || v.UserIsAdmin {
		t.Fatalf("a repository never enabled shows the default policy: %+v %v", v, err)
	}
	if _, err := e.svc.Get(context.Background(), auth.Principal{}, 100); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatal(err)
	}
}
