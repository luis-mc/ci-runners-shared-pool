package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/depsetup"
)

// TestDepsJobSurvivesReopen proves a paused job is read back whole after the
// database is closed and opened again (DEP7: a controller restart loses nothing).
func TestDepsJobSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	pct := 42.5
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	j := depsetup.Job{ID: "dep_1", MachineID: "m1", State: depsetup.JobPausedRestart, ChoiceID: "docker-in-wsl2", ItemIDs: []string{"wsl2", "docker-in-wsl2"},
		RunnerOS: "linux", RunnerKind: "persistent", Actor: "luis", AcceptLicence: true, CreatedAt: now, UpdatedAt: now.Add(time.Minute),
		Added: []depsetup.Added{{ItemID: "wsl2", Kind: "wsl-feature", Ref: "VirtualMachinePlatform", Version: "inbox"}}, ManualSteps: []string{"wsl --install"}, RestartSent: true,
		Last: &depsetup.Event{JobID: "dep_1", Phase: "restart", Pct: &pct, Stream: "status", Ts: 5}, Log: []depsetup.Event{{JobID: "dep_1", Phase: "install", Stream: "stdout", Line: "x", Ts: 4}}}
	if err := s.DepsJobs().Put(ctx, j); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r := s.DepsJobs()
	got, ok, err := r.Get(ctx, "m1", "dep_1")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if got.State != depsetup.JobPausedRestart || !got.AcceptLicence || !got.RestartSent || len(got.ItemIDs) != 2 || len(got.Added) != 1 || got.Added[0].Ref != "VirtualMachinePlatform" ||
		got.Last == nil || got.Last.Pct == nil || *got.Last.Pct != 42.5 || len(got.Log) != 1 || got.ManualSteps[0] != "wsl --install" || !got.UpdatedAt.Equal(j.UpdatedAt) {
		t.Fatalf("%+v", got)
	}
	if _, ok, _ := r.Get(ctx, "other", "dep_1"); ok {
		t.Fatal("a job of another machine was read")
	}
	run, err := r.Unfinished(ctx, "m1")
	if err != nil || len(run) != 1 {
		t.Fatalf("%v %d", err, len(run))
	}
	// Update in place, then it no longer holds the machine; the list is newest first.
	got.State = depsetup.JobDone
	got.UpdatedAt = now.Add(2 * time.Minute)
	if err := r.Put(ctx, got); err != nil {
		t.Fatal(err)
	}
	later := j
	later.ID, later.State, later.CreatedAt = "dep_2", depsetup.JobFailed, now.Add(time.Hour)
	_ = r.Put(ctx, later)
	if run, _ := r.Unfinished(ctx, "m1"); len(run) != 0 {
		t.Fatalf("%d", len(run))
	}
	l, err := r.List(ctx, "m1", 20)
	if err != nil || len(l) != 2 || l[0].ID != "dep_2" || l[1].State != depsetup.JobDone {
		t.Fatalf("%v %+v", err, l)
	}
}
