package runners

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

type fakeUpdater struct {
	reqs []UpdateRequest
	err  error
	// onCall runs inside the update, to read what the store says then.
	onCall func()
}

func (f *fakeUpdater) UpdateRunner(_ context.Context, _ string, req UpdateRequest) error {
	f.reqs = append(f.reqs, req)
	if f.onCall != nil {
		f.onCall()
	}
	return f.err
}

func oldRunner(t *testing.T, r *rig) Record {
	t.Helper()
	rec := r.add(t)
	rec.RunnerVersion = "2.300.0"
	rec.Managed = true
	_ = r.store.InsertRunner(context.Background(), rec)
	return rec
}

// R5.38: drain, update, start again; the result says updated.
func TestUpdateDrainsThenSwaps(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := oldRunner(t, r)
	up := &fakeUpdater{}
	r.svc.Updater = up
	ctx := context.Background()
	// Busy for two polls, then idle: the update waits and never cuts the job.
	r.gh.runners = []github.Runner{{Name: rec.Name, Busy: true}}
	polls := 0
	r.svc.Sleep = func(_ context.Context, d time.Duration) error {
		polls++
		if polls == 2 {
			r.gh.runners = []github.Runner{{Name: rec.Name}}
		}
		if len(up.reqs) != 0 {
			t.Errorf("updated while busy")
		}
		return nil
	}
	res, err := r.svc.UpdateRunner(ctx, rec.ID, "u")
	if err != nil || res.Status != UpdateUpdated || len(up.reqs) != 1 || polls != 2 {
		t.Fatalf("res=%+v err=%v reqs=%v polls=%d", res, err, up.reqs, polls)
	}
	pin, _ := PinFor("linux", "x64")
	if up.reqs[0].Version != pin.Version || up.reqs[0].SHA256 != pin.SHA256 {
		t.Errorf("req = %+v", up.reqs[0])
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if got.State != Idle || got.RunnerVersion != pin.Version {
		t.Errorf("record = %+v", got)
	}
}

// R5.38: a job that never ends is waited for, not cut; the result says so.
func TestUpdateWaitsForTheJob(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := oldRunner(t, r)
	up := &fakeUpdater{}
	r.svc.Updater = up
	r.gh.runners = []github.Runner{{Name: rec.Name, Busy: true}}
	res, err := r.svc.UpdateRunner(context.Background(), rec.ID, "u")
	if err != nil || res.Status != UpdateWaiting || len(up.reqs) != 0 {
		t.Fatalf("res=%+v err=%v reqs=%v", res, err, up.reqs)
	}
}

// R5.38: a failed update shows the reason and the runner is not left updating.
func TestUpdateFailureIsTruthful(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := oldRunner(t, r)
	r.svc.Updater = &fakeUpdater{err: errors.New("runner download refused: hash")}
	ctx := context.Background()
	res, err := r.svc.UpdateRunner(ctx, rec.ID, "u")
	if err != nil || res.Status != UpdateFailed || res.Reason == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got, _ := r.store.GetRunner(ctx, rec.ID)
	if got.State != Idle || got.RunnerVersion != "2.300.0" {
		t.Errorf("record = %+v", got)
	}
	// The runner list cannot be read: fail closed, nothing updated.
	r.svc.Updater = &fakeUpdater{}
	r.gh.listErr = errors.New("github down")
	res, _ = r.svc.UpdateRunner(ctx, rec.ID, "u")
	if res.Status != UpdateFailed || len(r.svc.Updater.(*fakeUpdater).reqs) != 0 {
		t.Fatalf("res=%+v", res)
	}
}

// R5.38: the owner setting turns the automatic run off; an asked update runs.
func TestUpdateAllFollowsTheSetting(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	rec := oldRunner(t, r)
	up := &fakeUpdater{}
	r.svc.Updater = up
	ctx := context.Background()
	on := false
	r.svc.AutoUpdate = func(context.Context) bool { return on }
	if out, _ := r.svc.UpdateAll(ctx, "auto", true); len(out) != 0 || len(up.reqs) != 0 {
		t.Fatalf("setting off but updated: %v", out)
	}
	on = true
	out, _ := r.svc.UpdateAll(ctx, "auto", true)
	if len(out) != 1 || out[0].Status != UpdateUpdated {
		t.Fatalf("out = %+v", out)
	}
	// Now current: nothing more to do.
	if out, _ = r.svc.UpdateAll(ctx, "auto", true); len(out) != 0 {
		t.Fatalf("again = %+v", out)
	}
	_ = rec
}
