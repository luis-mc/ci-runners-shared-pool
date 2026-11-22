package assign

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

func TestUnusedRunnerDeletedAfterStartupPlusTwoMinutes(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	r.clk.advance(20*time.Second + 2*time.Minute - time.Second)
	must(t, r.c.Sweep(ctx))
	if len(r.rn.removed) != 0 {
		t.Fatal("deleted too early")
	}
	r.clk.advance(time.Second)
	must(t, r.c.Sweep(ctx))
	// The runner is deleted and the job (still queued) gets a new one in the same sweep.
	if len(r.rn.removed) != 1 || r.rn.removed[0] != "r_1" || len(r.rn.started) != 2 {
		t.Fatalf("removed %v, started %d", r.rn.removed, len(r.rn.started))
	}
}

func TestMacRunnerCountsItsVMBoot(t *testing.T) {
	mac := Machine{ID: "a1", Name: "A1", OS: "macos", Arch: "arm64", Pool: admit.Pool{CPUs: 8, MemoryGiB: 16}, Labels: []string{"self-hosted", "macos", "arm64"}, LoadOK: true, Online: true}
	r := newRig(t, mac)
	must(t, r.c.Queued(ctx, job(1, "b", "self-hosted", "macos", "arm64", "crsp-b-0002")))
	r.clk.advance(60*time.Second + 2*time.Minute - time.Second)
	must(t, r.c.Sweep(ctx))
	if len(r.rn.removed) != 0 {
		t.Fatal("a macOS runner needs 60 s to boot before the 2 minutes count")
	}
	r.clk.advance(time.Second)
	must(t, r.c.Sweep(ctx))
	if len(r.rn.removed) != 1 {
		t.Fatal("deleted after boot plus 2 minutes")
	}
}

func TestCancelledJobsRunnerIsDeleted(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	if r.lease(1).State != Gone {
		t.Fatal("cancelled before it ran")
	}
	r.clk.advance(3 * time.Minute)
	must(t, r.c.Sweep(ctx))
	if len(r.rn.removed) != 1 {
		t.Fatalf("removed %v", r.rn.removed)
	}
}

func TestBusyUnusedRunnerIsLeftAlone(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	r.rn.busy["r_1"] = true
	r.clk.advance(3 * time.Minute)
	must(t, r.c.Sweep(ctx))
	if r.lease(1).State != Starting || len(r.rn.removed) != 0 {
		t.Fatalf("lease: %+v", r.lease(1))
	}
}

func TestAlertAt12HoursAndCancelAt24(t *testing.T) {
	r := newRig(t, m1(2, 4))
	r.env.machines[0].LoadOK, r.env.machines[0].LoadText = false, "load not measured"
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	r.clk.advance(12 * time.Hour)
	must(t, r.c.Sweep(ctx))
	must(t, r.c.Sweep(ctx))
	if len(r.nt.texts) != 1 || !strings.Contains(r.nt.texts[0], "12 hours") {
		t.Fatalf("alerts: %v", r.nt.texts)
	}
	lw, _ := r.c.LongWaiting(ctx)
	if len(lw) != 1 {
		t.Fatal("long waiting list")
	}
	r.clk.advance(12 * time.Hour)
	must(t, r.c.Sweep(ctx))
	if r.lease(1).State != Gone {
		t.Fatalf("GitHub cancels at 24 h: %+v", r.lease(1))
	}
}

func TestNoMachineCanEverServeWarnsAtOnce(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.req["huge"] = admit.Req{CPUs: 64, MemoryGiB: 8}
	must(t, r.c.Queued(ctx, job(1, "huge")))
	must(t, r.c.Queued(ctx, job(2, "gpu", "self-hosted", "linux", "gpu", "crsp-g-0001")))
	must(t, r.c.Replan(ctx)) // a second plan does not warn again
	if len(r.nt.texts) != 2 {
		t.Fatalf("warnings: %v", r.nt.texts)
	}
	un, _ := r.c.Unserved(ctx)
	if len(un) != 2 || r.lease(1).ReasonCode != CodeNever || r.lease(2).ReasonCode != CodeNoMachine {
		t.Fatalf("unserved: %d %+v", len(un), r.lease(2))
	}
}

