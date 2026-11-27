package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

func TestFinishedPoolJobsOnlyPoolJobsInWindow(t *testing.T) {
	js := jobsDB(t)
	ctx := context.Background()
	pool := mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(0), 5, 150)
	pool.MachineID, pool.MachineName = "m-1", "box"
	hosted := mkJob(2, "test", jobsdata.Done, jobsdata.Success, at(0), 5, 600) // no machine: ran elsewhere
	queued := mkJob(3, "lint", jobsdata.Queued, "", at(0), 0, 0)
	for _, j := range []jobsdata.Job{pool, hosted, queued} {
		must(t, js.UpsertJob(ctx, j))
	}
	got, err := js.FinishedPoolJobs(ctx, at(-60), at(60))
	must(t, err)
	if len(got) != 1 || got[0].Machine != "box" || got[0].Run != 150*time.Second || got[0].Repository != "o/r" {
		t.Fatalf("%+v", got)
	}
	none, err := js.FinishedPoolJobs(ctx, at(600), at(700))
	must(t, err)
	if len(none) != 0 {
		t.Fatalf("outside the window: %+v", none)
	}
}
