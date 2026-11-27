package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// legacyLightRig has a service whose background work runs at once and whose
// INFO lines are kept.
func legacyLightRig(t *testing.T) (*rig, *[]string) {
	t.Helper()
	r := newRig(machine("linux", "x64", true))
	r.svc.Background = func(f func()) { f() }
	var lines []string
	r.svc.LogInfo = func(l string) { lines = append(lines, l) }
	return r, &lines
}

func insertLight(t *testing.T, r *rig, id, name string, state State) {
	t.Helper()
	err := r.store.InsertRunner(context.Background(), Record{ID: id, Name: name, MachineID: "m1", OS: "windows", Managed: true,
		Kind: "persistent", State: state, ServiceName: "actions.runner.o-r." + name,
		Labels: []string{"self-hosted", "windows", "x64", LegacyLightLabel}, Route: Route{Queue: legacyQueueLight},
		Scope: Scope{Kind: "repository", RepositoryID: 1}})
	if err != nil {
		t.Fatal(err)
	}
}

func runnerCount(t *testing.T, r *rig) int {
	t.Helper()
	all, err := r.store.ListRunners(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(all)
}

// OPS49 (D110): nothing is done when no light runner exists.
func TestRemoveLegacyLightNothingToDo(t *testing.T) {
	r, lines := legacyLightRig(t)
	r.add(t) // an ordinary runner stays
	res, err := r.svc.RemoveLegacyLight(context.Background(), "controller")
	if err != nil || len(res.Started)+len(res.Waiting)+len(res.GaveUp) != 0 || len(*lines) != 0 {
		t.Fatalf("%+v %v %v", res, err, *lines)
	}
	if runnerCount(t, r) != 1 || r.gh.rmCalls != 0 || len(r.ag.removed) != 0 {
		t.Fatal("an ordinary runner was touched")
	}
}

// Idle light runners are drained and removed: registration (remove token),
// service, account and folder through the agent, then the record.
func TestRemoveLegacyLightRemovesIdleRunners(t *testing.T) {
	r, lines := legacyLightRig(t)
	keep := r.add(t)
	insertLight(t, r, "a", "crsp-light-comics-organizer-windows", Failed)
	insertLight(t, r, "b", "crsp-light-home-automation-windows", Idle)
	// Found by the label alone, with a plain name and no stored route.
	_ = r.store.InsertRunner(context.Background(), Record{ID: "c", Name: "box-9", MachineID: "m1", Managed: true, State: Idle,
		Labels: []string{"self-hosted", "CRSP-Light"}, Scope: Scope{Kind: "repository", RepositoryID: 1}, ServiceName: "actions.runner.o-r.box-9"})
	res, err := r.svc.RemoveLegacyLight(context.Background(), "controller")
	if err != nil || len(res.Started) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	all, _ := r.store.ListRunners(context.Background(), Filter{})
	if len(all) != 1 || all[0].ID != keep.ID {
		t.Fatalf("left: %+v", all)
	}
	if r.gh.rmCalls != 2 || len(r.ag.removed) != 2 { // "a" is failed: its leftovers go through the clean-up path
		t.Fatalf("remove tokens %d, agent removals %d", r.gh.rmCalls, len(r.ag.removed))
	}
	started, removed := 0, 0
	for _, l := range *lines {
		if strings.Contains(l, "removing") {
			started++
		}
		if strings.HasSuffix(l, "removed") {
			removed++
		}
	}
	if started != 3 || removed != 3 {
		t.Fatalf("INFO lines: %v", *lines)
	}
	// A second pass finds nothing.
	n := len(*lines)
	res, err = r.svc.RemoveLegacyLight(context.Background(), "controller")
	if err != nil || len(res.Started) != 0 || len(*lines) != n {
		t.Fatalf("second pass %+v %v %v", res, err, (*lines)[n:])
	}
}

// A runner with a job is never stopped: it waits, and goes after its job.
func TestRemoveLegacyLightBusyRunnerWaits(t *testing.T) {
	r, lines := legacyLightRig(t)
	insertLight(t, r, "a", "crsp-light-comics-organizer-windows", Busy)
	r.gh.runners = []github.Runner{{ID: 7, Name: "crsp-light-comics-organizer-windows", Busy: true}}
	ctx := context.Background()
	res, err := r.svc.RemoveLegacyLight(ctx, "controller")
	if err != nil || len(res.Waiting) != 1 || len(res.Started) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if runnerCount(t, r) != 1 || r.gh.rmCalls != 0 || len(r.ag.removed) != 0 {
		t.Fatal("a busy runner was touched")
	}
	if !strings.Contains((*lines)[0], "removed after the job") {
		t.Fatalf("line %q", (*lines)[0])
	}
	// The job ends.
	r.gh.runners[0].Busy = false
	res, err = r.svc.RemoveLegacyLight(ctx, "controller")
	if err != nil || len(res.Started) != 1 || runnerCount(t, r) != 0 {
		t.Fatalf("%+v %v left %d", res, err, runnerCount(t, r))
	}
	// Waiting for a job is not a failed try.
	if r.svc.triesOf("a") != 0 {
		t.Fatalf("tries %d", r.svc.triesOf("a"))
	}
}

// A failure keeps the runner with its reason, is tried again by each pass and
// stops after LegacyLightMaxTries.
func TestRemoveLegacyLightRetriesFiveTimes(t *testing.T) {
	r, _ := legacyLightRig(t)
	insertLight(t, r, "a", "crsp-light-home-automation-windows", Idle)
	r.ag.rmErr = errors.New("the service cannot be stopped")
	ctx := context.Background()
	for i := 1; i <= LegacyLightMaxTries; i++ {
		res, err := r.svc.RemoveLegacyLight(ctx, "controller")
		if err != nil || len(res.Started) != 1 {
			t.Fatalf("pass %d: %+v %v", i, res, err)
		}
	}
	rec, err := r.store.GetRunner(ctx, "a")
	if err != nil || !strings.Contains(rec.Reason, "the service cannot be stopped") || rec.State == Draining || rec.State == Removing {
		t.Fatalf("record %+v %v", rec, err)
	}
	calls := r.gh.rmCalls
	res, _ := r.svc.RemoveLegacyLight(ctx, "controller")
	if len(res.GaveUp) != 1 || len(res.Started) != 0 || r.gh.rmCalls != calls {
		t.Fatalf("after the limit: %+v, tokens %d -> %d", res, calls, r.gh.rmCalls)
	}
	// The cause goes away and a person removes it by hand: nothing is left to do.
	if runnerCount(t, r) != 1 {
		t.Fatal("the runner must stay for the doctor")
	}
}

// An unreadable runner list never removes anything (fails closed).
func TestRemoveLegacyLightListErrorRemovesNothing(t *testing.T) {
	r, _ := legacyLightRig(t)
	insertLight(t, r, "a", "crsp-light-x-windows", Idle)
	r.gh.listErr = errors.New("rate limited")
	res, err := r.svc.RemoveLegacyLight(context.Background(), "controller")
	if err != nil || len(res.Started) != 0 || runnerCount(t, r) != 1 || r.gh.rmCalls != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNewRunnersNeverCarryTheLightLabel(t *testing.T) {
	if _, err := ValidateLabels([]string{"self-hosted", "linux", "x64", LegacyLightLabel}, "pool-a", nil); err == nil {
		t.Fatal("the removed light label was accepted")
	}
	if RouteForLabels("p", []string{LegacyLightLabel}).Queue != QueueCI {
		t.Fatal("the light label routed a runner")
	}
}