func TestReservationAfterThreePasses(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.req["held"] = admit.Req{CPUs: 5, MemoryGiB: 1}
	r.env.req["big"] = admit.Req{CPUs: 6, MemoryGiB: 1}
	r.env.req["tiny"] = admit.Req{CPUs: 1, MemoryGiB: 1}
	must(t, r.c.Queued(ctx, job(1, "held")))
	must(t, r.c.Queued(ctx, job(2, "big")))
	for i := int64(10); i < 13; i++ {
		r.clk.advance(time.Second)
		must(t, r.c.Queued(ctx, job(i, "tiny")))
	}
	// 3 CPUs are free beside the held job: three tiny jobs pass the big one,
	// which then becomes the machine's reserved job.
	if l := r.lease(2); l.Passes != 3 || l.ReservedSince.IsZero() || l.MachineID != "m1" {
		t.Fatalf("passes: %+v", l)
	}
}

func TestReservedJobHoldsBackLaterJobs(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.req["held"] = admit.Req{CPUs: 5, MemoryGiB: 1}
	r.env.req["big"] = admit.Req{CPUs: 6, MemoryGiB: 1}
	must(t, r.c.Queued(ctx, job(1, "held")))
	must(t, r.c.Queued(ctx, job(2, "big")))
	r.clk.advance(11 * time.Minute) // waited 10 minutes: becomes the reserved job
	must(t, r.c.Replan(ctx))
	if l := r.lease(2); l.ReservedSince.IsZero() || l.MachineID != "m1" {
		t.Fatalf("not reserved: %+v", l)
	}
	must(t, r.c.Queued(ctx, job(3, "tiny")))
	if l := r.lease(3); l.State != Waiting || l.ReasonCode != CodeReserved {
		t.Fatalf("the reserved job keeps its room: %+v", l)
	}
}

func TestWarmRunnerLifecycle(t *testing.T) {
	r := newRig(t, m1(4, 8))
	r.env.req["e2e"] = admit.Req{CPUs: 2, MemoryGiB: 2}
	// A job of the set ran: the set is busy.
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	ws, _ := r.st.ListWarm(ctx)
	if len(ws) != 1 || ws[0].Req.CPUs != 2 {
		t.Fatalf("one ready runner for the busy set: %+v", ws)
	}
	// It is used by the next job of its set, and replaced after.
	n := len(r.rn.started)
	must(t, r.c.Queued(ctx, job(2, "e2e")))
	if len(r.rn.started) != n+1 {
		t.Fatalf("job 2 used the warm runner, the replacement is new: %d -> %d", n, len(r.rn.started))
	}
	if r.lease(2).RunnerName != ws[0].RunnerName {
		t.Fatalf("job 2 must hold the warm runner: %+v", r.lease(2))
	}
	nw, _ := r.st.ListWarm(ctx)
	if len(nw) != 1 || nw[0].ID == ws[0].ID {
		t.Fatalf("replacement: %+v", nw)
	}
}

func TestWarmRunnerReleasedFirstForARealJob(t *testing.T) {
	r := newRig(t, m1(4, 8))
	r.env.req["e2e"] = admit.Req{CPUs: 2, MemoryGiB: 2}
	r.env.req["lint"] = admit.Req{CPUs: 4, MemoryGiB: 2}
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	ws, _ := r.st.ListWarm(ctx)
	if len(ws) != 1 {
		t.Fatal("warm runner expected")
	}
	must(t, r.c.Queued(ctx, job(3, "lint", "self-hosted", "linux", "x64", "crsp-lint-0002")))
	if r.lease(3).State != Starting {
		t.Fatalf("the real job must start: %+v", r.lease(3))
	}
	if len(r.rn.removed) != 1 {
		t.Fatalf("the warm runner is released: %v", r.rn.removed)
	}
}

