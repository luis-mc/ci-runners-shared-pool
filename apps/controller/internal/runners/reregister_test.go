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

// CRSP382, R5.152: the truth of a runner GitHub does not list while its
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

// CRSP382, R5.152: a lost registration is registered again once, with a fresh
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

// CRSP382, R5.152: when the add fails, the runner is failed with the reason,
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

// poolRegRig is a pool runner with one hidden registration in the given state
// and reason, as the owner's PC had them after 0.1.31 (CRSP385).
func poolRegRig(t *testing.T, st State, reason string) (*rig, Record) {
	t.Helper()
	r := newRig(machine("linux", "x64", true))
	ctx := context.Background()
	parent := Record{ID: "r_pool", Name: "gameroompc-pool-1", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent",
		Backend: "docker", Scope: Scope{Kind: ScopePool}, State: Idle, Managed: true, CreatedAt: r.clock}
	reg := Record{ID: "r_reg", Name: "gameroompc-pool-1-r1378816711", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent",
		Backend: "docker", Scope: Scope{Kind: "repository", RepositoryID: 1378816711}, State: st, Reason: reason, FailedReason: reason,
		Managed: true, ServiceName: "crsp-persistent-pool:gameroompc-pool-1-r1378816711", PoolRunnerID: "r_pool", CreatedAt: r.clock}
	for _, x := range []Record{parent, reg} {
		if err := r.store.InsertRunner(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	// the machine's list no longer has the runner: its entry file is gone
	r.svc.Services = func(context.Context, string) ([]backend.Found, error) { return nil, nil }
	r.svc.Release = "0.1.32"
	return r, reg
}

// CRSP385: 0.1.31 failed to register the lost runners again (config.sh said
// "already configured") and wrote the reason without a release. The next
// release tries once by itself; a failure it writes names it, so it is not
// tried again until a newer release, and the log gets the reason.
func TestRecoverTriesAFailureOfAnOlderReleaseOnce(t *testing.T) {
	old := ReregisterFailedPrefix + "register: exit status 1: Cannot configure the runner because it is already configured."
	r, reg := poolRegRig(t, Failed, old)
	ctx := context.Background()
	r.ag.addErr = errors.New("register: config.sh exited 1")
	res, err := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if err != nil || len(res.Failed) != 1 || len(r.ag.added) != 1 || !strings.Contains(res.Why[reg.Name], "config.sh exited 1") {
		t.Fatalf("one try with the reason for the log: %+v adds %d %v", res, len(r.ag.added), err)
	}
	got, _ := r.store.GetRunner(ctx, reg.ID)
	if got.State != Failed || !strings.HasPrefix(got.Reason, ReregisterFailedPrefix) || !strings.HasSuffix(got.Reason, "(crsp 0.1.32)") {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
	for i := 0; i < 3; i++ {
		_, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	}
	if len(r.ag.added) != 1 {
		t.Fatalf("the same release tries once, adds %d", len(r.ag.added))
	}
	if v, _ := r.svc.GetView(ctx, reg.ID); !v.CanReregister {
		t.Fatal("then the button")
	}
	// a newer release tries once more, and this time it works
	r.svc.Release, r.ag.addErr = "0.1.33", nil
	res, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(res.Registered) != 1 || len(r.ag.added) != 2 || r.ag.cfgs[1].Registration == "" {
		t.Fatalf("%+v adds %d", res, len(r.ag.added))
	}
	got, _ = r.store.GetRunner(ctx, reg.ID)
	if got.State != Idle || got.Reason != ReregisteredReason {
		t.Fatalf("record %s %q", got.State, got.Reason)
	}
}

// CRSP385: a hidden pool registration whose add was cut by a restart stays in
// creating, is settled failed, and is then set up again by the product: the
// owner never removes and adds it by hand.
func TestStuckPoolRegistrationIsSetUpAgain(t *testing.T) {
	r, reg := poolRegRig(t, Creating, "")
	ctx := context.Background()
	r.clock = r.clock.Add(time.Hour)
	if got, _ := r.svc.SettleStuckCreating(ctx, 45*time.Minute, "recovery"); len(got) != 1 {
		t.Fatalf("settled %v", got)
	}
	res, err := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if err != nil || len(res.Registered) != 1 || len(r.ag.added) != 1 || r.ag.added[0].Name != reg.Name {
		t.Fatalf("%+v %v adds %+v", res, err, r.ag.added)
	}
	got, _ := r.store.GetRunner(ctx, reg.ID)
	if got.State != Idle || got.ServiceName == "" {
		t.Fatalf("record %s %q service %q", got.State, got.Reason, got.ServiceName)
	}
}

// CRSP385: a failed hidden registration (win-1's adds failed on 0.1.28 with
// "list listeners: powershell exited 1" and cleaned up) is set up whole again
// once, and a failure names this release.
func TestFailedPoolRegistrationOfAnOldReleaseIsSetUpAgainOnce(t *testing.T) {
	r, reg := poolRegRig(t, Failed, "list listeners: powershell exited 1. Cleaned up: account crsp-x, folder of the runner")
	ctx := context.Background()
	r.ag.addErr = errors.New("the service did not start")
	res, _ := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(res.Failed) != 1 || len(r.ag.added) != 1 {
		t.Fatalf("%+v adds %d", res, len(r.ag.added))
	}
	got, _ := r.store.GetRunner(ctx, reg.ID)
	if !strings.HasPrefix(got.Reason, SetUpAgainFailedPrefix) || !strings.Contains(got.Reason, "the service did not start") || !strings.Contains(got.Reason, "(crsp 0.1.32)") {
		t.Fatalf("reason %q", got.Reason)
	}
	_, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(r.ag.added) != 1 {
		t.Fatal("once per release")
	}
}

// CRSP385: what the retry must not touch: GitHub lists it, GitHub unreadable,
// the scope gate refuses, the pool runner is going away, a runner with no
// pool that failed for another reason.
func TestRetryLeavesWhatItMustNot(t *testing.T) {
	ctx := context.Background()
	old := ReregisterFailedPrefix + "x"
	r, reg := poolRegRig(t, Failed, old)
	r.gh.runners = []github.Runner{{ID: 1, Name: reg.Name, Status: "offline"}}
	if res, _ := r.svc.RecoverLostRegistrations(ctx, "recovery"); len(r.ag.added) != 0 || len(res.Left) != 1 {
		t.Fatalf("GitHub lists it: %+v", res)
	}
	r.gh.runners, r.gh.listErr = nil, errors.New("rate limit")
	if _, _ = r.svc.RecoverLostRegistrations(ctx, "recovery"); len(r.ag.added) != 0 {
		t.Fatal("GitHub unreadable")
	}
	r.gh.listErr = nil
	r.svc.Gate = func(context.Context, Scope) error { return refuse("public repository") }
	if _, _ = r.svc.RecoverLostRegistrations(ctx, "recovery"); len(r.ag.added) != 0 {
		t.Fatal("the gate refuses")
	}
	r.svc.Gate = nil
	_ = r.store.SetRunnerState(ctx, "r_pool", Removing, "")
	if _, _ = r.svc.RecoverLostRegistrations(ctx, "recovery"); len(r.ag.added) != 0 {
		t.Fatal("the pool runner is going away")
	}
	plain := Record{ID: "r_plain", Name: "box-9", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent", State: Failed,
		Reason: "the image build failed", Managed: true, Scope: Scope{Kind: "repository", RepositoryID: 1}}
	_ = r.store.InsertRunner(ctx, plain)
	r.svc.Release = "0.1.40"
	_, _ = r.svc.RecoverLostRegistrations(ctx, "recovery")
	for _, s := range r.ag.added {
		if s.Name == plain.Name {
			t.Fatal("a runner outside the pool that failed for another reason waits for the owner")
		}
	}
}

// CRSP385: a hidden registration its machine does not list at all (the entry
// file was removed by the failed add) is set up again like a lost one.
func TestPoolRegistrationMissingOnItsMachineIsSetUpAgain(t *testing.T) {
	r, reg := poolRegRig(t, Idle, "registered")
	ctx := context.Background()
	res, _ := r.svc.RecoverLostRegistrations(ctx, "recovery")
	if len(res.Registered) != 1 || len(r.ag.added) != 1 {
		t.Fatalf("%+v", res)
	}
	// a machine list that cannot be read changes nothing
	r2, _ := poolRegRig(t, Idle, "registered")
	r2.svc.Services = func(context.Context, string) ([]backend.Found, error) { return nil, errors.New("agent offline") }
	if _, _ = r2.svc.RecoverLostRegistrations(ctx, "recovery"); len(r2.ag.added) != 0 {
		t.Fatal("an unread list is not a missing runner")
	}
	_ = reg
}
