package main

import (
	"context"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// usualDuration is how long a job usually runs, from the finished jobs of the
// store: the median of the newest runs of the same repository and job name. A
// job with fewer than 3 finished runs has no usual duration (machines R5.55).
func usualDuration(jobs slots.Jobs) machines.TypicalFunc {
	return func(ctx context.Context, repository, job string) (time.Duration, bool) {
		rows, err := jobs.ListJobs(ctx, slots.JobFilter{State: slots.JobDone, Limit: 500})
		if err != nil {
			return 0, false
		}
		var runs []time.Duration
		for _, r := range rows {
			if r.Repository != repository || r.JobName != job || r.StartedAt.IsZero() || r.CompletedAt.IsZero() {
				continue
			}
			if d := r.CompletedAt.Sub(r.StartedAt); d > 0 {
				runs = append(runs, d)
			}
			if len(runs) == 30 {
				break
			}
		}
		if len(runs) < 3 {
			return 0, false
		}
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		return runs[len(runs)/2], true
	}
}
