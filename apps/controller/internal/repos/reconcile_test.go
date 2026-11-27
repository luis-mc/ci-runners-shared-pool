package repos

import (
	"context"
	"testing"
)

func TestReconcileInstallationsTurnsOffLeavers(t *testing.T) { // R5.22
	e := newEnv(t)
	e.world()
	e.turnOn(100, 101)
	e.fake.Lock(func() { e.fake.Repos[100].Private = true }) // a public repository is stopped (CRSP82)
	ctx := context.Background()
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.PoolOn {
		t.Fatal("a repository still in the installation stays on")
	}
	// The owner removes repository 101 from the installation on GitHub.
	e.fake.Lock(func() { delete(e.fake.Repos, 101) })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); r.PoolOn {
		t.Fatal("a repository that left the installation is turned off")
	}
	if r, _ := e.store.GetRepository(ctx, 100); !r.PoolOn {
		t.Fatal("others stay on")
	}
	if !e.audit.has("pool.off|controller|alice/secret|the repository left") {
		t.Fatalf("the console says why: %v", e.audit.events)
	}
}

func TestReconcileInstallationRemoved(t *testing.T) { // R5.22
	e := newEnv(t)
	e.world()
	e.turnOn(100)
	ctx := context.Background()
	e.fake.Lock(func() { delete(e.fake.Installs, 10) })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if ins, _ := e.store.ListInstallations(ctx); len(ins) != 0 {
		t.Fatalf("%+v", ins)
	}
	if r, _ := e.store.GetRepository(ctx, 100); r.PoolOn {
		t.Fatal("an uninstalled App turns its repositories off")
	}
}

func TestReconcileAddsNewRepositoriesAndPermissions(t *testing.T) { // R5.22, R5.24
	e := newEnv(t)
	e.world()
	ctx := context.Background()
	e.fake.AddRepo(103, 10, "alice", "newest", true, 1)
	e.fake.Lock(func() { e.fake.Installs[10].Perms["actions_variables"] = "read" }) // owner weakened it
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetRepository(ctx, 103); err != nil {
		t.Fatalf("new repository known: %v", err)
	}
	ins, _ := e.svc.Installations(ctx, viewerP)
	if len(ins) != 1 || len(ins[0].MissingPermissions) != 1 || ins[0].MissingPermissions[0] != "actions_variables" {
		t.Fatalf("the console names the missing permission: %+v", ins)
	}
}

func TestReconcileKeepsStateWhenGitHubFails(t *testing.T) { // section 8
	e := newEnv(t)
	e.world()
	e.turnOn(100)
	e.fake.FailMatching("/installation/repositories", 503, 5)
	if err := e.svc.ReconcileInstallations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(context.Background(), 100); !r.PoolOn {
		t.Fatal("a failed listing must not turn repositories off")
	}
}

func TestLeftRepositoryCarriesTheReasonUntilItIsBack(t *testing.T) { // R5.22
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	repo := e.fake.Repos[101]
	e.fake.Lock(func() { delete(e.fake.Repos, 101) })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); !r.LeftInstallation() {
		t.Fatalf("the console can say the repository left: %+v", r)
	}
	e.fake.Lock(func() { e.fake.Repos[101] = repo })
	if err := e.svc.ReconcileInstallations(ctx); err != nil {
		t.Fatal(err)
	}
	r, _ := e.store.GetRepository(ctx, 101)
	if r.LeftInstallation() || r.PoolOn {
		t.Fatalf("back in the installation: still off, no longer said to have left: %+v", r)
	}
}

func TestRepositoryTurnedOffByAnOwnerHasNoLeftReason(t *testing.T) { // R5.22
	e := newEnv(t)
	e.world()
	e.turnOn(101)
	ctx := context.Background()
	if err := e.store.SetRepositoryPool(ctx, 101, false, "alice", e.svc.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if r, _ := e.store.GetRepository(ctx, 101); r.LeftInstallation() {
		t.Fatal("only the reconcile's turn-off is a leaver")
	}
}
