package timers

import (
	"context"
	"reflect"
	"testing"
	"time"
)

var ctx = context.Background()

// Scheduling R5.104: a job timer that ran out gives a 30 s grace with a
// countdown, then stops that job's runner and only that job.
func TestJobTimerGraceThenStop(t *testing.T) {
	r := newRig()
	k := r.start(1)
	r.tick(29*time.Minute + 59*time.Second)
	if v, _ := r.eng.Job(ctx, k); v.State != StateRunning || v.Text != "Job timer 29:59 of 30:00" {
		t.Fatalf("running: %+v", v)
	}
	r.tick(time.Second)
	v, _ := r.eng.Job(ctx, k)
	if v.State != StateGrace || v.Text != "Job timer ran out at 30:00; stopping in 30 s" || !v.Deadline.Equal(t0.Add(30*time.Minute+Grace)) || !v.CanStopNow || !v.CanExtend {
		t.Fatalf("grace: %+v", v)
	}
	r.clock.Advance(10 * time.Second)
	if v, _ := r.eng.Job(ctx, k); v.Text != "Job timer ran out at 30:00; stopping in 20 s" {
		t.Fatalf("countdown: %q", v.Text)
	}
	r.tick(19 * time.Second)
	if len(r.agent.calls) != 0 {
		t.Fatalf("stopped inside the grace: %v", r.agent.calls)
	}
	r.tick(time.Second)
	if !reflect.DeepEqual(r.agent.calls, []string{"m1/runner-b"}) || len(r.gh.calls) != 0 {
		t.Fatalf("agent %v github %v", r.agent.calls, r.gh.calls)
	}
	v, _ = r.eng.Job(ctx, k)
	if v.State != StateStopped || v.Note != "stopped by crsp: job timer 30:00" {
		t.Fatalf("stopped: %+v", v)
	}
	if got := r.audit.actions(); !reflect.DeepEqual(got, []string{ActJobGrace, ActJobStop}) {
		t.Fatalf("audit %v", got)
	}
}

