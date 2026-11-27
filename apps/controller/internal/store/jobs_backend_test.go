package store

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// The jobs list carries the runner's backend and isolation, and filters by backend (web-console R5.24).
func TestIntegrationListJobsBackend(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.SeedMachine(ctx, "m1", "linux"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepoPolicyForTests(ctx, 5, "o/r", slots.Policy{Priority: 1, MaxParallel: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO runners (id, name, machine_id, os, arch, kind, backend, isolation, scope_kind)
		VALUES ('r1', 'run-1', 'm1', 'linux', 'x64', 'ephemeral', 'docker', 'container', 'organization')`); err != nil {
		t.Fatal(err)
	}
	for _, j := range []slots.JobRecord{
		{ID: 2, RepositoryID: 5, RunID: 2, RunAttempt: 1, JobName: "a", RunnerName: "run-1", MachineID: "m1"},
		{ID: 1, RepositoryID: 5, RunID: 1, RunAttempt: 1, JobName: "b"},
	} {
		if err := s.UpsertJobForTests(ctx, j, "completed"); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListJobs(ctx, slots.JobFilter{Limit: 10})
	if err != nil || len(all) != 2 {
		t.Fatalf("all: %v %v", all, err)
	}
	if all[0].Backend != "docker" || all[0].Isolation != "container" || all[1].Backend != "" {
		t.Fatalf("backend/isolation: %+v", all)
	}
	got, err := s.ListJobs(ctx, slots.JobFilter{Limit: 10, Backend: "docker"})
	if err != nil || len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("filtered: %v %v", got, err)
	}
}
