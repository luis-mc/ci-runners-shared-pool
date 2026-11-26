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
