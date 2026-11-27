package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

func openMachines(t *testing.T, dir string) (*Store, *machines.DurableRepo, *machines.Service) {
	t.Helper()
	ctx := context.Background()
	db, err := OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo, err := machines.OpenDurableRepo(ctx, db.MachinePersister())
	if err != nil {
		t.Fatal(err)
	}
	return db, repo, machines.New(machines.Options{Repo: repo, Fingerprint: "fp"})
}

func facts(host string) machines.Facts {
	return machines.Facts{HostName: host, OS: "windows", Arch: "x64", Threads: 8, MemoryBytes: 16 << 30}
}

func joinLocal(t *testing.T, svc *machines.Service, host string) machines.JoinResult {
	t.Helper()
	ctx := context.Background()
	cred, err := svc.LocalCredential(ctx)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(ctx, machines.JoinInput{Code: cred, Local: true, Facts: facts(host)})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// OPS59: a restart of the controller keeps machines, settings and keys.
func TestMachinesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, repo, svc := openMachines(t, dir)
	res := joinLocal(t, svc, "PC")
	set := machines.DefaultSettings()
	set.AllowedBackends = []string{"persistent", "docker", "wsl2"}
	set.PoolCPUs = 6
	if err := repo.PutSettings(ctx, res.MachineID, set); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, repo2, svc2 := openMachines(t, dir)
	m, err := svc2.Authenticate(ctx, res.Key, res.MachineID)
	if err != nil || m.ID != res.MachineID || !m.IsController {
		t.Fatalf("machine after restart = %+v, %v", m, err)
	}
	got, err := repo2.GetSettings(ctx, res.MachineID)
	if err != nil || got.PoolCPUs != 6 || len(got.AllowedBackends) != 3 {
		t.Fatalf("settings after restart = %+v, %v", got, err)
	}
}

// OPS59: the controller's own agent joining again keeps the machine, its settings
// and its runners; the old key stops working.
func TestLocalRejoinKeepsMachine(t *testing.T) {
	ctx := context.Background()
	_, repo, svc := openMachines(t, t.TempDir())
	first := joinLocal(t, svc, "PC")
	set := machines.DefaultSettings()
	set.PoolMemoryGiB = 12
	if err := repo.PutSettings(ctx, first.MachineID, set); err != nil {
		t.Fatal(err)
	}
	second := joinLocal(t, svc, "PC")
	if second.MachineID != first.MachineID {
		t.Fatalf("rejoin made machine %s, want %s", second.MachineID, first.MachineID)
	}
	if second.Settings.PoolMemoryGiB != 12 {
		t.Fatalf("settings = %+v", second.Settings)
	}
	if _, err := svc.Authenticate(ctx, first.Key, ""); err == nil {
		t.Fatal("old key still works after a rejoin")
	}
	if _, err := svc.Authenticate(ctx, second.Key, first.MachineID); err != nil {
		t.Fatal(err)
	}
	ms, _ := repo.ListMachines(ctx)
	if len(ms) != 1 {
		t.Fatalf("%d machines, want 1", len(ms))
	}
	var rejoined bool
	for _, a := range repo.AuditLog() {
		rejoined = rejoined || a.Action == "machine.rejoin"
	}
	if !rejoined {
		t.Fatal("no machine.rejoin audit entry")
	}
}

