package history

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

var c0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

func sec(n int) time.Time { return c0.Add(time.Duration(n) * time.Second) }

func jobAt(id int64, name string, from, to int) jobsdata.Job {
	return jobsdata.Job{ID: id, RunID: 1, Name: name, StartedAt: sec(from), CompletedAt: sec(to), State: jobsdata.Done}
}

func runOf(id int64, state jobsdata.State, created, completed int, jobs ...jobsdata.Job) jobsdata.RunDetail {
	r := jobsdata.RunDetail{Jobs: jobs}
	r.RunID, r.Attempt, r.Workflow, r.CreatedAt = id, 1, "ci.yml", sec(created)
	r.State, r.JobsTotal = state, len(jobs)
	if state == jobsdata.Done {
		r.CompletedAt, r.Conclusion = sec(completed), jobsdata.Success
	}
	return r
}

// R5.13: "all runs done" only when every run of the commit has finished, including one started on demand later.
func TestAllRunsDoneNeedsEveryRun(t *testing.T) {
	done := runOf(1, jobsdata.Done, 0, 100)
	if c := SummarizeCommit(nil, sec(0)); c.AllRunsDone {
		t.Error("no runs at all is not all done")
	}
	if c := SummarizeCommit([]jobsdata.RunDetail{done}, sec(200)); !c.AllRunsDone || c.RunsDone != 1 {
		t.Errorf("one finished run: %+v", c)
	}
	onDemand := runOf(2, jobsdata.Running, 120, 0)
	c := SummarizeCommit([]jobsdata.RunDetail{done, onDemand}, sec(150))
	if c.AllRunsDone || c.RunsDone != 1 || len(c.Runs) != 2 {
		t.Errorf("a run still going: %+v", c)
	}
	if c.WallSeconds != 150 {
		t.Errorf("an open commit measures to now: wall %v, want 150", c.WallSeconds)
	}
	queued := runOf(3, jobsdata.Queued, 130, 0)
	if c := SummarizeCommit([]jobsdata.RunDetail{done, queued}, sec(150)); c.AllRunsDone {
		t.Error("a queued run is not done")
	}
	finished := runOf(2, jobsdata.Done, 120, 180)
	if c := SummarizeCommit([]jobsdata.RunDetail{done, finished}, sec(900)); !c.AllRunsDone || c.WallSeconds != 180 {
		t.Errorf("both finished: %+v", c)
	}
}

// R5.13: the slowest job, its share of the wall time and its two slowest steps.
func TestSlowestJobShare(t *testing.T) {
	a := runOf(1, jobsdata.Done, 0, 200, jobAt(10, "lint", 5, 25), jobAt(11, "test", 10, 160))
	b := runOf(2, jobsdata.Done, 20, 100, jobAt(20, "docs", 25, 60))
	c := SummarizeCommit([]jobsdata.RunDetail{a, b}, sec(500))
	if c.Slowest == nil || c.Slowest.JobID != 11 || c.Slowest.Name != "test" || c.Slowest.Seconds != 150 {
		t.Fatalf("slowest %+v", c.Slowest)
	}
	if c.WallSeconds != 200 || c.Slowest.SharePercent != 75 {
		t.Errorf("wall %v share %d, want 200 and 75", c.WallSeconds, c.Slowest.SharePercent)
	}
	if c.Runs[0].RunID != 1 || c.Runs[1].RunID != 2 {
		t.Errorf("runs not in start order: %+v", c.Runs)
	}
	// A job with no measured time is never the slowest, and no jobs gives none.
	if got := SummarizeCommit([]jobsdata.RunDetail{runOf(1, jobsdata.Done, 0, 10, jobsdata.Job{ID: 1, Name: "x"})}, sec(20)); got.Slowest != nil {
		t.Errorf("slowest of unmeasured jobs: %+v", got.Slowest)
	}
	steps := []jobsdata.Step{
		{Number: 1, Name: "checkout", StartedAt: sec(0), CompletedAt: sec(5)},
		{Number: 2, Name: "build", StartedAt: sec(5), CompletedAt: sec(105)},
		{Number: 3, Name: "test", StartedAt: sec(105), CompletedAt: sec(145)},
		{Number: 4, Name: "no times"},
	}
	got := SlowestSteps(steps, 2)
	if len(got) != 2 || got[0].Name != "build" || got[0].Seconds != 100 || got[1].Name != "test" || got[1].Seconds != 40 {
		t.Errorf("two slowest steps: %+v", got)
	}
}

// R5.13: set up job, complete job and the post steps are left out, as in ci_runs.py.
func TestBookkeepingStepsSkipped(t *testing.T) {
	steps := []jobsdata.Step{
		{Number: 1, Name: "Set up job", StartedAt: sec(0), CompletedAt: sec(300)},
		{Number: 2, Name: "Run tests", StartedAt: sec(300), CompletedAt: sec(310)},
		{Number: 3, Name: "Post Run actions/checkout@v4", StartedAt: sec(310), CompletedAt: sec(500)},
		{Number: 4, Name: "Complete job", StartedAt: sec(500), CompletedAt: sec(900)},
	}
	got := SlowestSteps(steps, 2)
	if len(got) != 1 || got[0].Name != "Run tests" {
		t.Fatalf("steps %+v, want only Run tests", got)
	}
}
