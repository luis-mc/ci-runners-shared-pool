package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

type fakePauser struct {
	paused, resumed []string
}

func (f *fakePauser) PauseService(_ context.Context, _, svc string) error {
	f.paused = append(f.paused, svc)
	return nil
}
func (f *fakePauser) ResumeService(_ context.Context, _, svc string) error {
	f.resumed = append(f.resumed, svc)
	return nil
}

func repo(id int64, name string) PoolRepo {
	return PoolRepo{ID: id, FullName: "luis-mc/" + name, Scope: Scope{Kind: "repository", RepositoryID: id}}
}

// poolRig is a rig with one pool runner and a settable list of pool repos.
func poolRig(t *testing.T, repos *[]PoolRepo) (*rig, Record) {
	t.Helper()
	r := newRig(machine("linux", "x64", true))
	r.svc.Background = func(f func()) { f() }
	r.svc.PoolRepos = func(context.Context) ([]PoolRepo, error) { return *repos, nil }
	ctx := context.Background()
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Apply(ctx, p.ID, nil, "luis-mc"); err != nil {
		t.Fatal(err)
	}
	for _, rec := range r.list(t) {
		if IsPoolRunner(rec) {
			return r, rec
		}
	}
	t.Fatal("no pool runner")
	return nil, Record{}
}

