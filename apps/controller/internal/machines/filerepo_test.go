package machines_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// CRSP205: a controller restart must not forget a machine's key. Before, the
// pool lived in memory, so the controller's own agent was told "this machine was
// removed from the pool" at each start and every machine list was empty until it
// had joined again.
func TestFileRepoKeepsMachinesAndKeysAcrossARestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), machines.MachinesFile)
	r, err := machines.OpenFileRepo(path)
	if err != nil {
		t.Fatal(err)
	}
	m := machines.Machine{ID: "m_1", Name: "box", OS: "windows", Arch: "x64", IsController: true, Online: true, JoinedAt: time.Now().UTC()}
	if err := r.InsertMachine(ctx, m, machines.Settings{JobSlots: 3}, "hash-live"); err != nil {
		t.Fatal(err)
	}
	gone := machines.Machine{ID: "m_2", Name: "old", OS: "linux", Arch: "x64"}
	if err := r.InsertMachine(ctx, gone, machines.Settings{}, "hash-gone"); err != nil {
		t.Fatal(err)
	}
	if err := r.RevokeKeys(ctx, "m_2"); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkRemoved(ctx, "m_2", time.Now()); err != nil {
		t.Fatal(err)
	}

	// The controller restarts: a new repository reads the same file.
	r2, err := machines.OpenFileRepo(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r2.MachineByKey(ctx, "hash-live")
	if err != nil || got.ID != "m_1" {
		t.Fatalf("the stored key after a restart: %+v, %v (want m_1, no error)", got, err)
	}
	if got.Online {
		t.Fatal("a machine is offline until its session opens again")
	}
	if s, _ := r2.GetSettings(ctx, "m_1"); s.JobSlots != 3 {
		t.Fatalf("settings after a restart: %+v", s)
	}
	list, _ := r2.ListMachines(ctx)
	if len(list) != 1 || list[0].ID != "m_1" {
		t.Fatalf("machines after a restart: %+v", list)
	}
	if _, err := r2.MachineByKey(ctx, "hash-gone"); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("a removed machine's key after a restart: %v", err)
	}
	if _, err := r2.MachineByKey(ctx, "hash-unknown"); !errors.Is(err, machines.ErrUnauthenticated) {
		t.Fatalf("an unknown key: %v", err)
	}
}

func TestFileRepoRefusesADamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), machines.MachinesFile)
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := machines.OpenFileRepo(path); err == nil {
		t.Fatal("a damaged pool file must be an error, never an empty pool")
	}
}

func writeFile(path, s string) error { return os.WriteFile(path, []byte(s), 0o600) }
