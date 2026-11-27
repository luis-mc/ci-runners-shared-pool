package runs

import (
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// FailFastMinutes is the runner time other jobs of a failed run spent after its first failed job ended, with
// jobs that started later counted whole (R5.27). Zero when no job failed or no end time is known.
func FailFastMinutes(jobs []insights.JobFact) float64 {
	var first time.Time
	var firstID int64
	for _, j := range jobs {
		if j.Result == "failure" && !j.CompletedAt.IsZero() && (first.IsZero() || j.CompletedAt.Before(first)) {
			first, firstID = j.CompletedAt, j.ID
		}
	}
	if first.IsZero() {
		return 0
	}
	var total time.Duration
	for _, j := range jobs {
		if j.ID == firstID || runDuration(j) == 0 || !j.CompletedAt.After(first) {
			continue
		}
		from := j.StartedAt
		if from.Before(first) {
			from = first
		}
		total += j.CompletedAt.Sub(from)
	}
	return minutes(total)
}

// failFastWaste is the Waste of one run for the Reader; release runs are left out (dev-standards DL-98).
func failFastWaste(r runInfo, release func(string) bool) (Waste, bool) {
	if release(r.First.Branch) {
		return Waste{}, false
	}
	m := FailFastMinutes(r.Jobs)
	if m <= 0 {
		return Waste{}, false
	}
	return Waste{Kind: FailFast, Minutes: m,
		Words: fmt.Sprintf("Other jobs ran %s min after the first failure.", fmtMin(m))}, true
}

const failFastFix = "Stop the work that cannot matter once a job has failed: use `fail-fast: true` for a matrix, make the cheap checks " +
	"(lint, types) a `needs` of the long jobs, or cancel the run on failure. This advice is for runs outside a release pull request; " +
	"a release pull request's CI keeps running past the first failure on purpose."
