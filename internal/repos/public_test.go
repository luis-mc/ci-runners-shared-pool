package repos

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
)

type fakeFleet struct {
	mu      sync.Mutex
	removed []int64
}

func (f *fakeFleet) RemoveRepositoryRunners(_ context.Context, id int64, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	return []string{"crsp-job-1"}, nil
}

func publicEnv(t *testing.T) (*env, *fakeFleet) {
	t.Helper()
	f := &fakeFleet{}
	e := newEnv(t)
	e.svc.cfg.Fleet = f
	e.world()
	e.turnOn(101)
	return e, f
}

func (e *env) variable(name string) string {
	var v string
	e.fake.Lock(func() { v = e.fake.Variables["alice/secret"][name] })
	return v
}

// CRSP131: a served repository that turns public is NOT stopped any more
// (R5.41): it keeps working, the visibility is stored, and while the setting is
// off the repository carries the problem public_pool_on_while_off.
func TestPublicizedKeepsServingAndShowsProblem(t *testing.T) {
	e, fleet := publicEnv(t)
	ctx := context.Background()
	e.setPublic(false)
	before := e.variable("CRSP_RUNS_ON_LINUX")
	e.fake.Lock(func() { e.fake.Repos[101].Private = false })
	if err := e.svc.Publicized(ctx, "Alice/Secret"); err != nil {
		t.Fatal(err)
	}
	r, _ := e.store.GetRepository(ctx, 101)
	if !r.PoolOn || r.Visibility != "public" {
		t.Errorf("pool on %v, visibility %q", r.PoolOn, r.Visibility)
	}
	if v := e.variable("CRSP_RUNS_ON_LINUX"); v != before {
		t.Errorf("CRSP_RUNS_ON_LINUX changed to %s", v)
	}
	if len(fleet.removed) != 0 {
		t.Errorf("nothing may be removed: fleet %v", fleet.removed)
	}
	if !e.audit.has("repository.publicized|controller|alice/secret") || e.audit.has("pool.off") {
		t.Errorf("audit: %v", e.audit.events)
	}
	v, err := e.svc.Get(ctx, adminP, 101)
	if err != nil {
		t.Fatal(err)
	}
	if v.Problem == nil || v.Problem.Code != ProblemPublicPoolOnWhileOff || len(v.Problem.Actions) != 2 ||
		v.Problem.Actions[0].ID != ActionTurnOnPublic || v.Problem.Actions[1].ID != ActionTurnOffPool ||
		!strings.Contains(v.Problem.Text, "alice/secret") {
		t.Fatalf("problem: %+v", v.Problem)
	}
	// With the setting on there is no problem and nothing stops it.
	e.setPublic(true)
	if v, _ = e.svc.Get(ctx, adminP, 101); v.Problem != nil {
		t.Errorf("the setting is on, yet: %+v", v.Problem)
	}
	// A repeat event and an unknown repository change nothing and do not fail.
	if err := e.svc.Publicized(ctx, "alice/secret"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Publicized(ctx, "nobody/nothing"); err != nil {
		t.Fatal(err)
	}
}

// CRSP131: the periodic visibility check only stores what it reads; a public
// repository already on the pool stays on.
func TestReconcileKeepsPublicRepositoryServed(t *testing.T) {
	e, fleet := publicEnv(t)
	ctx := context.Background()
	e.setPublic(false)
	e.fake.Lock(func() { e.fake.Repos[101].Private = false })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := e.store.GetRepository(ctx, 101)
	if !r.PoolOn || r.Visibility != "public" || len(fleet.removed) != 0 {
		t.Fatalf("pool on %v, visibility %q, fleet %v", r.PoolOn, r.Visibility, fleet.removed)
	}
	if v := e.variable("CRSP_RUNS_ON_LINUX"); !strings.Contains(v, "crsp-home") {
		t.Errorf("the variable was rewritten: %s", v)
	}
	if v, _ := e.svc.Get(ctx, adminP, 101); v.Problem == nil {
		t.Error("the problem is missing while the setting is off")
	}
}

// The problem is only for a public repository whose pool is on.
func TestNoProblemForPrivateOrPoolOff(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.setPublic(false)
	ctx := context.Background()
	for _, id := range []int64{100, 101} { // 100 public, pool off; 101 private, pool off
		if v, err := e.svc.Get(ctx, adminP, id); err != nil || v.Problem != nil {
			t.Errorf("repository %d: %+v %v", id, v.Problem, err)
		}
	}
	e.turnOn(101)
	if v, _ := e.svc.Get(ctx, adminP, 101); v.Problem != nil {
		t.Errorf("a private repository on the pool: %+v", v.Problem)
	}
}

// CRSP131: while the setting is off a public repository cannot be turned on,
// by plan or by a plan made earlier; a private one can; with the setting on a
// public one can.
func TestTurnOnPublicRefusedWhileOff(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.setPublic(false)
	_, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	var po *PublicOffError
	if !errors.As(err, &po) || po.Repo != "alice/web" || !strings.Contains(err.Error(), "Public repositories") {
		t.Fatalf("plan: %v", err)
	}
	if r, _ := e.store.GetRepository(ctx, 100); r.PoolOn {
		t.Fatal("the pool was turned on")
	}
	// A plan made while the setting was on is refused when it applies.
	e.setPublic(true)
	plan, err := e.svc.PlanChanges(ctx, adminP, []Change{{RepositoryID: 100, Pool: true}})
	if err != nil {
		t.Fatal(err)
	}
	e.setPublic(false)
	if _, err := e.svc.ApplyPlan(ctx, adminP, plan.ID, nil); !errors.As(err, &po) {
		t.Fatalf("apply: %v", err)
	}
	if r, _ := e.store.GetRepository(ctx, 100); r.PoolOn {
		t.Fatal("the stale plan turned the pool on")
	}
	// A private repository is not affected.
	e.turnOn(101)
	// With the setting on a public repository can be turned on (R5.26 fork guard still runs).
	e.setPublic(true)
	e.turnOn(100)
	if r, _ := e.store.GetRepository(ctx, 100); !r.PoolOn {
		t.Fatal("the setting is on, yet the pool stayed off")
	}
	// A setting that cannot be read refuses a public repository.
	e.svc.cfg.PublicRepositories = func(context.Context) (bool, error) { return false, errors.New("db down") }
	if err := e.svc.RequirePublicAllowed(ctx, Repository{FullName: "a/b", Visibility: "public"}); err == nil {
		t.Error("an unreadable setting must refuse")
	}
	if err := e.svc.RequirePublicAllowed(ctx, Repository{FullName: "a/b", Visibility: "private"}); err != nil {
		t.Errorf("a private repository needs no setting: %v", err)
	}
	// Nothing wired means off.
	e.svc.cfg.PublicRepositories = nil
	if err := e.svc.RequirePublicAllowedByID(ctx, 100); !errors.As(err, &po) {
		t.Errorf("no setting source must mean off: %v", err)
	}
	if err := e.svc.RequirePublicAllowedByID(ctx, 99999); err != nil {
		t.Errorf("an unknown id is not this check's business: %v", err)
	}
}

// CRSP131: switching the setting off stops every public repository on the
// pool, keeps its policy, and leaves private ones alone. It reports how many.
func TestStopPublicOnPoolKeepsPolicyAndCounts(t *testing.T) {
	e := newEnv(t)
	f := &fakeFleet{}
	e.svc.cfg.Fleet = f
	e.world()
	ctx := context.Background()
	e.turnOn(100, 101)
	three := 3
	if _, err := e.svc.UpdatePolicy(ctx, adminP, 100, PolicyUpdate{Priority: &three}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.PublicOnPool(ctx); err != nil || n != 1 {
		t.Fatalf("public on pool: %d %v", n, err)
	}
	owner := auth.Principal{UserID: 1, Login: "owner1", Role: auth.RoleOwner, Source: "web"}
	n, err := e.svc.StopPublicOnPool(ctx, owner, "the owner turned public repositories off")
	if err != nil || n != 1 {
		t.Fatalf("stopped %d, %v", n, err)
	}
	pub, _ := e.store.GetRepository(ctx, 100)
	priv, _ := e.store.GetRepository(ctx, 101)
	if pub.PoolOn || !priv.PoolOn {
		t.Fatalf("public on %v, private on %v", pub.PoolOn, priv.PoolOn)
	}
	if pol, _ := e.store.GetPolicy(ctx, 100); pol.Priority != 3 {
		t.Errorf("the policy was not kept: %+v", pol)
	}
	if v := e.variable("CRSP_RUNS_ON_LINUX"); !strings.Contains(v, "crsp-home") { // private repository 101 untouched
		t.Errorf("private variable: %s", v)
	}
	e.fake.Lock(func() {
		if v := e.fake.Variables["alice/web"]["CRSP_RUNS_ON_LINUX"]; v != `["ubuntu-latest"]` {
			t.Errorf("public variable %s, want the hosted label", v)
		}
	})
	if len(f.removed) != 1 || f.removed[0] != 100 {
		t.Errorf("fleet %v", f.removed)
	}
	if !e.audit.has("pool.off|owner1|alice/web|the repository is public") || !e.audit.has("runners.removed|owner1|alice/web") {
		t.Errorf("audit: %v", e.audit.events)
	}
	// Again: nothing left to stop.
	if n, err := e.svc.StopPublicOnPool(ctx, owner, "again"); err != nil || n != 0 {
		t.Errorf("repeat: %d %v", n, err)
	}
}

// A private repository is left alone by both paths.
func TestPrivateRepositoryStaysServed(t *testing.T) {
	e, fleet := publicEnv(t)
	ctx := context.Background()
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.PoolOn || len(fleet.removed) != 0 {
		t.Fatalf("pool on %v, fleet %v", r.PoolOn, fleet.removed)
	}
}

// CRSP131: every sync stores the visibility GitHub reports, so the Visibility
// column and the refusals read a fresh value; a change on GitHub in either
// direction shows after the next sync and after the periodic reconcile.
func TestSyncStoresVisibilityEverySync(t *testing.T) {
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	vis := func(id int64) string {
		r, err := e.store.GetRepository(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Visibility
	}
	if vis(100) != "public" || vis(101) != "private" {
		t.Fatalf("first sync: 100 %q, 101 %q", vis(100), vis(101))
	}
	e.fake.Lock(func() { e.fake.Repos[100].Private = true; e.fake.Repos[101].Private = false })
	if err := e.svc.Sync(ctx, adminP); err != nil {
		t.Fatal(err)
	}
	if vis(100) != "private" || vis(101) != "public" {
		t.Fatalf("second sync: 100 %q, 101 %q", vis(100), vis(101))
	}
	e.fake.Lock(func() { e.fake.Repos[100].Private = false })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if vis(100) != "public" {
		t.Fatalf("reconcile: 100 %q", vis(100))
	}
}
