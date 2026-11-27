package listenergate

import (
	"context"
	"testing"
	"time"
)

func TestWeeklyWakeStartsThenStopsAfterAMinute(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	if err := r.g.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.svc.calls) != 1 || r.svc.calls[0] != "start m1 svc-r1 maint=true" {
		t.Fatalf("wake: %v", r.svc.calls)
	}
	if len(r.lb.calls) != 0 {
		t.Errorf("a wake carries no label: %v", r.lb.calls)
	}
	r.clk.t = r.clk.t.Add(WakeHold - time.Second)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != 1 {
		t.Fatalf("stopped too early: %v", r.svc.calls)
	}
	r.clk.t = r.clk.t.Add(time.Second)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != 2 || r.svc.calls[1] != "stop m1 svc-r1 assigned=false" {
		t.Fatalf("stop: %v", r.svc.calls)
	}
	if r.aud.actions() != ActionWake+","+ActionStop {
		t.Errorf("audit: %s", r.aud.actions())
	}
}

func TestNextWakeIsAWeekAfterTheLast(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_ = r.g.Sweep(ctx)
	r.clk.t = r.clk.t.Add(WakeHold)
	_ = r.g.Sweep(ctx)
	n := len(r.svc.calls)
	r.clk.t = r.clk.t.Add(WakeEvery - WakeHold - time.Minute)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != n {
		t.Fatalf("woke early: %v", r.svc.calls)
	}
	st, _ := r.g.Status(ctx)
	if st[0].NextWake.Sub(st[0].LastWake) != WakeEvery {
		t.Errorf("next wake %v last %v", st[0].NextWake, st[0].LastWake)
	}
	r.clk.t = r.clk.t.Add(2 * time.Minute)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != n+1 {
		t.Errorf("did not wake when due: %v", r.svc.calls)
	}
}

func TestNoWakeWhileAJobHoldsTheListener(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_, _ = r.g.Admit(ctx, Job{JobID: 1, JobLabel: "crsp-a-0001", Runner: host})
	n := len(r.svc.calls)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != n {
		t.Errorf("the sweep touched a runner with a job: %v", r.svc.calls)
	}
}

func TestSweepParksAnIdleListenerButNeverABusyOne(t *testing.T) {
	r := newRig(true)
	ctx := context.Background()
	_ = r.g.Sweep(ctx) // first wake
	r.clk.t = r.clk.t.Add(WakeHold)
	_ = r.g.Sweep(ctx) // wake ends
	r.svc.calls = nil
	r.svc.state = ListenerBusy
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != 0 {
		t.Fatalf("stopped a busy listener: %v", r.svc.calls)
	}
	r.svc.state = ListenerOnline
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != 1 || r.svc.calls[0] != "stop m1 svc-r1 assigned=false" {
		t.Errorf("idle listener not parked: %v", r.svc.calls)
	}
}

func TestObserveSweepLeavesListenersAlone(t *testing.T) {
	r := newRig(false)
	ctx := context.Background()
	r.svc.state = ListenerOnline
	_ = r.g.Sweep(ctx)
	r.clk.t = r.clk.t.Add(WakeHold)
	_ = r.g.Sweep(ctx)
	r.clk.t = r.clk.t.Add(time.Hour)
	_ = r.g.Sweep(ctx)
	if len(r.svc.calls) != 0 || len(r.lb.calls) != 0 {
		t.Fatalf("observe mode acted: %v %v", r.svc.calls, r.lb.calls)
	}
	if r.aud.actions() != ActionWouldStart+","+ActionWouldStop {
		t.Errorf("audit: %s", r.aud.actions())
	}
}

func TestFailedWakeDoesNotStopOtherRunners(t *testing.T) {
	two := Runner{ID: "r2", Name: "win-1", MachineID: "m2", ServiceRef: "svc-r2"}
	r := newRig(true, host, two)
	r.svc.failStart = context.DeadlineExceeded
	err := r.g.Sweep(context.Background())
	if err == nil {
		t.Fatal("a failed wake was not reported")
	}
	if len(r.svc.calls) != 2 {
		t.Errorf("the second runner was skipped: %v", r.svc.calls)
	}
}