func (r *rig) list(t *testing.T) []Record {
	t.Helper()
	all, err := r.store.ListRunners(context.Background(), Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func (r *rig) kids(t *testing.T) []Record {
	var out []Record
	for _, rec := range r.list(t) {
		if IsRegistration(rec) {
			out = append(out, rec)
		}
	}
	return out
}

func TestPoolRunnerHasNoRepositoryAndNoRegistration(t *testing.T) {
	repos := []PoolRepo{}
	r, p := poolRig(t, &repos)
	if !strings.HasSuffix(p.Name, "-pool-1") || p.Scope.Kind != ScopePool || p.ServiceName != "" {
		t.Fatalf("pool runner = %+v", p)
	}
	if r.gh.regCalls != 0 || len(r.ag.added) != 0 {
		t.Error("a pool runner with no pool repository registers nothing")
	}
}

func TestPoolOnAddsOneRegistrationPerRepository(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a")}
	r, p := poolRig(t, &repos)
	if len(r.kids(t)) != 1 {
		t.Fatalf("registrations after the pool runner was added = %d, want 1 (repository a was on)", len(r.kids(t)))
	}
	repos = append(repos, repo(2, "b"))
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	kids := r.kids(t)
	if len(kids) != 2 {
		t.Fatalf("registrations = %d, want 2", len(kids))
	}
	for _, k := range kids {
		if k.PoolRunnerID != p.ID || k.Kind != "persistent" || !k.Managed {
			t.Errorf("registration = %+v", k)
		}
	}
	// Sync again: nothing is added twice.
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if len(r.kids(t)) != 2 || len(r.ag.added) != 2 {
		t.Errorf("a second sync added more: %d records, %d services", len(r.kids(t)), len(r.ag.added))
	}
}

func TestPoolOffDrainsAndRemovesTheRegistration(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, _ := poolRig(t, &repos)
	repos = repos[:1] // repository b stops using the pool
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	kids := r.kids(t)
	if len(kids) != 1 || kids[0].Scope.RepositoryID != 1 {
		t.Fatalf("registrations after pool off = %+v", kids)
	}
	if len(r.ag.removed) != 1 || r.ag.removed[0].RemoveToken != "REMOVE-TOKEN-1" {
		t.Errorf("removed = %+v, want one removal with a remove token", r.ag.removed)
	}
}

func TestPoolOffNeverStopsAJob(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a")}
	r, _ := poolRig(t, &repos)
	kid := r.kids(t)[0]
	r.gh.runners = []github.Runner{{ID: 5, Name: kid.Name, Status: "online", Busy: true}}
	repos = nil
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if len(r.ag.removed) != 0 || len(r.ag.stopped) != 0 {
		t.Errorf("a busy registration was touched: removed %v stopped %v", r.ag.removed, r.ag.stopped)
	}
	if len(r.kids(t)) != 1 {
		t.Error("the registration must stay until its job ends")
	}
}

func TestSlotLockPausesTheOtherRegistrationsWhileOneRuns(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, _ := poolRig(t, &repos)
	pz := &fakePauser{}
	r.svc.Pauser = pz
	kids := r.kids(t)
	busy, other := kids[0], kids[1]
	r.gh.runners = []github.Runner{
		{ID: 5, Name: busy.Name, Status: "online", Busy: true},
		{ID: 6, Name: other.Name, Status: "online"},
	}
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if len(pz.paused) != 1 || pz.paused[0] != other.ServiceName {
		t.Fatalf("paused = %v, want only %s", pz.paused, other.ServiceName)
	}
	// The job ends: the paused registration runs again.
	r.gh.runners = []github.Runner{{ID: 5, Name: busy.Name, Status: "online"}, {ID: 6, Name: other.Name, Status: "online"}}
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if len(pz.resumed) != 1 || pz.resumed[0] != other.ServiceName {
		t.Errorf("resumed = %v", pz.resumed)
	}
}

func TestSlotLockWithoutPauserReportsAConflict(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, _ := poolRig(t, &repos)
	kids := r.kids(t)
	r.gh.runners = []github.Runner{
		{ID: 5, Name: kids[0].Name, Status: "online", Busy: true},
		{ID: 6, Name: kids[1].Name, Status: "online", Busy: true},
	}
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	if !r.au.has("pool.slot_conflict") {
		t.Error("two busy registrations of one runner must be reported")
	}
}

func TestSlotLockFailsClosedWhenGitHubCannotBeRead(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, _ := poolRig(t, &repos)
	pz := &fakePauser{}
	r.svc.Pauser = pz
	r.gh.listErr = errors.New("rate limited")
	_ = r.svc.SyncPoolRegistrations(ctx, "u")
	if len(pz.paused)+len(pz.resumed) != 0 {
		t.Error("an unreadable GitHub list must change no registration")
	}
}

func TestMigrationKeepsTheRepositoryRegistration(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a")}
	r := newRig(machine("linux", "x64", true))
	r.svc.Background = func(f func()) { f() }
	r.svc.PoolRepos = func(context.Context) ([]PoolRepo, error) { return repos, nil }
	old := r.add(t) // a repository-scoped persistent runner of repository 1
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	var pool, reg Record
	for _, rec := range r.list(t) {
		switch {
		case IsPoolRunner(rec):
			pool = rec
		case rec.ID == old.ID:
			reg = rec
		}
	}
	if pool.ID == "" || reg.PoolRunnerID != pool.ID {
		t.Fatalf("pool = %+v, old = %+v", pool, reg)
	}
	if reg.Name != old.Name || reg.ServiceName != old.ServiceName || reg.Scope != old.Scope {
		t.Error("the migrated runner must keep its name, service and registration")
	}
	if len(r.ag.added) != 1 || len(r.ag.removed) != 0 {
		t.Errorf("migration registered again or removed: added %d removed %d", len(r.ag.added), len(r.ag.removed))
	}
	// Idempotent.
	if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
		t.Fatal(err)
	}
	pools := 0
	for _, rec := range r.list(t) {
		if IsPoolRunner(rec) {
			pools++
		}
	}
	if pools != 1 {
		t.Errorf("pool runners = %d after a second sync, want 1", pools)
	}
}

func TestListShowsPoolRunnerWithServingAndRegistrationCount(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, p := poolRig(t, &repos)
	kids := r.kids(t)
	var busyKid Record
	for _, k := range kids {
		if k.Scope.RepositoryID == 2 {
			busyKid = k
		}
	}
	r.gh.runners = []github.Runner{{ID: 6, Name: busyKid.Name, Status: "online", Busy: true}}
	views, err := r.svc.ListView(ctx, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("the list shows %d runners, want the one pool runner", len(views))
	}
	v := views[0]
	if v.ID != p.ID || v.Registrations != 2 || v.Serving != "luis-mc/b" || v.State != Busy {
		t.Errorf("view = state %s serving %q registrations %d", v.State, v.Serving, v.Registrations)
	}
}

func TestRemovingAPoolRunnerRemovesItsRegistrations(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a"), repo(2, "b")}
	r, p := poolRig(t, &repos)
	_, _, err := r.svc.BeginRemove(ctx, p.ID, false, "u")
	var nc *NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) != 3 {
		t.Fatalf("err = %v, want a yes naming the runner and two registrations", err)
	}
	_, finish, err := r.svc.BeginRemove(ctx, p.ID, true, "u")
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(ctx); err != nil {
		t.Fatal(err)
	}
	if left := r.list(t); len(left) != 0 {
		t.Errorf("records left: %+v", left)
	}
	if len(r.ag.removed) != 2 {
		t.Errorf("removals = %d, want 2", len(r.ag.removed))
	}
}