func TestWarmRunnerNeverMadeWhileAJobWaits(t *testing.T) {
	r := newRig(t, m1(4, 8))
	r.env.req["e2e"] = admit.Req{CPUs: 4, MemoryGiB: 2}
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	must(t, r.c.Queued(ctx, job(2, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	ws, _ := r.st.ListWarm(ctx)
	if len(ws) != 0 {
		t.Fatalf("a waiting job keeps the room: %+v", ws)
	}
}

func TestIdleSetLosesItsWarmRunnerAfterAnHour(t *testing.T) {
	r := newRig(t, m1(4, 8))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	r.clk.advance(BusyWindow + time.Second)
	must(t, r.c.Sweep(ctx))
	ws, _ := r.st.ListWarm(ctx)
	if len(ws) != 0 {
		t.Fatalf("warm: %+v", ws)
	}
}

func TestWarmRunnerThatTookAJobBecomesItsLease(t *testing.T) {
	r := newRig(t, m1(4, 8))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	ws, _ := r.st.ListWarm(ctx)
	j2 := job(5, "e2e")
	j2.Status, j2.RunnerName = "in_progress", ws[0].RunnerName
	must(t, r.c.InProgress(ctx, j2))
	if l := r.lease(5); l.State != Running || l.RunnerRecord != ws[0].RunnerRecord {
		t.Fatalf("lease: %+v", l)
	}
}

type fakeSource struct {
	targets []github.PollTarget
	jobs    map[string][]github.Job
	err     map[string]error
}

func (f *fakeSource) Targets(context.Context) ([]github.PollTarget, error) { return f.targets, nil }
func (f *fakeSource) OpenJobs(_ context.Context, t github.PollTarget) ([]github.Job, error) {
	k := t.Owner + "/" + t.Repo
	return f.jobs[k], f.err[k]
}

func TestPollerFindsALostWebhookAndSeesJobsVanish(t *testing.T) {
	r := newRig(t, m1(8, 16))
	src := &fakeSource{targets: []github.PollTarget{{Owner: "o", Repo: "r"}}, jobs: map[string][]github.Job{repoName: {job(1, "e2e")}}}
	p := &Poller{C: r.c, Src: src}
	must(t, p.Round(ctx))
	if r.lease(1).State != Starting {
		t.Fatal("a queued job the poller found gets its runner")
	}
	must(t, p.Round(ctx))
	if len(r.rn.started) != 1 {
		t.Fatal("a second round makes no second runner")
	}
	// The job starts on its runner; the poller sees it in progress.
	j := job(1, "e2e")
	j.Status, j.RunnerName = "in_progress", r.lease(1).RunnerName
	src.jobs[repoName] = []github.Job{j}
	must(t, p.Round(ctx))
	if r.lease(1).State != Running {
		t.Fatal("running")
	}
	// A failed read of the repository leaves the lease alone.
	src.err = map[string]error{repoName: errBoom}
	_ = p.Round(ctx)
	if r.lease(1).State != Running {
		t.Fatal("a failed list must not end a lease")
	}
	src.err = nil
	src.jobs[repoName] = nil
	must(t, p.Round(ctx))
	if l := r.lease(1); l.State != Done {
		t.Fatalf("a complete list without the job ends it: %+v", l)
	}
}

func TestPollerIntervalIsWithin30And60Seconds(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{0: MinEvery, 10 * time.Second: MinEvery, 45 * time.Second: 45 * time.Second, 5 * time.Minute: MaxEvery} {
		if got := (&Poller{Every: in}).every(); got != want {
			t.Fatalf("%v -> %v, want %v", in, got, want)
		}
	}
}

func TestPollerRunPollsAtStartThenEveryInterval(t *testing.T) {
	r := newRig(t, m1(8, 16))
	src := &fakeSource{targets: []github.PollTarget{{Owner: "o", Repo: "r"}}, jobs: map[string][]github.Job{repoName: {job(1, "e2e")}}}
	var waits []time.Duration
	runCtx, cancel := context.WithCancel(ctx)
	p := &Poller{C: r.c, Src: src, Every: 40 * time.Second, After: func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) == 2 {
			cancel()
			return context.Canceled
		}
		return nil
	}}
	p.Run(runCtx)
	if len(waits) != 2 || waits[0] != 40*time.Second || r.lease(1).State != Starting {
		t.Fatalf("waits %v", waits)
	}
}

func TestEventsDispatchByAction(t *testing.T) {
	r := newRig(t, m1(8, 16))
	e := NewEvents(r.c, 4)
	j := job(1, "e2e")
	must(t, e.Handle(ctx, github.JobEvent{Action: "queued", Job: j}))
	j.RunnerName = r.lease(1).RunnerName
	must(t, e.Handle(ctx, github.JobEvent{Action: "in_progress", Job: j}))
	if r.lease(1).State != Running {
		t.Fatal("in_progress")
	}
	must(t, e.Handle(ctx, github.JobEvent{Action: "completed", Job: j}))
	if r.lease(1).State != Done {
		t.Fatal("completed")
	}
	must(t, e.Handle(ctx, github.JobEvent{Action: "waiting", Job: j}))
	e.JobDetail(github.JobEvent{Action: "queued", Job: j})
	if len(e.queue) != 1 {
		t.Fatal("queued for the worker")
	}
}
