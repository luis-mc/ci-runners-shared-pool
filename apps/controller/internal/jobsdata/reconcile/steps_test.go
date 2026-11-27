package reconcile

import (
	"context"
	"testing"
	"time"
)

func stepsFixture() (*fakeGH, *fakeStore) {
	gh := &fakeGH{jobs: map[int64]RemoteJob{
		1: {ID: 1, Status: "completed", Steps: []RemoteStep{{Number: 1, Name: "Set up job", Status: "completed", Conclusion: "success"}}},
	}}
	st := &fakeStore{refs: map[int64]JobRef{
		1: {Repository: "o/r", State: "done"},
		2: {Repository: "o/r", State: "done", StepsSource: "events", HasSteps: true},
		3: {Repository: "o/r", State: "done", HasSteps: true}, // steps stored by an unknown source
	}}
	return gh, st
}

func TestStepsByRESTOnce(t *testing.T) { // R5.40
	gh, st := stepsFixture()
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 10}, &now)
	ctx := context.Background()
	if err := r.JobCompleted(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.JobCompleted(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
	if gh.jobCalls != 1 || len(st.steps[1]) != 1 {
		t.Fatalf("calls %d, steps %v: want one read and one step", gh.jobCalls, st.steps)
	}
	// Steps that came from events, or are stored already, are not read again.
	for _, id := range []int64{2, 3} {
		if err := r.JobCompleted(ctx, 1, id); err != nil {
			t.Fatal(err)
		}
	}
	if gh.jobCalls != 1 {
		t.Fatalf("jobs with steps were read: %d calls", gh.jobCalls)
	}
}

func TestWatchReadsAtMostEveryMinute(t *testing.T) { // R5.40
	gh, st := stepsFixture()
	st.refs[4] = JobRef{Repository: "o/r", State: "running"}
	gh.jobs[4] = RemoteJob{ID: 4, Status: "in_progress", Steps: []RemoteStep{{Number: 1, Name: "build", Status: "in_progress"}}}
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 10}, &now)
	ctx := context.Background()
	for _, d := range []time.Duration{0, 30 * time.Second, 59 * time.Second, 61 * time.Second} {
		now = t0.Add(d)
		if err := r.Watch(ctx, 1, 4); err != nil {
			t.Fatal(err)
		}
	}
	if gh.jobCalls != 2 {
		t.Fatalf("calls = %d, want 2 (at 0 s and 61 s)", gh.jobCalls)
	}
	// A job that is not running is left to JobCompleted.
	if err := r.Watch(ctx, 1, 1); err != nil || gh.jobCalls != 2 {
		t.Fatalf("done job watched: %d calls, %v", gh.jobCalls, err)
	}
}

func TestStepsHeldWhenShareSpent(t *testing.T) { // R6.1
	gh, st := stepsFixture()
	now := t0
	r := newRec(gh, st, &fakeBudget{left: 0}, &now)
	if err := r.JobCompleted(context.Background(), 1, 1); err != nil {
		t.Fatal(err)
	}
	if gh.jobCalls != 0 {
		t.Fatal("a call was made over the share")
	}
	// The read is not marked done: it is made when the share frees.
	r2 := newRec(gh, st, &fakeBudget{left: 1}, &now)
	if err := r2.JobCompleted(context.Background(), 1, 1); err != nil || gh.jobCalls != 1 {
		t.Fatalf("read after share freed: %d calls, %v", gh.jobCalls, err)
	}
}
