package assign

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
)

var ctx = context.Background()

func has(ls []string, s string) bool {
	for _, l := range ls {
		if l == s {
			return true
		}
	}
	return false
}

func TestRoomMakesOneRunnerWithTheJobLabel(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	if len(r.rn.started) != 1 {
		t.Fatalf("runners made: %d", len(r.rn.started))
	}
	s := r.rn.started[0]
	if !has(s.Labels, e2eLabel) || s.OS != "linux" || s.MachineID != "m1" {
		t.Fatalf("runner request: %+v", s)
	}
	l := r.lease(1)
	if l.State != Starting || l.RunnerRecord == "" || l.GitHubRunner == 0 || l.JobLabel != e2eLabel {
		t.Fatalf("lease: %+v", l)
	}
}

func TestNoRoomNothingHappensAndReasonSaysWhy(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.req["big"] = admit.Req{CPUs: 6, MemoryGiB: 4}
	r.env.req["held"] = admit.Req{CPUs: 3, MemoryGiB: 2}
	must(t, r.c.Queued(ctx, job(1, "held")))
	must(t, r.c.Queued(ctx, job(2, "big")))
	if len(r.rn.started) != 1 {
		t.Fatalf("runners made: %d", len(r.rn.started))
	}
	l := r.lease(2)
	if l.State != Waiting || l.Reason != "waiting for room on M1: needs 6 CPU, 5 free" || l.ReasonCode != CodePool {
		t.Fatalf("lease: %+v", l)
	}
}

func TestRepeatedWebhookAndRestartMakeNoSecondRunner(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	// A restart: a new controller over the same store sees the same lease.
	c2 := r.controller()
	must(t, c2.Queued(ctx, job(1, "e2e")))
	must(t, c2.Replan(ctx))
	if len(r.rn.started) != 1 {
		t.Fatalf("runners made: %d", len(r.rn.started))
	}
}

func TestBeginIsTheOnlyWayToStart(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	ok, err := r.st.Begin(ctx, 1, "m1", "other", r.clk.now())
	if err != nil || ok {
		t.Fatalf("a second begin must lose: ok=%v err=%v", ok, err)
	}
}

func TestInProgressMovesLeaseToTheRealJob(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.req["small"] = admit.Req{CPUs: 1, MemoryGiB: 1}
	r.env.req["same"] = admit.Req{CPUs: 2, MemoryGiB: 2}
	must(t, r.c.Queued(ctx, job(1, "same")))
	run := r.lease(1).RunnerName
	// Job 2 of the same label is queued, then job 2 (not 1) takes the runner.
	must(t, r.c.Queued(ctx, job(2, "same")))
	if len(r.rn.started) != 2 {
		t.Fatalf("each queued job gets a runner: %d", len(r.rn.started))
	}
	j2 := job(2, "same")
	j2.Status, j2.RunnerName = "in_progress", run
	must(t, r.c.InProgress(ctx, j2))
	l2 := r.lease(2)
	if l2.State != Running || l2.RunnerName != run {
		t.Fatalf("job 2: %+v", l2)
	}
	// Job 1 holds the runner job 2 made, so no runner is lost.
	l1 := r.lease(1)
	if l1.State != Starting || l1.RunnerName == run || l1.RunnerRecord == "" {
		t.Fatalf("job 1: %+v", l1)
	}
}

func TestInProgressOnItsOwnRunner(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	if l := r.lease(1); l.State != Running || l.Reason != "" {
		t.Fatalf("lease: %+v", l)
	}
}

func TestCompletedFreesRoomAndPlansTheQueue(t *testing.T) {
	r := newRig(t, m1(4, 8))
	r.env.req["a"] = admit.Req{CPUs: 4, MemoryGiB: 2}
	r.env.req["b"] = admit.Req{CPUs: 4, MemoryGiB: 2}
	must(t, r.c.Queued(ctx, job(1, "a")))
	must(t, r.c.Queued(ctx, job(2, "b")))
	if r.lease(2).State != Waiting {
		t.Fatal("job 2 must wait")
	}
	j := job(1, "a")
	j.RunnerName = r.lease(1).RunnerName
	must(t, r.c.InProgress(ctx, j))
	j.Status = "completed"
	must(t, r.c.Completed(ctx, j))
	if len(r.rn.finished) != 1 || r.lease(1).State != Done {
		t.Fatalf("cleanup: %v %+v", r.rn.finished, r.lease(1))
	}
	if r.lease(2).State != Starting {
		t.Fatalf("job 2 must start: %+v", r.lease(2))
	}
}

