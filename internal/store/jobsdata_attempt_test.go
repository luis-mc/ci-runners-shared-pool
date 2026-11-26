package store

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// R5.7: one history row per job attempt. The same GitHub job id written again and again stays one row; a re-run is
// a new job id with the same run id and name, so it is a second row with its own attempt number.
func TestOneRowPerJobAttempt(t *testing.T) {
	s := jobsDB(t)
	ctx := context.Background()
	first := mkJob(1, "build", jobsdata.Queued, "", at(0), 0, 0)
	for _, j := range []jobsdata.Job{
		first,
		mkJob(1, "build", jobsdata.Running, "", at(0), 5, 0),
		mkJob(1, "build", jobsdata.Done, jobsdata.Failure, at(0), 5, 60),
		first, // a late event
	} {
		must(t, s.UpsertJob(ctx, j))
	}
	rerun := mkJob(2, "build", jobsdata.Done, jobsdata.Success, at(10), 5, 60)
	rerun.Attempt = 2
	must(t, s.UpsertJob(ctx, rerun))
	must(t, s.UpsertJob(ctx, rerun))
	must(t, s.UpsertJob(ctx, mkJob(3, "lint", jobsdata.Done, jobsdata.Success, at(0), 5, 30)))

	rows, total, err := s.ListJobs(ctx, jobsdata.JobFilter{})
	must(t, err)
	if total != 3 || len(rows) != 3 {
		t.Fatalf("rows %d total %d, want 3 (attempt 1, attempt 2, lint)", len(rows), total)
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.ID] {
			t.Fatalf("job %d listed twice", r.ID)
		}
		seen[r.ID] = true
	}
	d, ok, err := s.GetJob(ctx, 1)
	must(t, err)
	if !ok || d.Result != jobsdata.Failure || d.Attempt != 1 || len(d.Attempts) != 2 {
		t.Fatalf("attempt 1 row: %+v attempts %+v", d.Job, d.Attempts)
	}
}
