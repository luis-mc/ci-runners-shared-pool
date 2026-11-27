package api

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// The live overview (observability R5.1) shows the same machines and jobs as the lists do; these two functions give the
// controller's overview source the contract shapes, so there is one place that turns a machine view or a job and its
// forecast into what the console reads (audit R3-7).

// OverviewMachines are the machine views as the contract shows them.
func OverviewMachines(vs []machines.View, now time.Time) []gen.Machine {
	out := make([]gen.Machine, 0, len(vs))
	for _, v := range vs {
		out = append(out, machineToAPI(v, now))
	}
	return out
}

// OverviewJobs reads the queued and running jobs once and gives each with its forecast and, when queued, its place,
// the way the job list does. Queued jobs come in queue order.
func OverviewJobs(ctx context.Context, data jobsdata.Reader, now time.Time) (queued, running []gen.Job, err error) {
	rows, err := data.OpenJobs(ctx)
	if err != nil {
		return nil, nil, err
	}
	s := &server{opts: Options{JobsData: data, Now: func() time.Time { return now }}}
	items, err := s.jobItems(ctx, rows)
	if err != nil {
		return nil, nil, err
	}
	for i, r := range rows {
		if r.State == jobsdata.Queued {
			queued = append(queued, items[i])
		} else {
			running = append(running, items[i])
		}
	}
	return queued, running, nil
}
