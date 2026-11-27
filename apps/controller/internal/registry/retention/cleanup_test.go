package retention

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// R5.48: read-only, dry run, mark and sweep, back to read-write, bytes freed recorded.
func TestCleanupWindow(t *testing.T) {
	e := newEnv(t)
	e.reg.gcFreed = 4_200
	e.repo.layers = []registry.Layer{{Digest: "sha256:dead", SizeBytes: 4_200}}
	freed, err := e.svc.Cleanup(context.Background(), TriggerNow)
	must(t, err)
	if freed != 4_200 {
		t.Fatalf("freed %d", freed)
	}
	if len(e.reg.gcDry) != 2 || !e.reg.gcDry[0] || e.reg.gcDry[1] {
		t.Fatalf("want a dry run, then the real one: %v", e.reg.gcDry)
	}
	for i, ro := range e.reg.roAtGC {
		if !ro {
			t.Fatalf("garbage collection %d ran outside the read-only window (pulls keep working, pushes wait)", i)
		}
	}
	if e.reg.readOnly {
		t.Fatal("registry left read-only")
	}
	if len(e.repo.readOnly) != 2 || !e.repo.readOnly[0] || e.repo.readOnly[1] {
		t.Fatalf("persisted flag: %v", e.repo.readOnly)
	}
	c := e.repo.cleanups[0]
	if c.State != "done" || c.FreedBytes != 4_200 || c.Trigger != TriggerNow {
		t.Fatalf("record: %+v", c)
	}
	if len(e.repo.deletedL) != 1 || e.repo.deletedL[0] != "sha256:dead" {
		t.Fatalf("the swept layer row stays: %v", e.repo.deletedL)
	}
	if !e.aud.has(ActionReadOnly) || !e.aud.has(ActionCleanup) {
		t.Fatalf("audit (R5.51): %+v", e.aud.entries)
	}
}

// R5.48: a failing sweep still ends read-write and records the failure; the dry run guards the sweep.
func TestCleanupFailureReturnsToReadWrite(t *testing.T) {
	e := newEnv(t)
	e.reg.gcErr = errors.New("disk error")
	_, err := e.svc.Cleanup(context.Background(), TriggerWeekly)
	if err == nil {
		t.Fatal("failure hidden")
	}
	if e.reg.readOnly || e.repo.readOnly[len(e.repo.readOnly)-1] {
		t.Fatal("left read-only after a failure")
	}
	if c := e.repo.cleanups[0]; c.State != "failed" || c.Error == "" {
		t.Fatalf("%+v", c)
	}

	e2 := newEnv(t)
	e2.reg.dryErr = errors.New("mark failed")
	if _, err := e2.svc.Cleanup(context.Background(), TriggerNow); err == nil {
		t.Fatal("dry run failure hidden")
	}
	if len(e2.reg.gcDry) != 1 {
		t.Fatalf("the sweep ran after a failed dry run: %v", e2.reg.gcDry)
	}
	if e2.reg.readOnly {
		t.Fatal("left read-only")
	}
}

// R5.52: staged and refused images older than 1 day are removed; younger ones stay.
func TestCleanupRemovesOldStagedAndRefused(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "s_old", State: registry.StateStaged, PushedAt: t0.Add(-25 * time.Hour), Manifest: "sha256:s1"})
	e.repo.staged["s_old"] = "crsp/incoming/m1/a"
	e.repo.add(registry.Image{ID: "r_old", State: registry.StateRefused, PushedAt: day(-3), Manifest: "sha256:r1"})
	e.repo.staged["r_old"] = "crsp/incoming/m2/b"
	e.repo.add(registry.Image{ID: "s_new", State: registry.StateStaged, PushedAt: t0.Add(-2 * time.Hour)})
	e.repo.staged["s_new"] = "crsp/incoming/m1/c"
	_, err := e.svc.Cleanup(context.Background(), TriggerNow)
	must(t, err)
	if e.repo.images["s_old"].State != registry.StateDeleted || e.repo.images["r_old"].State != registry.StateDeleted {
		t.Fatal("old staged or refused image stays")
	}
	if e.repo.images["s_new"].State != registry.StateStaged {
		t.Fatal("a staged image under a day old was removed")
	}
	if len(e.reg.deleted) != 2 || e.reg.deleted[0] != "crsp/incoming/m1/a@sha256:s1" && e.reg.deleted[1] != "crsp/incoming/m1/a@sha256:s1" {
		t.Fatalf("manifests deleted: %v", e.reg.deleted)
	}
}

// The cleanup acts on what crsp may delete alone before the sweep, so the sweep frees it.
func TestCleanupActsOnDueRecommendationsFirst(t *testing.T) {
	e := newEnv(t)
	e.repo.add(registry.Image{ID: "old", State: registry.StateSuperseded, KeepUntil: day(-1)})
	_, err := e.svc.Cleanup(context.Background(), TriggerWeekly)
	must(t, err)
	if e.repo.images["old"].State != registry.StateDeleted {
		t.Fatal("a due superseded image survived the cleanup")
	}
	if e.reg.roAtDelete[0] {
		t.Fatal("the manifest delete should come before the window")
	}
}

// Clean up now while one runs: conflict.
func TestStartCleanupRefusesASecond(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	e.svc.o.Registry = &blockingRegistry{fakeRegistry: e.reg, release: release}
	row, err := e.svc.StartCleanup(context.Background(), TriggerNow, "luis")
	must(t, err)
	if row.State != "running" || row.Trigger != TriggerNow {
		t.Fatalf("%+v", row)
	}
	if _, err := e.svc.StartCleanup(context.Background(), TriggerNow, "luis"); !errors.Is(err, ErrCleanupRunning) {
		t.Fatalf("got %v", err)
	}
	close(release)
	e.svc.Wait()
	if c, _ := e.repo.Cleanup(context.Background(), row.ID); c.State != "done" {
		t.Fatalf("%+v", c)
	}
	// After the first ended another may start.
	if _, err := e.svc.StartCleanup(context.Background(), TriggerNow, "luis"); err != nil {
		t.Fatal(err)
	}
	e.svc.Wait()
}

type blockingRegistry struct {
	*fakeRegistry
	release chan struct{}
}

func (b *blockingRegistry) GC(ctx context.Context, dry bool) (int64, error) {
	<-b.release
	return b.fakeRegistry.GC(ctx, dry)
}

// R5.42: the weekly cleanup runs at its time, once, and not at other times.
func TestTickRunsTheWeeklyCleanupOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	must(t, e.svc.Tick(ctx, t0)) // Thursday noon
	if len(e.repo.cleanups) != 0 {
		t.Fatal("ran on a Thursday")
	}
	sun := time.Date(2026, 11, 22, 3, 5, 0, 0, time.UTC)
	e.now = sun
	must(t, e.svc.Tick(ctx, sun))
	if len(e.repo.cleanups) != 1 || e.repo.cleanups[0].Trigger != TriggerWeekly {
		t.Fatalf("%+v", e.repo.cleanups)
	}
	e.repo.cleanups[0].StartedAt = sun
	must(t, e.svc.Tick(ctx, sun.Add(10*time.Minute)))
	if len(e.repo.cleanups) != 1 {
		t.Fatal("ran twice in one week slot")
	}
	// A week later it runs again.
	next := sun.AddDate(0, 0, 7)
	e.now = next
	must(t, e.svc.Tick(ctx, next))
	if len(e.repo.cleanups) != 2 {
		t.Fatalf("no cleanup the next week: %d", len(e.repo.cleanups))
	}
}