// R5.104: a job that ends during the grace clears the countdown and nothing is stopped.
func TestJobEndsDuringGrace(t *testing.T) {
	r := newRig()
	k := r.start(1)
	r.tick(30 * time.Minute)
	r.tick(10 * time.Second)
	if err := r.eng.JobEnded(ctx, k); err != nil {
		t.Fatal(err)
	}
	r.tick(time.Minute)
	if len(r.agent.calls) != 0 {
		t.Fatalf("stopped after the job ended: %v", r.agent.calls)
	}
	v, _ := r.eng.Job(ctx, k)
	if v.State != StateEnded || !v.Deadline.IsZero() || v.Text != "Job ended after 30:10" {
		t.Fatalf("view %+v", v)
	}
	if !contains(r.audit.actions(), ActJobGraceCleared) {
		t.Fatalf("audit %v", r.audit.actions())
	}
	c := r.store.jobs[k]
	if c.JobTimer != 30*time.Minute+10*time.Second || c.RunTimer != c.JobTimer || c.State != StateEnded {
		t.Fatalf("stored %+v", c)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// R5.104: the run timer starts at the first job's real start, not at later
// jobs, and cancels the whole run after its grace.
func TestRunTimerCancelsTheRun(t *testing.T) {
	r := newRig()
	r.start(1)
	r.tick(20 * time.Minute)
	r.start(2) // a later job does not restart the run clock
	r.tick(25 * time.Minute)
	rk := jobKey(1).Run()
	v, _ := r.eng.Run(ctx, rk)
	if v.State != StateGrace || v.Text != "Run timer ran out at 45:00; cancelling in 30 s" || !v.CanCancelNow {
		t.Fatalf("run grace: %+v", v)
	}
	r.tick(30 * time.Second)
	if !reflect.DeepEqual(r.gh.calls, []int64{900}) {
		t.Fatalf("cancel calls %v", r.gh.calls)
	}
	if v, _ = r.eng.Run(ctx, rk); v.State != StateStopped || v.Note != "cancelled by crsp: run timer 45:00" {
		t.Fatalf("run %+v", v)
	}
}

// R5.104: when the job and run timers run out together, only the run is cancelled.
func TestBothRunOutCancelRunOnly(t *testing.T) {
	r := newRig()
	r.start(1)
	r.tick(16 * time.Minute)
	k := r.start(2) // its 30 min end at 46:00, after the run's 45:00
	r.tick(29 * time.Minute)
	r.tick(30 * time.Second) // 45:30: run cancelled
	r.tick(30 * time.Second) // 46:00: job 2 runs out
	r.tick(time.Minute)      // 47:00: its grace is over
	if len(r.gh.calls) != 1 || len(r.agent.calls) != 0 {
		t.Fatalf("github %v agent %v", r.gh.calls, r.agent.calls)
	}
	v, _ := r.eng.Job(ctx, k)
	if !v.CoveredByRun || v.CanStopNow {
		t.Fatalf("job %+v", v)
	}
	if err := r.eng.StopNow(ctx, k, "owner"); err != ErrCovered && err != ErrNotInGrace {
		t.Fatalf("stop now on a covered job: %v", err)
	}
}

// R5.105: Extend 10 min works once per job, only during the grace.
func TestExtendOnce(t *testing.T) {
	r := newRig()
	k := r.start(1)
	if err := r.eng.Extend(ctx, k, "owner"); err != ErrNotInGrace {
		t.Fatalf("extend before the grace: %v", err)
	}
	r.tick(30*time.Minute + 5*time.Second)
	if err := r.eng.Extend(ctx, k, "owner"); err != nil {
		t.Fatal(err)
	}
	v, _ := r.eng.Job(ctx, k)
	if v.State != StateRunning || v.Limit != 40*time.Minute+5*time.Second || !v.Extended || !v.Deadline.IsZero() {
		t.Fatalf("after extend %+v", v)
	}
	r.tick(10 * time.Minute) // 40:05: out again
	if v, _ = r.eng.Job(ctx, k); v.State != StateGrace || v.CanExtend {
		t.Fatalf("second grace %+v", v)
	}
	if err := r.eng.Extend(ctx, k, "owner"); err != ErrExtendedOnce {
		t.Fatalf("second extend: %v", err)
	}
	r.tick(Grace)
	if len(r.agent.calls) != 1 {
		t.Fatalf("agent %v", r.agent.calls)
	}
	if n := count(r.audit.actions(), ActJobExtend); n != 1 {
		t.Fatalf("%d extend rows", n)
	}
}

func count(l []string, s string) int {
	n := 0
	for _, x := range l {
		if x == s {
			n++
		}
	}
	return n
}

// R5.105: Stop now stops at once and the audit row names who.
func TestStopNow(t *testing.T) {
	r := newRig()
	k := r.start(1)
	if err := r.eng.StopNow(ctx, k, "owner"); err != ErrNotInGrace {
		t.Fatalf("before the grace: %v", err)
	}
	r.tick(30*time.Minute + 3*time.Second)
	if err := r.eng.StopNow(ctx, k, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(r.agent.calls) != 1 {
		t.Fatalf("agent %v", r.agent.calls)
	}
	var who []string
	for _, e := range r.audit.rows {
		if e.Action == ActJobStopNow || e.Action == ActJobStop {
			who = append(who, e.Action+":"+e.Actor)
		}
	}
	if !reflect.DeepEqual(who, []string{ActJobStopNow + ":luis", ActJobStop + ":luis"}) {
		t.Fatalf("rows %v", who)
	}
	if err := r.eng.StopNow(ctx, k, "luis"); err != ErrNotInGrace {
		t.Fatalf("twice: %v", err)
	}
}

// R5.105: Cancel now cancels the run at once; the run can be extended once too.
func TestCancelNowAndExtendRun(t *testing.T) {
	r := newRig()
	r.start(1)
	rk := jobKey(1).Run()
	r.tick(45 * time.Minute)
	if err := r.eng.ExtendRun(ctx, rk, "luis"); err != nil {
		t.Fatal(err)
	}
	r.tick(10 * time.Minute)
	if err := r.eng.ExtendRun(ctx, rk, "luis"); err != ErrExtendedOnce {
		t.Fatalf("second: %v", err)
	}
	if err := r.eng.CancelNow(ctx, rk, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(r.gh.calls) != 1 || !contains(r.audit.actions(), ActRunCancelNow) {
		t.Fatalf("gh %v audit %v", r.gh.calls, r.audit.actions())
	}
}

// R5.106: an audit row that cannot be written stops the action.
func TestActionRefusedWhenAuditFails(t *testing.T) {
	r := newRig()
	k := r.start(1)
	r.tick(30*time.Minute + time.Second)
	r.audit.err = errBoom
	if err := r.eng.StopNow(ctx, k, "luis"); err != errBoom {
		t.Fatalf("stop now: %v", err)
	}
	if err := r.eng.Extend(ctx, k, "luis"); err != errBoom {
		t.Fatalf("extend: %v", err)
	}
	if len(r.agent.calls) != 0 {
		t.Fatalf("agent %v", r.agent.calls)
	}
	r.audit.err = nil
	if err := r.eng.Extend(ctx, k, "luis"); err != nil {
		t.Fatalf("extend after: %v", err)
	}
}

// R5.104: a stop the agent cannot do is tried again after another grace.
func TestStopFailureRetries(t *testing.T) {
	r := newRig()
	k := r.start(1)
	r.agent.err = errBoom
	r.clock.Advance(30 * time.Minute)
	_ = r.eng.Tick(ctx)
	r.clock.Advance(Grace)
	if err := r.eng.Tick(ctx); err == nil {
		t.Fatal("no error")
	}
	if v, _ := r.eng.Job(ctx, k); v.State != StateGrace {
		t.Fatalf("state %v", v.State)
	}
	r.agent.err = nil
	r.tick(Grace)
	if v, _ := r.eng.Job(ctx, k); v.State != StateStopped || len(r.agent.calls) != 2 {
		t.Fatalf("view %+v calls %v", v, r.agent.calls)
	}
}

// R5.103: the clock starts at JobStarted, keeps the durations and survives a restart.
func TestClockStartDurationsAndRestore(t *testing.T) {
	r := newRig()
	r.clock.Advance(time.Hour) // assignment long ago: the clock does not start there
	k := jobKey(1)
	_ = r.eng.JobStarted(ctx, JobStart{Key: k, MachineID: "m1", RunnerRef: "x", LimitMin: 20,
		Queued: time.Minute, Startup: 20 * time.Second, Hold: 5 * time.Second})
	c := r.store.jobs[k]
	if !c.StartedAt.Equal(t0.Add(time.Hour)) || c.Limit != 20*time.Minute || c.Queued != time.Minute || c.Startup != 20*time.Second || c.Hold != 5*time.Second {
		t.Fatalf("stored %+v", c)
	}
	e2 := New(Config{Clock: r.clock, Agent: r.agent, GitHub: r.gh, Audit: r.audit, Store: r.store})
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(20 * time.Minute)
	if err := e2.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := e2.Job(ctx, k); !ok || v.State != StateGrace {
		t.Fatalf("restored %+v", v)
	}
	if err := r.eng.JobStarted(ctx, JobStart{Key: k}); err != nil {
		t.Fatal(err)
	}
}

// R5.103: a per-job limit at or above the run timer is not used.
func TestPerJobLimitAboveRunTimerIsIgnored(t *testing.T) {
	r := newRig()
	k := jobKey(1)
	_ = r.eng.JobStarted(ctx, JobStart{Key: k, LimitMin: 50})
	if v, _ := r.eng.Job(ctx, k); v.Limit != 30*time.Minute {
		t.Fatalf("limit %v", v.Limit)
	}
}

type recQueued struct{ got []int64 }

func (q *recQueued) QueuedTooLong(_ context.Context, it QueuedItem, _ time.Duration) error {
	q.got = append(q.got, it.Key.JobID)
	return nil
}

type recStartup struct{ got []int64 }

func (q *recStartup) StartupTooLong(_ context.Context, it StartupItem, _ time.Duration) error {
	q.got = append(q.got, it.Key.JobID)
	return nil
}

// R5.107: the queued timer alerts once at 12 h; the start-up timer acts once at 2 min.
func TestQueuedAndStartupSweeps(t *testing.T) {
	r := newRig()
	q, s := &recQueued{}, &recStartup{}
	r.eng.cfg.Queued, r.eng.cfg.Startup = q, s
	items := []QueuedItem{{Key: jobKey(1), QueuedAt: t0}, {Key: jobKey(2), QueuedAt: t0.Add(11 * time.Hour)}}
	r.clock.Advance(12 * time.Hour)
	for i := 0; i < 2; i++ {
		if err := r.eng.SweepQueued(ctx, items); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(q.got, []int64{1}) {
		t.Fatalf("queued %v", q.got)
	}
	st := []StartupItem{{Key: jobKey(3), AssignedAt: r.clock.Now().Add(-119 * time.Second)}, {Key: jobKey(4), AssignedAt: r.clock.Now().Add(-2 * time.Minute)}}
	_ = r.eng.SweepStartup(ctx, st)
	_ = r.eng.SweepStartup(ctx, st)
	if !reflect.DeepEqual(s.got, []int64{4}) || !contains(r.audit.actions(), ActQueuedAlert) || !contains(r.audit.actions(), ActStartupExpired) {
		t.Fatalf("startup %v audit %v", s.got, r.audit.actions())
	}
}
