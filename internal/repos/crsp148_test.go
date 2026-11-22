package repos

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// memPersister keeps the saved state in memory, like the database row.
type memPersister struct {
	mu   sync.Mutex
	body []byte
}

func (p *memPersister) LoadReposState(context.Context) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.body...), nil
}

func (p *memPersister) SaveReposState(_ context.Context, b []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body = append([]byte(nil), b...)
	return nil
}

// restart is what an upgrade does: a new process with an empty memory, the
// same database, and no GitHub sign-in (the user token is memory only).
func (e *env) restart(p Persister) {
	e.t.Helper()
	if err := e.store.Flush(); err != nil {
		e.t.Fatal(err)
	}
	st, err := NewPersistentStore(context.Background(), p)
	if err != nil {
		e.t.Fatal(err)
	}
	e.store = st
	e.tokens.m = map[int64]github.UserToken{}
	e.svc = New(Config{Store: st, GitHub: e.gh, Tokens: e.tokens, Audit: e.audit, Capacity: e.cap, Now: e.clock,
		PublicRepositories: func(context.Context) (bool, error) { return e.publicOn(), nil }})
}

func persistentEnv(t *testing.T) (*env, *memPersister) {
	t.Helper()
	e := newEnv(t)
	p := &memPersister{}
	st, err := NewPersistentStore(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	e.store = st
	e.svc = New(Config{Store: st, GitHub: e.gh, Tokens: e.tokens, Audit: e.audit, Capacity: e.cap, Now: e.clock,
		PublicRepositories: func(context.Context) (bool, error) { return e.publicOn(), nil }})
	return e, p
}

// The upgrade restarts the controller: the pool switch, the policy and the
// variables of a repository must be as they were (CRSP148, R5.17).
func TestPoolOnSurvivesARestart(t *testing.T) {
	e, p := persistentEnv(t)
	e.world()
	e.turnOn(100, 101)
	ctx := context.Background()
	before, err := e.store.ListRepositories(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	pol, err := e.store.GetPolicy(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	e.restart(p)
	after, err := e.store.ListRepositories(ctx, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("repositories changed over a restart:\n%+v\n%+v", before, after)
	}
	if got, err := e.store.GetPolicy(ctx, 100); err != nil || !reflect.DeepEqual(got, pol) {
		t.Fatalf("policy %+v %v, want %+v", got, err, pol)
	}
	// Signed out (token gone): the sync reads through the App and succeeds,
	// and the listing still says the pool is on (OPS13).
	if err := e.svc.Sync(ctx, adminP); err != nil {
		t.Fatalf("sync without a token: %v", err)
	}
	rows, err := e.svc.List(ctx, adminP, ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	on := 0
	for _, r := range rows {
		if r.PoolOn {
			on++
		}
	}
	if on != 2 {
		t.Fatalf("%d repositories on after a restart, want 2", on)
	}
	// The reconcile that runs at start keeps them on.
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 100); !r.PoolOn {
		t.Fatal("the reconcile at start turned the pool off")
	}
}

// A store of the older version lived in memory: after the upgrade the
// repository is new to the store, but GitHub still holds its variables. The
// pool switch is restored from them (CRSP148).
func TestPoolOnAdoptedFromVariablesAfterUpgrade(t *testing.T) {
	e, _ := persistentEnv(t)
	e.world()
	e.fake.SetVariable("alice/web", "RUNS_ON_LIGHT", "crsp-light")
	e.fake.SetVariable("alice/secret", "RUNS_ON_LINUX", `["self-hosted","crsp-home","Linux"]`)
	// alice/other has no pool variable.
	ctx := context.Background()
	fresh := NewMemStore() // the empty store of the new process
	e.svc = New(Config{Store: fresh, GitHub: e.gh, Tokens: e.tokens, Audit: e.audit, Capacity: e.cap, Now: e.clock,
		PublicRepositories: func(context.Context) (bool, error) { return true, nil }})
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{100: true, 101: true, 102: false} {
		if r, err := fresh.GetRepository(ctx, id); err != nil || r.PoolOn != want {
			t.Fatalf("repository %d pool on = %v (%v), want %v", id, r.PoolOn, err, want)
		}
	}
	if !e.audit.has("pool.adopted") {
		t.Fatalf("the audit log says why: %v", e.audit.events)
	}
	// A second read does not adopt again or turn anything off.
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := fresh.GetRepository(ctx, 102); r.PoolOn {
		t.Fatal("a repository without pool variables stays off")
	}
}

// A suspended installation is not a removed one (CRSP148): its pool switch
// is kept.
func TestReconcileKeepsPoolOnOfSuspendedInstallation(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.Lock(func() { e.fake.Installs[10].Suspended = true })
	ctx := context.Background()
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.PoolOn {
		t.Fatal("a suspended installation turned the pool off")
	}
}

// A GitHub error on the read of an installation keeps what was known.
func TestReconcileKeepsPoolOnWhenGitHubErrors(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	e.fake.FailMatching("/installation/repositories", 422, 10)
	ctx := context.Background()
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.PoolOn {
		t.Fatal("a failed read turned the pool off")
	}
}

// An installation token is asked for without a permissions map, so an
// installation that lacks a permission still gets a token (CRSP148, R5.86).
func TestInstallationTokenAsksForNoPermissions(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.fake.Lock(func() { delete(e.fake.Installs[10].Perms, "secrets") }) // the App asks for secrets, the owner has not accepted
	ctx := context.Background()
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatalf("reconcile on an installation without secrets: %v", err)
	}
	bodies := e.fake.TokenRequestBodies()
	if len(bodies) == 0 {
		t.Fatal("no token was asked for")
	}
	for _, b := range bodies {
		if strings.Contains(b, "permissions") {
			t.Fatalf("a token request carries permissions: %q", b)
		}
	}
	// The missing permission is a step of the grant, not an error.
	g, err := e.svc.Grant(ctx, adminP, GrantInput{Needs: []string{"secrets"}, InstallationID: 10})
	if err != nil {
		t.Fatal(err)
	}
	if g.Ready || len(g.Missing) != 1 || g.Missing[0] != "secrets" {
		t.Fatalf("grant %+v", g)
	}
}

// The fake refuses a token for permissions the installation lacks, as GitHub
// does; the error keeps its status and message.
func TestGitHubErrorKeepsStatusAndMessage(t *testing.T) {
	e := newEnv(t)
	e.world()
	e.fake.FailMatching("/app/installations", 422, 1)
	err := e.svc.Sync(context.Background(), adminP)
	var api *github.APIError
	if !errors.As(err, &api) || api.Status != 422 {
		t.Fatalf("sync error %v, want an APIError with status 422", err)
	}
}

// The saved bytes depend on the state only, not on the order a map gave its entries (CRSP222: a reinstall must
// not change the repos_state row).
func TestEncodeIsDeterministic(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	for i := int64(1); i <= 20; i++ {
		_ = m.UpsertInstallation(ctx, Installation{ID: i})
		_ = m.UpsertRepository(ctx, Repository{ID: 100 + i, InstallationID: i})
		_ = m.UpsertManagedVariable(ctx, ManagedVariable{RepositoryID: 100 + i, Name: "V"})
	}
	m.mu.Lock()
	first, err := m.encode()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 20; n++ {
		m.mu.Lock()
		again, err := m.encode()
		m.mu.Unlock()
		if err != nil || string(again) != string(first) {
			t.Fatalf("encode changed between two saves of the same state: %v", err)
		}
	}
}