func TestTakenElsewhereHoldsNoRoom(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.machines[0].LoadOK = false
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	j := job(1, "e2e")
	j.RunnerName = "someone-else"
	must(t, r.c.InProgress(ctx, j))
	if l := r.lease(1); l.State != Gone {
		t.Fatalf("lease: %+v", l)
	}
}

func TestStartFailureBacksOff(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.rn.failNext = errBoom
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	l := r.lease(1)
	if l.State != Waiting || l.Fails != 1 || l.ReasonCode != CodeBackoff || !strings.Contains(l.Reason, "boom") {
		t.Fatalf("lease: %+v", l)
	}
	must(t, r.c.Replan(ctx))
	if len(r.rn.started) != 0 {
		t.Fatal("no retry before the wait is over")
	}
	r.clk.advance(31 * time.Second)
	must(t, r.c.Replan(ctx))
	if len(r.rn.started) != 1 || r.lease(1).State != Starting {
		t.Fatalf("retry: %+v", r.lease(1))
	}
}

func TestLoadOverBudgetWaits(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.machines[0].LoadOK, r.env.machines[0].LoadText = false, "memory is at 91%, the budget is 80%"
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	if l := r.lease(1); l.ReasonCode != CodeLoad || !strings.Contains(l.Reason, "memory is at 91%") || len(r.rn.started) != 0 {
		t.Fatalf("lease: %+v", l)
	}
}

func TestRepositoryWithPoolOffIsIgnored(t *testing.T) {
	r := newRig(t, m1(8, 16))
	r.env.noRepo = true
	must(t, r.c.Queued(ctx, job(1, "e2e")))
	if _, ok, _ := r.st.Get(ctx, 1); ok || len(r.rn.started) != 0 {
		t.Fatal("a repository with the pool off gets no lease")
	}
}

func TestEachOSStartsItsOwnRunner(t *testing.T) {
	win := Machine{ID: "w1", Name: "W1", OS: "windows", Arch: "x64", Pool: admit.Pool{CPUs: 8, MemoryGiB: 16}, Labels: []string{"self-hosted", "windows", "x64"}, LoadOK: true, Online: true}
	mac := Machine{ID: "a1", Name: "A1", OS: "macos", Arch: "arm64", Pool: admit.Pool{CPUs: 8, MemoryGiB: 16}, Labels: []string{"self-hosted", "macos", "arm64"}, LoadOK: true, Online: true}
	r := newRig(t, m1(8, 16), win, mac)
	must(t, r.c.Queued(ctx, job(1, "a", "self-hosted", "windows", "x64", "crsp-a-0001")))
	must(t, r.c.Queued(ctx, job(2, "b", "self-hosted", "macos", "arm64", "crsp-b-0002")))
	must(t, r.c.Queued(ctx, job(3, "c", "self-hosted", "linux", "x64", "crsp-c-0003")))
	got := map[string]string{}
	for _, s := range r.rn.started {
		got[s.OS] = s.MachineID
	}
	if got["windows"] != "w1" || got["macos"] != "a1" || got["linux"] != "m1" {
		t.Fatalf("runners: %v", got)
	}
}

func TestIsolationAndStartupPerOS(t *testing.T) {
	if runners.StartupAllowance("macos") < 45*time.Second || runners.StartupAllowance("macos") > 60*time.Second {
		t.Fatal("a macOS start-up is 45 to 60 s")
	}
	if runners.IsolationFor("macos") != "VM" || runners.IsolationFor("linux") != "container" || runners.IsolationFor("windows") != "persistent" {
		t.Fatal("isolation per OS")
	}
}

func TestNoLabelAskedSetsNoRoutingLabel(t *testing.T) {
	r := newRig(t, m1(8, 16))
	must(t, r.c.Queued(ctx, job(1, "x", "self-hosted", "linux")))
	if l := r.lease(1); l.JobLabel != "" || l.State != Starting {
		t.Fatalf("lease: %+v", l)
	}
	var _ = github.Job{}
}
