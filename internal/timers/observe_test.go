package timers

import (
	"reflect"
	"testing"
	"time"
)

// Observe mode: a job whose 30 min ran out shows the countdown and the grace,
// then reports what it would do and sends no stop.
func TestObserveJobWouldStop(t *testing.T) {
	r := newRigMode(false)
	k := r.start(1)
	r.tick(30 * time.Minute)
	v, _ := r.eng.Job(ctx, k)
	if v.State != StateGrace || v.Text != "Job timer ran out at 30:00; would stop in 30 s" || !v.CanStopNow {
		t.Fatalf("grace: %+v", v)
	}
	r.tick(Grace)
	r.tick(5 * time.Minute) // no second report
	if len(r.agent.calls) != 0 || len(r.gh.calls) != 0 {
		t.Fatalf("sent: agent %v github %v", r.agent.calls, r.gh.calls)
	}
	want := []string{ActJobGrace, ActJobWouldStop}
	if got := r.audit.actions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("audit %v", got)
	}
	const note = "would stop at 30:00 (timers in observe mode)"
	if got := r.audit.rows[1].Text; got != note {
		t.Fatalf("audit text %q", got)
	}
	v, _ = r.eng.Job(ctx, k)
	if v.Note != note || v.State != StateRunning {
		t.Fatalf("view %+v", v)
	}
	if r.store.jobs[k].Note != note {
		t.Fatalf("stored %+v", r.store.jobs[k])
	}
}

// Observe mode: the run's 45 min reports would_cancel and cancels nothing.
func TestObserveRunWouldCancel(t *testing.T) {
	r := newRigMode(false)
	r.start(1)
	r.tick(45 * time.Minute)
	r.tick(Grace)
	r.tick(10 * time.Minute)
	if len(r.agent.calls) != 0 || len(r.gh.calls) != 0 {
		t.Fatalf("sent: agent %v github %v", r.agent.calls, r.gh.calls)
	}
	if !contains(r.audit.actions(), ActRunWouldCancel) || contains(r.audit.actions(), ActRunCancel) {
		t.Fatalf("audit %v", r.audit.actions())
	}
	rv, _ := r.eng.Run(ctx, jobKey(1).Run())
	if rv.Note != "would cancel at 45:00 (timers in observe mode)" {
		t.Fatalf("run %+v", rv)
	}
}

// Enforce mode stops the job and cancels the run as before.
func TestEnforceStopsAndCancels(t *testing.T) {
	r := newRigMode(true)
	r.start(1)
	r.tick(30*time.Minute + Grace)
	if len(r.agent.calls) != 1 || len(r.gh.calls) != 0 {
		t.Fatalf("stop: agent %v github %v", r.agent.calls, r.gh.calls)
	}
	r2 := newRigMode(true)
	r2.start(1)
	r2.tick(45*time.Minute + Grace)
	if len(r2.gh.calls) != 1 {
		t.Fatalf("cancel: %v", r2.gh.calls)
	}
	if contains(r2.audit.actions(), ActJobWouldStop) || contains(r2.audit.actions(), ActRunWouldCancel) {
		t.Fatalf("audit %v", r2.audit.actions())
	}
}

// The buttons a user presses act in observe mode as well.
func TestObserveManualButtonsAct(t *testing.T) {
	r := newRigMode(false)
	k := r.start(1)
	r.tick(30 * time.Minute)
	if err := r.eng.StopNow(ctx, k, "luis"); err != nil {
		t.Fatal(err)
	}
	if len(r.agent.calls) != 1 {
		t.Fatalf("stop now sent %v", r.agent.calls)
	}
	r2 := newRigMode(false)
	r2.start(1)
	r2.tick(45 * time.Minute)
	if err := r2.eng.CancelNow(ctx, jobKey(1).Run(), "luis"); err != nil {
		t.Fatal(err)
	}
	if len(r2.gh.calls) != 1 {
		t.Fatalf("cancel now sent %v", r2.gh.calls)
	}
	r3 := newRigMode(false)
	k3 := r3.start(1)
	r3.tick(30 * time.Minute)
	if err := r3.eng.Extend(ctx, k3, "luis"); err != nil {
		t.Fatal(err)
	}
	if v, _ := r3.eng.Job(ctx, k3); v.State != StateRunning || !v.Extended {
		t.Fatalf("extend %+v", v)
	}
}

// A controller restart after the report does not report again.
func TestObserveReportedOnceAcrossRestart(t *testing.T) {
	r := newRigMode(false)
	r.start(1)
	r.tick(30*time.Minute + Grace)
	n := len(r.audit.rows)
	e2 := New(Config{Settings: func() Settings { return Defaults() }, Clock: r.clock, Agent: r.agent, GitHub: r.gh, Audit: r.audit, Store: r.store})
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(time.Minute)
	if err := e2.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.audit.rows) != n {
		t.Fatalf("audit grew: %v", r.audit.actions())
	}
}

// Upgrade: saved settings from before the enforce setting come back with
// enforce off; a new install has it off too; the owner can switch it on.
func TestEnforceDefaultsToObserve(t *testing.T) {
	if Defaults().Enforce {
		t.Fatal("new install enforces")
	}
	old := `{"job_minutes":20,"run_minutes":40,"hold_minutes":2,"timeout_minutes":60,"queued_minutes":720,"startup_minutes":2}`
	s := Parse(old)
	if s.Enforce || s.JobMin != 20 {
		t.Fatalf("upgrade %+v", s)
	}
	if Parse("").Enforce || Parse("not json").Enforce {
		t.Fatal("empty or bad settings enforce")
	}
	on := true
	next, problems := Check(s, Input{Enforce: &on})
	if len(problems) != 0 || !next.Enforce || !Parse(next.Encode()).Enforce {
		t.Fatalf("switch on: %+v %v", next, problems)
	}
}
