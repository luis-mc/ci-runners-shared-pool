package machines_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeSource struct {
	scan      leftovers.Scan
	scanErr   error
	results   []leftovers.Result
	removeErr error
	ownedSeen [][]leftovers.Owned
	removed   [][]leftovers.Ref
}

func (f *fakeSource) Scan(_ context.Context, _ string, owned []leftovers.Owned) (leftovers.Scan, error) {
	f.ownedSeen = append(f.ownedSeen, owned)
	return f.scan, f.scanErr
}

func (f *fakeSource) Remove(_ context.Context, _ string, owned []leftovers.Owned, refs []leftovers.Ref) ([]leftovers.Result, error) {
	f.ownedSeen = append(f.ownedSeen, owned)
	f.removed = append(f.removed, refs)
	return f.results, f.removeErr
}

type loRig struct {
	svc  *machines.Service
	repo *machines.MemRepo
	id   string
	src  *fakeSource
	note *notes
}

func newLoRig(t *testing.T, controller bool) *loRig {
	t.Helper()
	clk := &clock{t: time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC)}
	repo := machines.NewMemRepo()
	repo.Now = clk.Now
	n := &notes{}
	svc := machines.New(machines.Options{Repo: repo, Now: clk.Now, ControllerAddress: "pool.lan", AgentPort: 8461, Notify: n})
	code, err := svc.CreateJoinCode(context.Background(), "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.Join(context.Background(), machines.JoinInput{Code: code.Code, Facts: machines.Facts{HostName: "win-1", OS: "windows", Arch: "x64", Threads: 8}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := repo.GetMachine(context.Background(), res.MachineID)
	m.Online, m.LastSeen, m.IsController = true, clk.Now(), controller
	_ = repo.UpdateMachine(context.Background(), m)
	src := &fakeSource{scan: leftovers.Scan{Admin: true, Items: []leftovers.Item{
		{Kind: leftovers.KindService, Name: "actions.runner.old"},
		{Kind: leftovers.KindFolder, Name: `C:\crsp\runners\old`, SizeBytes: 1 << 20},
	}}}
	svc.SetOwnedRunners(func(context.Context, string) ([]leftovers.Owned, error) {
		return []leftovers.Owned{{Name: "box-1", ServiceName: "actions.runner.box-1"}}, nil
	})
	if controller {
		svc.SetLeftoverSources(src, nil)
	} else {
		svc.SetLeftoverSources(nil, src)
	}
	return &loRig{svc: svc, repo: repo, id: res.MachineID, src: src, note: n}
}

func TestLeftoversScanBuildsOwnedFromRegistry(t *testing.T) {
	for _, controller := range []bool{false, true} {
		r := newLoRig(t, controller)
		sc, err := r.svc.RunnerLeftovers(context.Background(), r.id)
		if err != nil || len(sc.Items) != 2 || !sc.Admin {
			t.Fatalf("controller %v: scan %+v err %v", controller, sc, err)
		}
		if len(r.src.ownedSeen) != 1 || len(r.src.ownedSeen[0]) != 1 || r.src.ownedSeen[0][0].ServiceName != "actions.runner.box-1" {
			t.Fatalf("owned = %+v", r.src.ownedSeen)
		}
	}
}

func TestLeftoversWithoutSourceOrRegistry(t *testing.T) {
	r := newLoRig(t, true)
	r.svc.SetLeftoverSources(nil, r.src)
	if _, err := r.svc.RunnerLeftovers(context.Background(), r.id); !errors.Is(err, machines.ErrNotSupported) {
		t.Fatalf("no local source: %v", err)
	}
	r.svc.SetLeftoverSources(r.src, r.src)
	r.svc.SetOwnedRunners(nil)
	if _, err := r.svc.RunnerLeftovers(context.Background(), r.id); !errors.Is(err, machines.ErrNotSupported) {
		t.Fatalf("no registry: %v", err)
	}
	if len(r.src.ownedSeen) != 0 {
		t.Fatal("scanned without knowing the owned runners")
	}
}

