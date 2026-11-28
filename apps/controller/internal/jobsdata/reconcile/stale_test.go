package reconcile

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func staleRun(id int64) StaleRun {
	return StaleRun{RepositoryID: 1, Repository: "o/r", RunID: id, RunAttempt: 1, OldestUpdate: t0}
}

func TestStaleQueuedBecomesDone(t *testing.T) { // R5.41
	gh := &fakeGH{runs: map[int64][]RemoteJob{
		10: {
			{ID: 100, Status: "completed", Conclusion: "success", RunnerName: "old-pool-3", RunnerGroupName: "Default"},
			{ID: 101, Status: "completed", Conclusion: "failure", RunnerName: "crsp-1"},
		},
	}}
	st := &fakeStore{runs: []StaleRun{staleRun(10)}, open: map[int64][]int64{10: {100, 101}}}
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 5}, &now)
	n, err := r.Pass(context.Background(), now)
	if err != nil || n != 1 {
		t.Fatalf("Pass = %d, %v", n, err)
	}
	if len(gh.runCalls) != 1 {
		t.Fatalf("%d calls for one run", len(gh.runCalls))
	}
	if len(st.applied) != 2 || st.applied[0].origin != Other || st.applied[1].origin != Crsp {
		t.Fatalf("applied %+v", st.applied)
	}
	if len(st.lost) != 0 {
		t.Fatalf("jobs lost: %v", st.lost)
	}
	if st.gotLim != MaxRunsPass {
		t.Fatalf("limit %d, want %d", st.gotLim, MaxRunsPass)
	}
}

func TestGoneBecomesLost(t *testing.T) { // R5.41
	gh := &fakeGH{
		runs:     map[int64][]RemoteJob{10: {{ID: 100, Status: "completed", Conclusion: "success"}}},
		goneRuns: map[int64]bool{11: true},
	}
	st := &fakeStore{runs: []StaleRun{staleRun(10), staleRun(11)},
		open: map[int64][]int64{10: {100, 102}, 11: {110}}}
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 5}, &now)
	if n, err := r.Pass(context.Background(), now); err != nil || n != 2 {
		t.Fatalf("Pass = %d, %v", n, err)
	}
	if st.lost[102] != GoneReason {
		t.Fatalf("job missing from its run: lost = %v", st.lost)
	}
	if st.lost[110] != RunGoneWhy {
		t.Fatalf("job of a run GitHub dropped: lost = %v", st.lost)
	}
	if _, bad := st.lost[100]; bad {
		t.Fatal("a job GitHub still has was marked lost")
	}
}

func TestPassStopsWhenShareSpent(t *testing.T) { // R6.1: reconcile goes first, but is within the share
	gh := &fakeGH{runs: map[int64][]RemoteJob{}}
	st := &fakeStore{runs: []StaleRun{staleRun(10), staleRun(11), staleRun(12)}, open: map[int64][]int64{}}
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 2}, &now)
	n, err := r.Pass(context.Background(), now)
	if err != nil || n != 2 || len(gh.runCalls) != 2 || gh.runCalls[0] != 10 {
		t.Fatalf("Pass = %d, %v, calls %v: want the two oldest runs only", n, err, gh.runCalls)
	}
}

// CRSP392: a job crsp holds as running for days is closed with GitHub's real end, and the correction is reported.
func TestStaleRunningJobIsClosedAndReported(t *testing.T) {
	end := t0.Add(-90 * time.Hour)
	gh := &fakeGH{runs: map[int64][]RemoteJob{10: {{ID: 100, Status: "completed", Conclusion: "cancelled", CompletedAt: end}}}}
	st := &fakeStore{runs: []StaleRun{staleRun(10)}, open: map[int64][]int64{10: {100}}}
	now := t0
	var said []string
	r := New(Config{GitHub: gh, Store: st, Jobs: st, Budget: &fakeBudget{left: 5},
		Classify:     NewClassifier(fakeRunners{}),
		Installation: func(context.Context, int64) (int64, bool) { return 7, true },
		Now:          func() time.Time { return now },
		OnCorrected: func(_ context.Context, id int64, repo, s string) {
			said = append(said, fmt.Sprint(id, " ", repo, " ", s))
		}})
	if n, err := r.Pass(context.Background(), now); err != nil || n != 1 {
		t.Fatalf("Pass = %d, %v", n, err)
	}
	if len(st.applied) != 1 || st.applied[0].job.Conclusion != "cancelled" || !st.applied[0].job.CompletedAt.Equal(end) {
		t.Fatalf("applied %+v", st.applied)
	}
	if len(said) != 1 || said[0] != "100 o/r completed on GitHub: cancelled" {
		t.Fatalf("reported %v", said)
	}
}

// A job GitHub still has queued is stored as GitHub says it, never closed or lost, and not reported.
func TestStillQueuedJobIsLeft(t *testing.T) {
	gh := &fakeGH{runs: map[int64][]RemoteJob{10: {{ID: 100, Status: "queued"}}}}
	st := &fakeStore{runs: []StaleRun{staleRun(10)}, open: map[int64][]int64{10: {100}}}
	now := t0
	said := 0
	r := New(Config{GitHub: gh, Store: st, Jobs: st, Budget: &fakeBudget{left: 5}, Classify: NewClassifier(fakeRunners{}),
		Installation: func(context.Context, int64) (int64, bool) { return 7, true }, Now: func() time.Time { return now },
		OnCorrected: func(context.Context, int64, string, string) { said++ }})
	if _, err := r.Pass(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if len(st.lost) != 0 || said != 0 || len(st.applied) != 1 || st.applied[0].job.Status != "queued" {
		t.Fatalf("lost %v said %d applied %+v", st.lost, said, st.applied)
	}
}

type limitedGH struct {
	*fakeGH
	calls int
}

type limitErr struct{}

func (limitErr) Error() string     { return "limit" }
func (limitErr) RateLimited() bool { return true }

func (g *limitedGH) RunJobs(context.Context, int64, string, string, int64, int) ([]RemoteJob, error) {
	g.calls++
	return nil, fmt.Errorf("read: %w", limitErr{})
}

// GitHub's own limit stops the pass at the first refused call; the next pass goes on.
func TestPassStopsOnGitHubRateLimit(t *testing.T) {
	gh := &limitedGH{fakeGH: &fakeGH{}}
	st := &fakeStore{runs: []StaleRun{staleRun(10), staleRun(11), staleRun(12)}, open: map[int64][]int64{}}
	now := t0
	r := New(Config{GitHub: gh, Store: st, Jobs: st, Budget: &fakeBudget{left: 9}, Classify: NewClassifier(fakeRunners{}),
		Installation: func(context.Context, int64) (int64, bool) { return 7, true }, Now: func() time.Time { return now }})
	n, err := r.Pass(context.Background(), now)
	if err != nil || n != 0 || gh.calls != 1 {
		t.Fatalf("Pass = %d, %v after %d calls: want a stop at the first refusal", n, err, gh.calls)
	}
}

func TestRunPassesAtStartAndStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &countingRec{}
	c.onPass = cancel
	Run(ctx, c, nil, nil)
	if c.passes != 1 {
		t.Fatalf("%d passes, want the one at start", c.passes)
	}
}

type countingRec struct {
	Reconciler
	passes int
	onPass func()
}

func (c *countingRec) Pass(context.Context, time.Time) (int, error) {
	c.passes++
	c.onPass()
	return 0, nil
}
