package runners

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// lostRig is a runner the machine says lost its registration: the listener log
// says so and GitHub lists no runner.
func lostRig(t *testing.T) (*rig, Record) {
	t.Helper()
	r := newRig(machine("linux", "x64", true))
	rec := r.add(t)
	r.svc.Services = func(context.Context, string) ([]backend.Found, error) {
		return []backend.Found{{ServiceName: rec.ServiceName, Name: rec.Name, Running: false, RegistrationLost: true}}, nil
	}
	return r, rec
}

// CRSP382, R5.140: the truth of a runner GitHub does not list while its
// listener says the registration was deleted is not idle, and not "stopped".
func TestTruthRegistrationLostIsNotIdle(t *testing.T) {
	lost := &backend.Found{Running: false, RegistrationLost: true}
	got, why := Truth(Record{State: Idle}, Facts{GitHubKnown: true, ServiceKnown: true, Service: lost})
	if got != NotRegistered || why != RegistrationLostReason {
		t.Fatalf("%s (%s)", got, why)
	}
	// GitHub lists it: the log is old, the registration is there.
	on := &github.Runner{Name: "a", Status: "online"}
	got, _ = Truth(Record{State: Idle}, Facts{GitHubKnown: true, GitHub: on, ServiceKnown: true, Service: &backend.Found{Running: true, RegistrationLost: true}})
	if got != Idle {
		t.Fatalf("a listed runner is idle, got %s", got)
	}
	// Registered again once: idle, and the page says so.
	got, why = Truth(Record{State: Idle, Reason: ReregisteredReason}, Facts{GitHubKnown: true, GitHub: on, ServiceKnown: true, Service: &backend.Found{Running: true}})
	if got != Idle || why != ReregisteredReason {
		t.Fatalf("%s (%s)", got, why)
	}
}

