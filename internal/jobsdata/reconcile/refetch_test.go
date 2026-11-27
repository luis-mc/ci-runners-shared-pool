package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"
)

// failingGH answers every read with an error GitHub could give (a 5xx, a rate limit, no network).
type failingGH struct{ calls int }

func (g *failingGH) Job(context.Context, int64, string, string, int64) (RemoteJob, error) {
	g.calls++
	return RemoteJob{}, errors.New("github: 502 bad gateway")
}

func (g *failingGH) RunJobs(context.Context, int64, string, string, int64, int) ([]RemoteJob, error) {
	g.calls++
	return nil, errors.New("github: 502 bad gateway")
}

// R5.12: a finished row is never fetched again. The pass reads only the runs the store lists as stale, and the
// store lists open jobs only; with none listed GitHub is not called, and a job whose steps are stored is not read.
func TestFinishedRowNeverRefetched(t *testing.T) {
	gh, st := stepsFixture()
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 10}, &now)
	ctx := context.Background()
	if n, err := r.Pass(ctx, now); err != nil || n != 0 {
		t.Fatalf("Pass = %d, %v", n, err)
	}
	for _, id := range []int64{2, 3} { // finished, steps stored
		if err := r.JobCompleted(ctx, 1, id); err != nil {
			t.Fatal(err)
		}
	}
	if gh.jobCalls != 0 || len(gh.runCalls) != 0 {
		t.Fatalf("GitHub was read: %d job calls, runs %v", gh.jobCalls, gh.runCalls)
	}
}

// R5.12: a failed GitHub read keeps the stored rows. Nothing is applied, nothing is marked lost, the run is not
// counted as checked and the pass goes on to the next run.
func TestGitHubFailureKeepsRows(t *testing.T) {
	gh := &failingGH{}
	st := &fakeStore{runs: []StaleRun{staleRun(10), staleRun(11)}, open: map[int64][]int64{10: {100}, 11: {110}}}
	now := t0
	r := New(Config{GitHub: gh, Store: st, Jobs: st, Budget: &fakeBudget{left: 5},
		Classify:     NewClassifier(fakeRunners{}),
		Installation: func(context.Context, int64) (int64, bool) { return 7, true },
		Now:          func() time.Time { return now }})
	n, err := r.Pass(context.Background(), now)
	if err != nil || n != 0 {
		t.Fatalf("Pass = %d, %v: a failed read is logged, not returned", n, err)
	}
	if gh.calls != 2 {
		t.Fatalf("%d reads, want one per run", gh.calls)
	}
	if len(st.applied) != 0 || len(st.lost) != 0 {
		t.Fatalf("rows changed after a failed read: applied %v, lost %v", st.applied, st.lost)
	}
}
