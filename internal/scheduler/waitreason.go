package scheduler

import (
	"strings"
	"time"
)

// NoRunnerLabelsAfter is how long a queued job waits before "no online runner with the labels" is raised
// (observability R5.5, a setting; this is its default).
const NoRunnerLabelsAfter = 2 * time.Minute

// NoRunnerWithLabels reports whether a queued job should carry the wait reason WaitNoRunnerLabels (R5.5): it
// has waited at least threshold since queuedAt, and no online runner carries all of its labels. A zero
// threshold means NoRunnerLabelsAfter. Labels are compared without regard to case, as GitHub does. A job with
// no labels never raises it. The function is pure: the caller passes the clock reading.
//
// Ported from warn_queued_jobs in dev-standards
// claude/plugins/delivery/templates/shared/scripts/delivery/ci_watch.py.
func NoRunnerWithLabels(queuedAt, now time.Time, threshold time.Duration, jobLabels []string, onlineRunners [][]string) bool {
	if threshold <= 0 {
		threshold = NoRunnerLabelsAfter
	}
	if len(jobLabels) == 0 || queuedAt.IsZero() || now.Sub(queuedAt) < threshold {
		return false
	}
	for _, have := range onlineRunners {
		if hasAll(have, jobLabels) {
			return false
		}
	}
	return true
}

func hasAll(have, want []string) bool {
	set := make(map[string]bool, len(have))
	for _, h := range have {
		set[strings.ToLower(h)] = true
	}
	for _, w := range want {
		if !set[strings.ToLower(w)] {
			return false
		}
	}
	return true
}
