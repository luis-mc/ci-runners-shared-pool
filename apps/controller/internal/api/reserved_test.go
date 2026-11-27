package api

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/assign"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/timers"
)

func TestReservedOfKeepsOnlyWaitingLeasesWithAReservation(t *testing.T) {
	got := reservedOf([]assign.Lease{
		{JobID: 1, State: assign.Waiting, ReservedSince: ago(5 * time.Minute)},
		{JobID: 2, State: assign.Waiting},                                  // waiting, not reserved
		{JobID: 3, State: assign.Running, ReservedSince: ago(time.Minute)}, // started: no longer reserved
	})
	if len(got) != 1 || !got[1].Equal(ago(5*time.Minute)) {
		t.Fatalf("reserved %v", got)
	}
}

func TestGraceOfOnlyForATimerThatRanOut(t *testing.T) {
	d := ago(-time.Minute)
	if got, ok := graceOf(timers.JobView{State: timers.StateGrace, Deadline: d}, true); !ok || !got.Equal(d) {
		t.Fatalf("grace %v %v", got, ok)
	}
	for _, v := range []timers.JobView{{State: timers.StateRunning, Deadline: d}, {State: timers.StateGrace}} {
		if _, ok := graceOf(v, true); ok {
			t.Fatalf("view %+v is not a timer that ran out", v)
		}
	}
	if _, ok := graceOf(timers.JobView{State: timers.StateGrace, Deadline: d}, false); ok {
		t.Fatal("a missing timer has no grace")
	}
}

func TestTimelineDrawsAReservedBar(t *testing.T) {
	since := ago(3 * time.Minute)
	tl := buildTimeline(timelineInput{Now: testNow, Machines: twoMachines,
		Open:  []jobsdata.Job{running(1, "m1", 5), queuedJob(2, "m1", "free_slot")},
		Flags: jobFlags{reserved: map[int64]time.Time{2: since}}})
	b := barsByJob(tl)[2]
	if b.State != "reserved" || b.RowId != "m1:waiting" || b.ReservedSince == nil || !b.ReservedSince.Equal(since) {
		t.Fatalf("bar %+v", b)
	}
	if b.End != nil {
		t.Fatal("a reserved bar has no end: the job starts when its room is free")
	}
}

func TestTimelineMarksATimerThatRanOut(t *testing.T) {
	deadline := ago(-30 * time.Second)
	tl := buildTimeline(timelineInput{Now: testNow, Machines: twoMachines,
		Open:  []jobsdata.Job{running(1, "m1", 40), running(2, "m1", 5)},
		Flags: jobFlags{grace: map[int64]time.Time{1: deadline}}})
	got := barsByJob(tl)
	if b := got[1]; b.State != "timer_ran_out" || b.ExpiresAt == nil || !b.ExpiresAt.Equal(deadline) {
		t.Fatalf("bar %+v", b)
	}
	if got[2].State != "running" || got[2].ExpiresAt != nil {
		t.Fatalf("the other job still runs: %+v", got[2])
	}
}

func TestMarkJobShowsReservationAndTimer(t *testing.T) {
	since, deadline := ago(time.Minute), ago(-time.Minute)
	f := jobFlags{reserved: map[int64]time.Time{7: since}, grace: map[int64]time.Time{8: deadline}}
	q := gen.Job{Id: 7, State: gen.JobStateQueued}
	f.markJob(&q)
	if q.WaitReason == nil || *q.WaitReason != gen.WaitReasonReserved || q.ReservedSince == nil || !q.ReservedSince.Equal(since) {
		t.Fatalf("queued job %+v", q)
	}
	r := gen.Job{Id: 8, State: gen.JobStateRunning}
	f.markJob(&r)
	if r.TimerDeadline == nil || !r.TimerDeadline.Equal(deadline) || r.WaitReason != nil {
		t.Fatalf("running job %+v", r)
	}
	other := gen.Job{Id: 9, State: gen.JobStateQueued}
	f.markJob(&other)
	if other.WaitReason != nil || other.TimerDeadline != nil {
		t.Fatalf("a plain job is left alone: %+v", other)
	}
}

type reservedLeases []assign.Lease

func (f reservedLeases) List(context.Context, ...assign.State) ([]assign.Lease, error) { return f, nil }

func TestOverviewShowsTheReservedJobAsReserved(t *testing.T) {
	s := &server{opts: Options{Logger: slog.Default(), Assign: reservedLeases{{JobID: 4, State: assign.Waiting, ReservedSince: ago(time.Minute)}}}}
	queued := []gen.Job{{Id: 4, State: gen.JobStateQueued}, {Id: 5, State: gen.JobStateQueued}}
	waiting := []gen.WaitingJob{{JobId: 4, Reason: gen.WaitReasonFreeSlot}, {JobId: 5, Reason: gen.WaitReasonFreeSlot}}
	o := gen.Overview{Queued: &queued, Waiting: &waiting}
	s.markOverview(context.Background(), &o)
	if r := queued[0].WaitReason; r == nil || *r != gen.WaitReasonReserved {
		t.Fatalf("job 4 %+v", queued[0])
	}
	if queued[1].WaitReason != nil {
		t.Fatalf("job 5 is not reserved: %+v", queued[1])
	}
	if waiting[0].Reason != gen.WaitReasonReserved || waiting[1].Reason != gen.WaitReasonFreeSlot {
		t.Fatalf("reasons %+v", waiting)
	}
}
