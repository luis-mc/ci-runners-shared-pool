package store

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

func TestPoolJobGroupsOnlyPoolJobsInWindow(t *testing.T) {
	js := jobsDB(t)
	ctx := context.Background()
	pool := mkJob(1, "build", jobsdata.Done, jobsdata.Success, at(0), 5, 150)
	pool.MachineID, pool.MachineName = "m-1", "box"
	hosted := mkJob(2, "test", jobsdata.Done, jobsdata.Success, at(0), 5, 600) // no machine: ran elsewhere
	queued := mkJob(3, "lint", jobsdata.Queued, "", at(0), 0, 0)
	for _, j := range []jobsdata.Job{pool, hosted, queued} {
		must(t, js.UpsertJob(ctx, j))
	}
	got, err := js.PoolJobGroups(ctx, at(-60), at(60))
	must(t, err)
	if len(got) != 1 || got[0].Machine != "box" || got[0].Minutes != 3 || got[0].Jobs != 1 || got[0].Repository != "o/r" {
		t.Fatalf("%+v", got)
	}
	none, err := js.PoolJobGroups(ctx, at(600), at(700))
	must(t, err)
	if len(none) != 0 {
		t.Fatalf("outside the window: %+v", none)
	}
}

func TestPoolJobGroupsRoundUpPerJobAndGroup(t *testing.T) {
	js := jobsDB(t)
	ctx := context.Background()
	// 60 s is one minute, 61 s is two: the round-up is per job, never on the sum.
	for i, run := range []int{60, 60, 61, 1} {
		j := mkJob(int64(10+i), "build", jobsdata.Done, jobsdata.Success, at(0), 5, run)
		j.MachineID, j.MachineName = "m-1", "box"
		must(t, js.UpsertJob(ctx, j))
	}
	got, err := js.PoolJobGroups(ctx, at(-60), at(600))
	must(t, err)
	by := map[int]int{}
	for _, g := range got {
		by[g.Minutes] += g.Jobs
	}
	if len(got) != 2 || by[1] != 3 || by[2] != 1 {
		t.Fatalf("want 3 jobs of 1 minute and 1 of 2: %+v", got)
	}
}
