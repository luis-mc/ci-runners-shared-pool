package scheduler_test

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// R5.5. Every reason on the fixed list has its own words, and the gate's figures stay in them.
func TestWaitReasons(t *testing.T) {
	cases := []struct {
		r    scheduler.WaitReason
		id   string
		text string
		want string
	}{
		{scheduler.WaitFreeSlot, "free_slot", "2 ahead in the queue", "No free slot: 2 ahead in the queue."},
		{scheduler.WaitLoadOverBudget, "load_over_budget", "memory 91% > 85%", "Over the load budget: memory 91% > 85%."},
		{scheduler.WaitNoRunnerLabels, "no_runner_with_labels", "", "No machine has this OS and these labels."},
		{scheduler.WaitRepoParallelLimit, "repository_parallel_limit", "2 of 2 running", "The repository is at its parallel limit: 2 of 2 running."},
		{scheduler.WaitMachineDrained, "machine_drained", "", "The machine is drained."},
		{scheduler.WaitLoadNotMeasured, "load_not_measured", "", "Load not measured yet."},
		{scheduler.WaitUnknown, "unknown", "", "Waiting. The reason is not known yet."},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if string(c.r) != c.id {
			t.Errorf("reason id %q, want %q", c.r, c.id)
		}
		got := scheduler.WaitWords(c.r, c.text)
		if len(got) < len(c.want) || got[:len(c.want)] != c.want {
			t.Errorf("%s: got %q, want prefix %q", c.r, got, c.want)
		}
		if seen[got] {
			t.Errorf("%s: words %q repeat another reason", c.r, got)
		}
		seen[got] = true
	}
}

// R5.5. "No runner with the labels" is raised once the job has waited 2 minutes and no online runner has all
// its labels (ported from warn_queued_jobs in ci_watch.py).
func TestNoRunnerWithLabelsAfterThreshold(t *testing.T) {
	q := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	job := []string{"self-hosted", "linux", "gpu"}
	runners := [][]string{{"self-hosted", "linux"}, {"self-hosted", "linux", "x64"}}
	at := func(d time.Duration) time.Time { return q.Add(d) }

	if scheduler.NoRunnerLabelsAfter != 2*time.Minute {
		t.Fatalf("default threshold %v, want 2m", scheduler.NoRunnerLabelsAfter)
	}
	if scheduler.NoRunnerWithLabels(q, at(119*time.Second), 0, job, runners) {
		t.Error("raised before 2 minutes")
	}
	if !scheduler.NoRunnerWithLabels(q, at(2*time.Minute), 0, job, runners) {
		t.Error("not raised at 2 minutes")
	}
	if !scheduler.NoRunnerWithLabels(q, at(time.Minute), time.Minute, job, runners) {
		t.Error("the setting must change the threshold")
	}
	if scheduler.NoRunnerWithLabels(q, at(10*time.Minute), 0, job, append(runners, []string{"Self-Hosted", "LINUX", "GPU"})) {
		t.Error("an online runner with every label (any case) must clear it")
	}
	if !scheduler.NoRunnerWithLabels(q, at(10*time.Minute), 0, job, nil) {
		t.Error("no online runner at all must raise it")
	}
	if scheduler.NoRunnerWithLabels(q, at(10*time.Minute), 0, nil, nil) {
		t.Error("a job with no labels never raises it")
	}
	if scheduler.NoRunnerWithLabels(time.Time{}, at(10*time.Minute), 0, job, nil) {
		t.Error("an unknown queue time never raises it")
	}
}
