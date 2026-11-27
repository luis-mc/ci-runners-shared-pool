package repos

import (
	"context"
	"errors"
	"testing"
)

type fakeRegistrar struct {
	calls  int
	actors []string
	err    error
	snap   [][]int64 // pool-on repositories seen at each call
	env    *env
}

func (f *fakeRegistrar) SyncPoolRegistrations(ctx context.Context, actor string) error {
	f.calls++
	f.actors = append(f.actors, actor)
	on := true
	rs, _ := f.env.store.ListRepositories(ctx, ListFilter{Pool: &on})
	var ids []int64
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	f.snap = append(f.snap, ids)
	return f.err
}

// OPS23: the pool switch drives the hidden registrations of the pool runners.
func TestPoolSwitchSyncsRegistrationsAfterTheSwitchIsStored(t *testing.T) {
	e := newEnv(t)
	e.world()
	reg := &fakeRegistrar{env: e}
	e.svc.SetRegistrar(reg)
	e.turnOn(101)
	if reg.calls != 1 || len(reg.snap[0]) != 1 || reg.snap[0][0] != 101 {
		t.Fatalf("on: calls %d, pool-on repositories at the call %v", reg.calls, reg.snap)
	}
	plan, err := e.svc.PlanChanges(context.Background(), adminP, []Change{{RepositoryID: 101, Pool: false}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ApplyPlan(context.Background(), adminP, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if reg.calls != 2 || len(reg.snap[1]) != 0 {
		t.Fatalf("off: calls %d, pool-on repositories at the call %v", reg.calls, reg.snap)
	}
}

func TestRegistrationFailureDoesNotUndoThePoolSwitch(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.svc.SetRegistrar(&fakeRegistrar{env: e, err: errors.New("agent offline")})
	e.turnOn(101)
	if r, _ := e.store.GetRepository(context.Background(), 101); !r.PoolOn {
		t.Fatal("the pool stays on; the reconcile loop tries the registration again")
	}
	if !e.audit.has("pool.registrations_failed|") {
		t.Fatalf("the failure is in the audit log: %v", e.audit.events)
	}
}

func TestReconcileInstallationsSyncsRegistrations(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	reg := &fakeRegistrar{env: e}
	e.svc.SetRegistrar(reg)
	e.fake.Lock(func() { delete(e.fake.Repos, 101) }) // the repository left the installation
	if err := e.svc.ReconcileInstallations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reg.calls != 1 || len(reg.snap[0]) != 0 {
		t.Fatalf("calls %d, pool-on at the call %v: the sync runs after the leaver is off", reg.calls, reg.snap)
	}
	if reg.actors[0] != System.Login {
		t.Errorf("actor = %q", reg.actors[0])
	}
}
