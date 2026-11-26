package history

// Timing rules of the history (observability R5.8, R5.9, R5.10). Every function is pure: it takes the times
// as arguments and never reads a clock.
//
// Ported from dev-standards claude/plugins/delivery/scripts/ci_runs.py (build_finished, the duration helpers)
// and claude/plugins/delivery/templates/shared/scripts/delivery/ci_watch.py (the zero-time and re-run rules),
// and from their tests in claude/plugins/delivery/tests/test_ci_runs.py (test_waterfall_times,
// test_failed_step_and_conclusion).

import (
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsdata"
)

// Span is a duration that may be unknown. An unknown span is never shown as 0 (R5.9).
type Span struct {
	MS    int64
	Known bool
}

// Unknown is the span of a missing or impossible pair of times.
var Unknown = Span{}

// Dur is a known span of d.
func Dur(d time.Duration) Span { return Span{MS: d.Milliseconds(), Known: true} }

// Duration returns the span, or false when it is unknown.
func (s Span) Duration() (time.Duration, bool) {
	return time.Duration(s.MS) * time.Millisecond, s.Known
}

// Missing reports a time that is not set: the zero value, or GitHub's 0001-01-01 placeholder (R5.9).
func Missing(t time.Time) bool { return t.IsZero() || t.Year() <= 1 }

// Between is the span from start to end. A missing time or an end before the start is unknown, never 0 and
// never negative (R5.9).
func Between(start, end time.Time) Span {
	if Missing(start) || Missing(end) || end.Before(start) {
		return Unknown
	}
	return Dur(end.Sub(start))
}

// AttemptStart is where a run attempt's time starts. A re-run counts from its own attempt start (GitHub's
// run_started_at, reset on each attempt), never from the first attempt's created_at. Without an attempt
// start, the first attempt falls back to created_at; a re-run without one is unknown (R5.9).
func AttemptStart(attempt int, createdAt, attemptStartedAt time.Time) time.Time {
	if !Missing(attemptStartedAt) {
		return attemptStartedAt
	}
	if attempt <= 1 {
		return createdAt
	}
	return time.Time{}
}

// Timing is the wait and run time of one job attempt.
type Timing struct {
	// GitHubWait is created_at to started_at: GitHub's own wait for a runner.
	GitHubWait Span
	// SlotWait is queued_at to slot_at: the wait in crsp's slot queue. It happens inside "Set up job".
	SlotWait Span
	// Wait is GitHubWait plus SlotWait. Unknown when GitHubWait is unknown.
	Wait Span
	// Run is started_at to completed_at minus SlotWait, so the gate is counted once (R5.8).
	Run Span
}

// Split cuts a job attempt's times into GitHub wait, slot wait and run time (R5.8). Wait and run add up to
// created_at to completed_at: the slot gate sits inside the started-to-completed span, so it is added to the
// wait and taken out of the run. A missing slot time leaves slot wait unknown and the run uncut.
func Split(j jobsdata.Job) Timing {
	var t Timing
	t.GitHubWait = Between(j.CreatedAt, j.StartedAt)
	t.SlotWait = Between(j.QueuedAt, j.SlotAt)
	run := Between(j.StartedAt, j.CompletedAt)
	if run.Known && t.SlotWait.Known {
		run.MS -= t.SlotWait.MS
		if run.MS < 0 {
			run.MS = 0
		}
	}
	t.Run = run
	switch {
	case !t.GitHubWait.Known:
		t.Wait = Unknown
	case t.SlotWait.Known:
		t.Wait = Span{MS: t.GitHubWait.MS + t.SlotWait.MS, Known: true}
	default:
		t.Wait = t.GitHubWait
	}
	return t
}

// MapResult maps a GitHub conclusion to one of the five results (R5.10). startup_failure is a failure;
// timed_out stays its own result. An open job ("") or a conclusion with no result (skipped, neutral) gives
// false.
func MapResult(conclusion string) (jobsdata.Result, bool) {
	switch conclusion {
	case "success":
		return jobsdata.Success, true
	case "failure", "startup_failure":
		return jobsdata.Failure, true
	case "cancelled":
		return jobsdata.Cancelled, true
	case "timed_out":
		return jobsdata.TimedOut, true
	case "lost":
		return jobsdata.Lost, true
	}
	return "", false
}

// CountsAsFailure reports a result that counts as a failure in totals: failure and timed out (R5.10).
func CountsAsFailure(r jobsdata.Result) bool {
	return r == jobsdata.Failure || r == jobsdata.TimedOut
}

// Totals are the counts of a list of results.
type Totals struct {
	Success, Failed, Cancelled, Lost int
}

// Count adds results into totals. Failed holds failure, startup_failure and timed out (R5.10).
func Count(results []jobsdata.Result) Totals {
	var t Totals
	for _, r := range results {
		switch {
		case r == jobsdata.Success:
			t.Success++
		case CountsAsFailure(r):
			t.Failed++
		case r == jobsdata.Cancelled:
			t.Cancelled++
		case r == jobsdata.Lost:
			t.Lost++
		}
	}
	return t
}
