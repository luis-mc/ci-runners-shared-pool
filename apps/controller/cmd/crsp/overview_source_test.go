package main

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// R3-7: the overview names the queued and the running jobs, not an empty list.
func TestPoolOverviewSourceListsOpenJobs(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	js := db.Jobs()
	now := time.Now().UTC()
	for _, j := range []jobsdata.Job{
		{ID: 1, RepositoryID: 5, Repository: "o/r", RunID: 1, Attempt: 1, Workflow: "ci.yml", Name: "test", JobType: "test", JobKey: "o/r/ci.yml/test", State: jobsdata.Queued, CreatedAt: now},
		{ID: 2, RepositoryID: 5, Repository: "o/r", RunID: 1, Attempt: 1, Workflow: "ci.yml", Name: "lint", JobType: "lint", JobKey: "o/r/ci.yml/lint", State: jobsdata.Running, CreatedAt: now, StartedAt: now},
	} {
		if err := js.UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	svc := &overview.Service{Source: &poolOverviewSource{DB: db}}
	ov, err := svc.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ov.QueuedCount != 1 || ov.Queued == nil || len(*ov.Queued) != 1 || len(ov.Running) != 1 || ov.Running[0].Id != 2 {
		t.Fatalf("overview %+v", ov)
	}
}