// OPS59: another agent of the same host, OS and architecture that is offline
// joins as the same machine; a different host does not.
func TestAgentRejoinMatchesHostOSArch(t *testing.T) {
	ctx := context.Background()
	_, repo, svc := openMachines(t, t.TempDir())
	join := func(f machines.Facts) machines.JoinResult {
		c, err := svc.CreateJoinCode(ctx, "owner", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.Join(ctx, machines.JoinInput{Code: c.Code, Facts: f})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := join(facts("box"))
	if b := join(facts("BOX")); b.MachineID != a.MachineID {
		t.Fatalf("same host got %s, want %s", b.MachineID, a.MachineID)
	}
	lin := facts("box")
	lin.OS = "linux"
	if c := join(lin); c.MachineID == a.MachineID {
		t.Fatal("another OS reused the machine")
	}
	if d := join(facts("other")); d.MachineID == a.MachineID {
		t.Fatal("another host reused the machine")
	}
	if ms, _ := repo.ListMachines(ctx); len(ms) != 3 {
		t.Fatalf("%d machines, want 3", len(ms))
	}
}

// OPS59: an existing machines.json is imported once into the database.
func TestImportMachinesJSON(t *testing.T) {
	ctx := context.Background()
	src := machines.NewMemRepo()
	m := machines.Machine{ID: "m_old", Name: "PC", OS: "windows", Arch: "x64", IsController: true, State: machines.StateActive, JoinedAt: time.Now()}
	set := machines.DefaultSettings()
	set.AllowedBackends = []string{"persistent", "docker"}
	if err := src.InsertMachine(ctx, m, set, "hash1"); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(src.Snapshot())
	file := filepath.Join(t.TempDir(), "machines.json")
	if err := os.WriteFile(file, b, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, ok, err := machines.ReadSnapshotFile(file)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	dir := t.TempDir()
	db, repo, _ := openMachines(t, dir)
	if imp, err := repo.Import(ctx, snap); err != nil || !imp {
		t.Fatal(imp, err)
	}
	if imp, _ := repo.Import(ctx, snap); imp {
		t.Fatal("second import ran")
	}
	_ = db.Close()
	_, repo2, _ := openMachines(t, dir)
	got, err := repo2.MachineByKey(ctx, "hash1")
	if err != nil || got.ID != "m_old" {
		t.Fatalf("%+v %v", got, err)
	}
	if s, _ := repo2.GetSettings(ctx, "m_old"); len(s.AllowedBackends) != 2 {
		t.Fatalf("settings = %+v", s)
	}
}

// OPS59: runner records and plans survive a restart, also when their machine is unknown.
func TestRunnersSurviveRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := runners.OpenDurableStore(ctx, db.RunnerPersister())
	if err != nil {
		t.Fatal(err)
	}
	rec := runners.Record{ID: "r1", Name: "pool-1", MachineID: "m_unknown", Kind: "persistent", State: runners.Idle, Managed: true, CreatedAt: time.Now()}
	if err := rs.InsertRunner(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err := rs.SetRunnerService(ctx, "r1", "svc", "acct"); err != nil {
		t.Fatal(err)
	}
	if err := rs.SavePlan(ctx, runners.Plan{ID: "p1", Area: "add"}); err != nil {
		t.Fatal(err)
	}
	_ = rs.InsertRunner(ctx, runners.Record{ID: "r2", Name: "gone", MachineID: "m1"})
	if err := rs.DeleteRunner(ctx, "r2"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db2, err := OpenForTests(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	rs2, err := runners.OpenDurableStore(ctx, db2.RunnerPersister())
	if err != nil {
		t.Fatal(err)
	}
	all, _ := rs2.ListRunners(ctx, runners.Filter{})
	if len(all) != 1 || all[0].ServiceName != "svc" || all[0].MachineID != "m_unknown" {
		t.Fatalf("runners = %+v", all)
	}
	if p, err := rs2.GetPlan(ctx, "p1"); err != nil || p.Area != "add" {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

// OPS59: default settings after chosen ones of the same computer, and a rejoin, are signs of a reset.
func TestResetSigns(t *testing.T) {
	ctx := context.Background()
	_, repo, svc := openMachines(t, t.TempDir())
	old := machines.Machine{ID: "m_old", Name: "PC", OS: "windows", Arch: "x64", Facts: facts("PC"), State: machines.StateActive, JoinedAt: time.Now().Add(-time.Hour)}
	chosen := machines.DefaultSettings()
	chosen.PoolCPUs = 4
	if err := repo.InsertMachine(ctx, old, chosen, "h1"); err != nil {
		t.Fatal(err)
	}
	// The old machine is online, so the new join is a second machine with the defaults.
	old.Online = true
	_ = repo.UpdateMachine(ctx, old)
	join := func() machines.JoinResult {
		c, err := svc.CreateJoinCode(ctx, "owner", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		r, err := svc.Join(ctx, machines.JoinInput{Code: c.Code, Facts: facts("PC")})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	res := join()
	signs, err := svc.ResetSigns(ctx)
	if err != nil || len(signs[res.MachineID]) != 1 {
		t.Fatalf("signs = %v, %v", signs, err)
	}
	join()
	if signs, _ = svc.ResetSigns(ctx); len(signs[res.MachineID]) != 2 {
		t.Fatalf("after a rejoin: %v", signs)
	}
}
