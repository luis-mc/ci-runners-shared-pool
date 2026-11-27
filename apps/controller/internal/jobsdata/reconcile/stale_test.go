package reconcile

import (
	"context"
	"testing"
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
