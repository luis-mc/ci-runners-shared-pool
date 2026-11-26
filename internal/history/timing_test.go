package history

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

var t0 = time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

// R5.9. A duration is never negative; an end before its start is unknown.
func TestNeverNegative(t *testing.T) {
	if s := Between(at(10), at(5)); s.Known || s.MS != 0 {
		t.Fatalf("end before start: %+v", s)
	}
	if s := Between(at(5), at(5)); !s.Known || s.MS != 0 {
		t.Fatalf("equal times are a known zero: %+v", s)
	}
}

// R5.9. A missing or zero time (0001-01-01) is unknown, never 0.
func TestZeroTimeIsUnknown(t *testing.T) {
	placeholder, err := time.Parse(time.RFC3339, "0001-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]Span{
		"zero start":        Between(time.Time{}, at(5)),
		"zero end":          Between(at(5), time.Time{}),
		"placeholder start": Between(placeholder, at(5)),
		"placeholder end":   Between(at(5), placeholder),
		"both zero":         Between(time.Time{}, time.Time{}),
	} {
		if s.Known {
			t.Errorf("%s: want unknown, got %+v", name, s)
		}
	}
	if _, ok := Unknown.Duration(); ok {
		t.Error("Unknown must report not known")
	}
}

// R5.9. A re-run counts from its own attempt start, not the first attempt.
func TestRerunUsesAttemptStart(t *testing.T) {
	created, rerunStart := at(0), at(3600)
	if got := AttemptStart(2, created, rerunStart); !got.Equal(rerunStart) {
		t.Fatalf("re-run start %v, want %v", got, rerunStart)
	}
	if got := AttemptStart(1, created, time.Time{}); !got.Equal(created) {
		t.Fatalf("first attempt falls back to created_at: %v", got)
	}
	if got := AttemptStart(2, created, time.Time{}); !Missing(got) {
		t.Fatalf("a re-run without its own start is unknown: %v", got)
	}
	d, ok := Between(AttemptStart(2, created, rerunStart), at(3660)).Duration()
	if !ok || d != time.Minute {
		t.Fatalf("re-run duration %v %v, want 1m", d, ok)
	}
}

// R5.8. Port of test_waterfall_times: GitHub wait and slot wait are split.
func TestWaitSplitGitHubAndSlot(t *testing.T) {
	j := jobsdata.Job{CreatedAt: at(0), StartedAt: at(30), QueuedAt: at(32), SlotAt: at(47), CompletedAt: at(150)}
	tm := Split(j)
	if tm.GitHubWait.MS != 30_000 || tm.SlotWait.MS != 15_000 || tm.Wait.MS != 45_000 {
		t.Fatalf("wait split %+v", tm)
	}
	// No slot times: the wait is GitHub's alone and the slot wait is unknown.
	tm = Split(jobsdata.Job{CreatedAt: at(0), StartedAt: at(30), CompletedAt: at(150)})
	if tm.SlotWait.Known || tm.Wait.MS != 30_000 || tm.Run.MS != 120_000 {
		t.Fatalf("no slot times %+v", tm)
	}
	if tm := Split(jobsdata.Job{}); tm.Wait.Known || tm.Run.Known {
		t.Fatalf("empty job is unknown: %+v", tm)
	}
}

// R5.8. The slot gate sits inside "Set up job": run time excludes it and wait includes it, so it is counted once.
func TestSlotGateCountedOnce(t *testing.T) {
	j := jobsdata.Job{CreatedAt: at(0), StartedAt: at(30), QueuedAt: at(32), SlotAt: at(47), CompletedAt: at(150)}
	tm := Split(j)
	if tm.Run.MS != 105_000 {
		t.Fatalf("run %d, want 120s minus the 15s gate", tm.Run.MS)
	}
	if total := tm.Wait.MS + tm.Run.MS; total != 150_000 {
		t.Fatalf("wait + run = %d ms, want created to completed 150000", total)
	}
	// A gate longer than the run never makes the run negative.
	j.SlotAt = at(500)
	if tm := Split(j); tm.Run.MS < 0 || !tm.Run.Known {
		t.Fatalf("run %+v", tm.Run)
	}
}

// R5.10. Port of test_failed_step_and_conclusion: the five results and the failure count.
func TestResultMapping(t *testing.T) {
	cases := map[string]jobsdata.Result{
		"success": jobsdata.Success, "failure": jobsdata.Failure, "startup_failure": jobsdata.Failure,
		"cancelled": jobsdata.Cancelled, "timed_out": jobsdata.TimedOut, "lost": jobsdata.Lost,
	}
	for in, want := range cases {
		if got, ok := MapResult(in); !ok || got != want {
			t.Errorf("%q -> %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "skipped", "neutral", "bogus"} {
		if _, ok := MapResult(in); ok {
			t.Errorf("%q must have no result", in)
		}
	}
	got := Count([]jobsdata.Result{jobsdata.Success, jobsdata.Failure, jobsdata.TimedOut, jobsdata.Cancelled, jobsdata.Lost})
	if got != (Totals{Success: 1, Failed: 2, Cancelled: 1, Lost: 1}) {
		t.Fatalf("totals %+v: timed out counts as failure", got)
	}
	r, _ := MapResult("startup_failure")
	if !CountsAsFailure(r) {
		t.Fatal("startup_failure must count as failure")
	}
}