// CRSP382, R5.140: a lost registration is registered again once, with a fresh
// token, and recorded.
func TestRecoverRegistersAgainOnce(t *testing.T) {
	r, rec := lostRig(t)
	ctx := context.Background()
	r.ag.added, r.ag.cfgs, r.gh.regCalls = nil, nil, 0
	res, err := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if err != nil || len(res.Registered) != 1 || res.Registered[0] != rec.Name {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.ag.added) != 1 || r.ag.cfgs[0].Registration != "REG-TOKEN-1" || r.ag.cfgs[0].Removal != "" || r.gh.regCalls != 1 {
		t.Fatalf("one add with one fresh registration token and no removal: %+v %+v calls %d", r.ag.added, r.ag.cfgs, r.gh.regCalls)
	}
	if r.ag.added[0].Name != rec.Name || r.ag.added[0].GitHubURL == "" {
		t.Errorf("spec %+v", r.ag.added[0])
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if got.State != Idle || got.Reason != ReregisteredReason {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
	if !strings.Contains(strings.Join(r.au.entries, "\n"), "runner.reregistered") {
		t.Errorf("not recorded: %v", r.au.entries)
	}
	// Lost again at once: not registered a second time, shown failed with the reason.
	res, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(r.ag.added) != 1 || len(res.Failed) != 1 {
		t.Fatalf("a second loss is not repaired in a loop: adds %d, %+v", len(r.ag.added), res)
	}
	got, _ = r.store.GetRunner(ctx, rec.ID)
	if got.State != Failed || !strings.HasPrefix(got.Reason, ReregisterFailedPrefix) {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
	// A failed runner is left alone by the pass (no loop at all).
	_, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(r.ag.added) != 1 {
		t.Fatal("a failed runner is never registered again by the pass")
	}
}

// CRSP382, R5.140: when the add fails, the runner is failed with the reason,
// and the button registers it again.
func TestRecoverGivesUpWithTheReasonThenTheButtonWorks(t *testing.T) {
	r, rec := lostRig(t)
	ctx := context.Background()
	r.ag.addErr = errors.New("register again: config.sh exited 1")
	res, err := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if err != nil || len(res.Failed) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if got.State != Failed || !strings.Contains(got.Reason, "config.sh exited 1") || !strings.HasPrefix(got.Reason, ReregisterFailedPrefix) {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
	v, err := r.svc.GetView(ctx, rec.ID)
	if err != nil || v.State != Failed || !v.CanReregister || !strings.Contains(v.Reason, "config.sh exited 1") {
		t.Fatalf("view %+v %v", v, err)
	}
	// the owner presses Register again
	r.ag.addErr = nil
	v, err = r.svc.ReregisterRunner(ctx, rec.ID, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = r.store.GetRunner(ctx, rec.ID)
	if got.State != Idle || got.Reason != ReregisteredReason {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
}

// A runner GitHub lists, or one with a job, or GitHub unreadable: left alone.
func TestRecoverLeavesWhatIsNotLost(t *testing.T) {
	r, rec := lostRig(t)
	ctx := context.Background()
	r.ag.added = nil
	r.gh.runners = []github.Runner{{ID: 9, Name: rec.Name, Status: "offline"}}
	if res, _ := r.svc.RecoverLostRegistrations(ctx, "recovery"); len(res.Registered)+len(res.Failed) != 0 || len(r.ag.added) != 0 {
		t.Fatalf("GitHub lists it: %+v", res)
	}
	r.gh.runners, r.gh.listErr = nil, errors.New("rate limit")
	if res, _ := r.svc.RecoverLostRegistrations(ctx, "recovery"); len(res.Left) != 1 || len(r.ag.added) != 0 {
		t.Fatalf("GitHub unreadable changes nothing: %+v", res)
	}
	r.gh.listErr = nil
	if _, err := r.svc.ReregisterRunner(ctx, rec.ID, "u"); err != nil {
		t.Fatal(err) // the owner may press it for a runner GitHub does not list
	}
	r.gh.runners = []github.Runner{{ID: 9, Name: rec.Name, Status: "online"}}
	var re *RefusalError
	if _, err := r.svc.ReregisterRunner(ctx, rec.ID, "u"); !errors.As(err, &re) {
		t.Fatalf("GitHub lists it, nothing to do: %v", err)
	}
}

// CRSP382: a runner stuck in creating is settled, not left for ever.
func TestSettleStuckCreating(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	rec := Record{ID: "r_1", Name: "box-1", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent", State: Creating, Managed: true,
		ServiceName: "svc", CreatedAt: r.clock}
	_ = r.store.InsertRunner(ctx, rec)
	if got, _ := r.svc.SettleStuckCreating(ctx, 45*time.Minute, "recovery"); len(got) != 0 {
		t.Fatalf("a young runner is left: %v", got)
	}
	r.clock = r.clock.Add(time.Hour)
	got, _ := r.svc.SettleStuckCreating(ctx, 45*time.Minute, "recovery")
	if len(got) != 1 {
		t.Fatalf("settled %v", got)
	}
	g, _ := r.store.GetRunner(ctx, "r_1")
	if g.State != Failed || g.Reason != StuckCreatingReason {
		t.Fatalf("%s %q", g.State, g.Reason)
	}
	// A running plan is never disturbed.
	_ = r.store.InsertRunner(ctx, Record{ID: "r_2", Name: "box-2", MachineID: "m1", State: Creating, Managed: true, CreatedAt: r.clock.Add(-2 * time.Hour)})
	r.svc.beginRun("p1")
	if got, _ := r.svc.SettleStuckCreating(ctx, 45*time.Minute, "recovery"); len(got) != 0 {
		t.Fatalf("a plan runs: %v", got)
	}
}

func TestCanRegisterAgainOnlyWhereItHelps(t *testing.T) {
	rec := Record{Managed: true, Kind: "persistent"}
	for _, c := range []struct {
		st   State
		why  string
		want bool
	}{
		{NotRegistered, "x", true}, {Failed, ReregisterFailedPrefix + "x", true}, {Failed, "the runner did not start", false}, {Idle, "", false}, {Stopped, "x", false},
	} {
		if got := CanReregister(rec, c.st, c.why); got != c.want {
			t.Errorf("%s %q: %v", c.st, c.why, got)
		}
	}
	if CanReregister(Record{Managed: false, Kind: "persistent"}, NotRegistered, "") || CanReregister(Record{Managed: true, Kind: "ephemeral"}, NotRegistered, "") {
		t.Error("not for a runner CRSP did not make or an ephemeral one")
	}
}
