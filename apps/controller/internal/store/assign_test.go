package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/admit"
)

func TestIntegrationAssignLeaseCompareAndSet(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := s.Assign()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	l := assign.Lease{JobID: 5, RunID: 50, Repository: "o/r", JobName: "e2e", Labels: []string{"self-hosted", "linux"}, JobLabel: "crsp-e2e-ab12",
		OS: "linux", State: assign.Waiting, Req: admit.Req{CPUs: 4, MemoryGiB: 2}, QueuedAt: now, UpdatedAt: now}
	got, created, err := a.Claim(ctx, l)
	if err != nil || !created || got.JobID != 5 {
		t.Fatalf("claim: %+v %v %v", got, created, err)
	}
	if _, created, _ := a.Claim(ctx, l); created {
		t.Fatal("a second claim of one job id must lose")
	}
	if ok, err := a.Begin(ctx, 5, "m1", "crsp-job-5", now); err != nil || !ok {
		t.Fatalf("begin: %v %v", ok, err)
	}
	if ok, _ := a.Begin(ctx, 5, "m1", "crsp-job-5b", now); ok {
		t.Fatal("a second begin must lose")
	}
	cur, _, _ := a.Get(ctx, 5)
	if cur.State != assign.Starting || cur.RunnerName != "crsp-job-5" || cur.Req.CPUs != 4 || cur.Labels[1] != "linux" || !cur.QueuedAt.Equal(now) {
		t.Fatalf("lease: %+v", cur)
	}
	if by, ok, _ := a.ByRunner(ctx, "crsp-job-5"); !ok || by.JobID != 5 {
		t.Fatalf("by runner: %+v", by)
	}
	cur.State, cur.Reason, cur.Alerted, cur.NextTry = assign.Running, "x", true, now.Add(time.Minute)
	if err := a.Save(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if ls, _ := a.List(ctx, assign.Running); len(ls) != 1 || !ls[0].Alerted || ls[0].Reason != "x" || !ls[0].NextTry.Equal(now.Add(time.Minute)) {
		t.Fatalf("list: %+v", ls)
	}
	if ls, _ := a.List(ctx, assign.Waiting, assign.Done); len(ls) != 0 {
		t.Fatalf("list by state: %+v", ls)
	}
}

func TestIntegrationAssignWarmAndSets(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	a := s.Assign()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	w := assign.Warm{ID: "w1", MachineID: "m1", LabelKey: "k", Repository: "o/r", OS: "linux", Labels: []string{"a"}, RunnerName: "w1",
		RunnerRecord: "r_1", GitHubRunner: 9, Req: admit.Req{CPUs: 2, MemoryGiB: 2}, StartedAt: now}
	if err := a.PutWarm(ctx, w); err != nil {
		t.Fatal(err)
	}
	if ws, _ := a.ListWarm(ctx); len(ws) != 1 || ws[0].GitHubRunner != 9 || ws[0].Labels[0] != "a" {
		t.Fatalf("warm: %+v", ws)
	}
	_ = a.DeleteWarm(ctx, "w1")
	if ws, _ := a.ListWarm(ctx); len(ws) != 0 {
		t.Fatal("deleted")
	}
	if err := a.TouchSet(ctx, assign.Set{Key: "k", Repository: "o/r", OS: "linux", Labels: []string{"a"}, Req: admit.Req{CPUs: 2, MemoryGiB: 2}, LastBusy: now}); err != nil {
		t.Fatal(err)
	}
	if ss, _ := a.ListSets(ctx); len(ss) != 1 || !ss[0].LastBusy.Equal(now) {
		t.Fatalf("sets: %+v", ss)
	}
}
