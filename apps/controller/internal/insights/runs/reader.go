package runs

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// RunSource loads the jobs of one run attempt and the median run time per job key. Contract gap: the CIX8
// contract has no run reader, so the Reader takes this local interface.
type RunSource interface {
	RunJobs(ctx context.Context, runID int64, attempt int) ([]insights.JobFact, error)
	Medians(ctx context.Context, repositoryID int64) (map[string]time.Duration, error)
}

// StoreReader answers getRunSavings (R5.23) and the fail-fast waste of the run (R5.27). Superseded, duplicate and
// re-tested waste need the sibling runs of a window and come from the analyser findings, not from one run.
type StoreReader struct {
	Runs    RunSource
	Graphs  GraphSource
	Release func(branch string) bool
}

var _ Reader = StoreReader{}

// RunSavings returns the top savings and the waste of a finished run; reason says why there are none.
func (r StoreReader) RunSavings(ctx context.Context, runID int64, attempt int) ([]Saving, []Waste, string, error) {
	jobs, err := r.Runs.RunJobs(ctx, runID, attempt)
	if err != nil {
		return nil, nil, "", err
	}
	if len(jobs) == 0 {
		return nil, nil, "The run is not known yet.", nil
	}
	for _, j := range jobs {
		if j.CompletedAt.IsZero() {
			return nil, nil, "The run has not finished.", nil
		}
	}
	needs, err := r.Graphs.Needs(ctx, jobs[0].RepositoryID, runID, attempt)
	if err != nil {
		return nil, nil, "", err
	}
	med, err := r.Runs.Medians(ctx, jobs[0].RepositoryID)
	if err != nil {
		return nil, nil, "", err
	}
	rel := r.Release
	if rel == nil {
		rel = defaultRelease
	}
	s := NewSavings().Top(Graph{Jobs: jobs, Needs: needs, Medians: med})
	var waste []Waste
	if w, ok := failFastWaste(runInfo{First: jobs[0], Jobs: jobs}, rel); ok {
		waste = append(waste, w)
	}
	reason := ""
	if len(s) == 0 && len(waste) == 0 {
		reason = "Nothing on this run would have ended it sooner."
	}
	return s, waste, reason, nil
}
