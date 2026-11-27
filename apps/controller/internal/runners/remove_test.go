package runners

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// R5.62: the idle check comes first, GitHub's registration goes second, the
// listener stops last, so GitHub never lists a runner with nothing behind it.
func TestIdleCheckThenDeregisterThenStop(t *testing.T) {
	r, rec := idleRig(t)
	var order []string
	g := &orderGitHub{fakeGitHub: r.gh, order: &order}
	g.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "online"}}
	r.svc.GitHub = g
	r.svc.Agent = &orderAgent{fakeAgent: r.ag, order: &order}
	if err := r.svc.RemoveIdleRunner(context.Background(), rec.ID, "demand"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(order); got != "[list delete stop cleanup]" {
		t.Fatalf("order = %s, want [list delete stop cleanup]", got)
	}
}

// R5.62: a 422 on the delete means the runner became busy after the check.
// It is left running and nothing is stopped or cleaned up.
func TestBusy422LeavesTheRunner(t *testing.T) {
	r, rec := idleRig(t)
	r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "online"}}
	r.gh.delErr = fmt.Errorf("%w: /actions/runners/4242", github.ErrRunnerBusy)
	err := r.svc.RemoveIdleRunner(context.Background(), rec.ID, "demand")
	if !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(r.ag.stopped) != 0 || len(r.ag.folders) != 0 {
		t.Fatalf("a runner GitHub calls busy was stopped: %v %v", r.ag.stopped, r.ag.folders)
	}
	if n, _ := r.svc.EphemeralOnMachine(context.Background(), "m1"); n != 1 {
		t.Fatal("the record of a busy runner was dropped")
	}
	assertAudit(t, r.au, "runner.remove_left_busy")
}

// R5.62: a runner GitHub no longer knows counts as already deregistered.
func TestUnknownRunnerCountsAsGone(t *testing.T) {
	for name, setup := range map[string]func(r *rig, rec Record){
		"not listed": func(r *rig, rec Record) {},
		"404 on delete": func(r *rig, rec Record) {
			r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "offline"}}
			r.gh.delErr = fmt.Errorf("%w: /actions/runners/4242", github.ErrNotFound)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, rec := idleRig(t)
			setup(r, rec)
			if err := r.svc.RemoveIdleRunner(context.Background(), rec.ID, "demand"); err != nil {
				t.Fatal(err)
			}
			if len(r.ag.stopped) != 1 || len(r.ag.folders) != 1 {
				t.Fatalf("clean-up did not run: %v %v", r.ag.stopped, r.ag.folders)
			}
			if n, _ := r.svc.EphemeralOnMachine(context.Background(), "m1"); n != 0 {
				t.Fatal("the record stayed")
			}
		})
	}
}

// A busy runner is never deregistered, whatever the caller.
func TestBusyRunnerNeverDeregistered(t *testing.T) {
	r, rec := idleRig(t)
	r.gh.runners = []github.Runner{{ID: 4242, Name: rec.Name, Status: "online", Busy: true}}
	if err := r.svc.RemoveIdleRunner(context.Background(), rec.ID, "demand"); !errors.Is(err, ErrRunnerBusy) {
		t.Fatalf("err = %v", err)
	}
	if len(r.gh.deleted) != 0 {
		t.Fatalf("deleted %v", r.gh.deleted)
	}
}

func assertAudit(t *testing.T, a *fakeAudit, action string) {
	t.Helper()
	for _, e := range a.entries {
		if strings.Contains(e, action) {
			return
		}
	}
	t.Fatalf("no audit entry %q in %v", action, a.entries)
}

// orderGitHub and orderAgent record the order of the calls of one removal.
type orderGitHub struct {
	*fakeGitHub
	order *[]string
}

func (g *orderGitHub) ListRunners(ctx context.Context, i int64, s github.RunnerScope) ([]github.Runner, error) {
	*g.order = append(*g.order, "list")
	return g.fakeGitHub.ListRunners(ctx, i, s)
}
func (g *orderGitHub) DeleteRunner(ctx context.Context, i int64, s github.RunnerScope, id int64) error {
	*g.order = append(*g.order, "delete")
	return g.fakeGitHub.DeleteRunner(ctx, i, s, id)
}

type orderAgent struct {
	*fakeAgent
	order *[]string
}

func (a *orderAgent) StopService(ctx context.Context, m, svc string) error {
	*a.order = append(*a.order, "stop")
	return a.fakeAgent.StopService(ctx, m, svc)
}
func (a *orderAgent) CleanUp(ctx context.Context, m, svc string) error {
	*a.order = append(*a.order, "cleanup")
	return a.fakeAgent.CleanUp(ctx, m, svc)
}