func TestLeftoversOfflineAndUnknown(t *testing.T) {
	ctx := context.Background()
	r := newLoRig(t, false)
	if _, err := r.svc.RunnerLeftovers(ctx, "nope"); err == nil {
		t.Fatal("unknown machine scanned")
	}
	m, _ := r.repo.GetMachine(ctx, r.id)
	m.Online = false
	_ = r.repo.UpdateMachine(ctx, m)
	if _, err := r.svc.RunnerLeftovers(ctx, r.id); !errors.Is(err, machines.ErrOffline) {
		t.Fatalf("scan offline: %v", err)
	}
	refs := []leftovers.Ref{{Kind: leftovers.KindService, Name: "actions.runner.old"}}
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, refs); !errors.Is(err, machines.ErrOffline) {
		t.Fatalf("remove offline: %v", err)
	}
	if len(r.src.removed) != 0 {
		t.Fatal("removal sent to an offline machine")
	}
}

func TestLeftoversRefuseEmptyTooManyAndNotScanned(t *testing.T) {
	ctx := context.Background()
	r := newLoRig(t, false)
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, nil); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	many := make([]leftovers.Ref, 201)
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, many); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("too many: %v", err)
	}
	ref := leftovers.Ref{Kind: leftovers.KindService, Name: "actions.runner.old"}
	// No scan yet: nothing is known.
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{ref}); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("no scan: %v", err)
	}
	_, _ = r.svc.RunnerLeftovers(ctx, r.id)
	other := leftovers.Ref{Kind: leftovers.KindFolder, Name: `C:\Windows`}
	_, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{ref, other})
	if !errors.Is(err, machines.ErrConflict) || !strings.Contains(err.Error(), "scan again, this item was not in the last scan") || !strings.Contains(err.Error(), `C:\Windows`) {
		t.Fatalf("not in scan: %v", err)
	}
	if len(r.src.removed) != 0 {
		t.Fatal("something was sent although one item was not in the scan")
	}
}

func TestLeftoversRemoveAuditsAndDropsRemoved(t *testing.T) {
	ctx := context.Background()
	r := newLoRig(t, false)
	_, _ = r.svc.RunnerLeftovers(ctx, r.id)
	svcRef := leftovers.Ref{Kind: leftovers.KindService, Name: "actions.runner.old"}
	dirRef := leftovers.Ref{Kind: leftovers.KindFolder, Name: `C:\crsp\runners\old`}
	r.src.results = []leftovers.Result{
		{Kind: leftovers.KindService, Name: svcRef.Name, Outcome: leftovers.Removed},
		{Kind: leftovers.KindFolder, Name: dirRef.Name, Outcome: leftovers.Failed, Error: "in use"},
	}
	before := len(r.note.state)
	res, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{svcRef, dirRef})
	if err != nil || len(res) != 2 || res[1].Outcome != leftovers.Failed {
		t.Fatalf("res %+v err %v", res, err)
	}
	if len(r.src.ownedSeen) != 2 || len(r.src.ownedSeen[1]) != 1 {
		t.Fatalf("owned not passed fresh: %+v", r.src.ownedSeen)
	}
	var calls, items []string
	for _, a := range r.repo.AuditLog() {
		switch a.Action {
		case "machine.runner_leftovers.remove":
			calls = append(calls, a.Actor+" "+a.Subject)
		case "machine.runner_leftovers.item":
			items = append(items, a.Detail)
		}
	}
	if len(calls) != 1 || calls[0] != "alice "+r.id || len(items) != 2 ||
		!strings.Contains(items[0], "kind service, name actions.runner.old, outcome removed") || !strings.Contains(items[1], "outcome failed, in use") {
		t.Fatalf("audit calls %v items %v", calls, items)
	}
	if len(r.note.state) <= before {
		t.Fatal("the machine change was not announced")
	}
	// The removed service is out of the latest scan; the failed folder stays.
	r.src.results = nil
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{svcRef}); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("removed item still in scan: %v", err)
	}
	if _, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{dirRef}); err != nil {
		t.Fatalf("failed item dropped from scan: %v", err)
	}
}

func TestLeftoversRemoveErrorPassesThrough(t *testing.T) {
	ctx := context.Background()
	r := newLoRig(t, false)
	_, _ = r.svc.RunnerLeftovers(ctx, r.id)
	r.src.removeErr = errors.New("the agent did not answer in time")
	_, err := r.svc.RemoveRunnerLeftovers(ctx, "alice", r.id, []leftovers.Ref{{Kind: leftovers.KindService, Name: "actions.runner.old"}})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v", err)
	}
}
