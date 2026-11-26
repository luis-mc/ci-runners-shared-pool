package history

import (
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// The commit view (observability R5.13): every workflow run and job of one head commit, whether the whole commit
// is done, and where the time went. The functions are pure; the API route reads the rows and calls them. The
// summary lines are ported from `summary_lines` of ci_watch.py (claude/plugins/delivery/templates/shared/scripts/
// delivery/ci_watch.py) and the bookkeeping rule from ci_runs.py.

// CommitRun is one run of the commit, its latest attempt.
type CommitRun struct {
	RunID       int64
	Attempt     int
	Workflow    string
	State       jobsdata.State
	Result      jobsdata.Result
	CreatedAt   time.Time
	CompletedAt time.Time
	HTMLURL     string
	Jobs        int
	JobsDone    int
}

// SlowJob is the job that took longest and its share of the wall time.
type SlowJob struct {
	JobID    int64
	RunID    int64
	Name     string
	Workflow string
	Seconds  float64
	// SharePercent is Seconds over the wall time of the commit, 0 to 100, rounded to a whole number. 0 when the
	// wall time is not known.
	SharePercent int
}

// StepTime is one step and how long it took.
type StepTime struct {
	Name    string
	Seconds float64
}

// Commit is the summary of one head commit.
type Commit struct {
	Runs []CommitRun
	// AllRunsDone is true only when there is at least one run and every run has finished (R5.13). A run that
	// was started on demand counts as soon as it is stored.
	AllRunsDone bool
	RunsDone    int
	// WallSeconds runs from the first run created to the last run completed (to now while one is open).
	WallSeconds float64
	// Slowest is nil when no job has a measured time.
	Slowest *SlowJob
}

// bookkeepingStep is true for the steps GitHub adds around every job: set up, complete and the post steps. They
// are never "now running" and never the slowest step (ci_runs.py).
func bookkeepingStep(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "set up job" || n == "complete job" || strings.HasPrefix(n, "post ")
}

// jobSeconds is how long a job ran: started to completed. 0 when either is unknown or the order is wrong.
func jobSeconds(j jobsdata.Job) float64 {
	if j.StartedAt.IsZero() || j.CompletedAt.IsZero() || j.CompletedAt.Before(j.StartedAt) {
		return 0
	}
	return j.CompletedAt.Sub(j.StartedAt).Seconds()
}

// SummarizeCommit builds the commit view from the latest attempt of each run. now closes the wall time of an
// open commit.
func SummarizeCommit(runs []jobsdata.RunDetail, now time.Time) Commit {
	out := Commit{Runs: make([]CommitRun, 0, len(runs))}
	var first, last time.Time
	open := false
	var slow *SlowJob
	for _, r := range runs {
		out.Runs = append(out.Runs, CommitRun{
			RunID: r.RunID, Attempt: r.Attempt, Workflow: r.Workflow, State: r.State, Result: r.Conclusion,
			CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt, HTMLURL: r.HTMLURL, Jobs: r.JobsTotal, JobsDone: r.JobsDone,
		})
		if r.State == jobsdata.Done {
			out.RunsDone++
		} else {
			open = true
		}
		if !r.CreatedAt.IsZero() && (first.IsZero() || r.CreatedAt.Before(first)) {
			first = r.CreatedAt
		}
		if r.CompletedAt.After(last) {
			last = r.CompletedAt
		}
		for _, j := range r.Jobs {
			if s := jobSeconds(j); s > 0 && (slow == nil || s > slow.Seconds) {
				slow = &SlowJob{JobID: j.ID, RunID: j.RunID, Name: j.Name, Workflow: r.Workflow, Seconds: s}
			}
		}
	}
	sort.SliceStable(out.Runs, func(i, j int) bool { return out.Runs[i].CreatedAt.Before(out.Runs[j].CreatedAt) })
	out.AllRunsDone = len(runs) > 0 && !open
	if open && now.After(last) {
		last = now
	}
	if !first.IsZero() && last.After(first) {
		out.WallSeconds = last.Sub(first).Seconds()
	}
	if slow != nil && out.WallSeconds > 0 {
		share := slow.Seconds / out.WallSeconds * 100
		if share > 100 {
			share = 100
		}
		slow.SharePercent = int(share + 0.5)
	}
	out.Slowest = slow
	return out
}

// SlowestSteps gives the n steps that took longest, longest first, leaving out the bookkeeping steps and steps
// without a measured time. Equal times keep their step order.
func SlowestSteps(steps []jobsdata.Step, n int) []StepTime {
	var all []StepTime
	for _, s := range steps {
		if bookkeepingStep(s.Name) || s.StartedAt.IsZero() || s.CompletedAt.IsZero() || !s.CompletedAt.After(s.StartedAt) {
			continue
		}
		all = append(all, StepTime{Name: s.Name, Seconds: s.CompletedAt.Sub(s.StartedAt).Seconds()})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Seconds > all[j].Seconds })
	if n >= 0 && len(all) > n {
		all = all[:n]
	}
	return all
}