func TestPoolScopeRefusesEphemeralAndSkipsTheGate(t *testing.T) {
	ctx := context.Background()
	r := newRig(machine("linux", "x64", true))
	r.svc.Gate = func(context.Context, Scope) error { return errors.New("gate must not run for a pool runner") }
	_, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "ephemeral", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "u")
	var re *RefusalError
	if !errors.As(err, &re) || strings.Contains(re.Reason, "gate must not run") {
		t.Fatalf("err = %v", err)
	}
	if _, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "u"); err != nil {
		t.Errorf("a pool runner plan was refused: %v", err)
	}
}

func (f *fakeAudit) has(action string) bool {
	for _, e := range f.entries {
		if strings.Contains(e, "|"+action+"|") {
			return true
		}
	}
	return false
}

// OPS60, runners R5.101: a registration whose build failed stays, with its
// reason, in the list as Failed. A later sync neither removes nor retries it.
func TestFailedPoolRegistrationStaysVisibleAndIsNotRetried(t *testing.T) {
	ctx := context.Background()
	repos := []PoolRepo{repo(1, "a")}
	r := newRig(machine("linux", "x64", true))
	r.svc.Background = func(f func()) { f() }
	r.svc.PoolRepos = func(context.Context) ([]PoolRepo, error) { return repos, nil }
	r.ag.addErr = errors.New("build failed (exit 1): no Linux crsp")
	p, err := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: ScopePool}}, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	done, err := r.svc.Apply(ctx, p.ID, nil, "luis-mc")
	if err != nil {
		t.Fatal(err)
	}
	// The plan never says done for a runner that is failed.
	if done.State == "done" || len(done.Steps) == 0 || done.Steps[0].State != "failed" || !strings.Contains(done.Steps[0].Error, "build failed") {
		t.Fatalf("plan = %+v", done)
	}
	tries := len(r.ag.added)
	for range 3 {
		if err := r.svc.SyncPoolRegistrations(ctx, "u"); err != nil {
			t.Fatal(err)
		}
	}
	kids := r.kids(t)
	if len(kids) != 1 || kids[0].State != Failed || !strings.Contains(kids[0].Reason, "build failed") {
		t.Fatalf("registrations = %+v", kids)
	}
	if len(r.ag.added) != tries {
		t.Errorf("a sync retried the failed build: %d tries, want %d", len(r.ag.added), tries)
	}
	views, err := r.svc.ListView(ctx, Filter{})
	if err != nil || len(views) != 1 || views[0].State != Failed || !strings.Contains(views[0].Reason, "build failed") {
		t.Fatalf("the list shows %+v %v", views, err)
	}
}

// OPS89: an ephemeral pool runner holds no registration by design, so its view
// never says that no repository uses the pool.
func TestEphemeralPoolRunnerViewDoesNotBlameTheRepositories(t *testing.T) {
	v := poolView(Record{Kind: "ephemeral", Scope: Scope{Kind: ScopePool}}, nil, nil, "m")
	if v.State != Idle || v.Reason != "a container starts for each job of the pool" {
		t.Fatalf("view = %+v", v)
	}
	p := poolView(Record{Kind: "persistent", Scope: Scope{Kind: ScopePool}}, nil, nil, "m")
	if p.Reason != "no repository uses the pool yet" {
		t.Fatalf("persistent view = %+v", p)
	}
}
