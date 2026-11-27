package runs

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// thresholdMinutes is the weekly total a workflow needs before a run finding opens (R5.23, R5.24, R5.26, R5.27).
const thresholdMinutes = 10.0

// runKey names one run attempt.
type runKey struct {
	ID      int64
	Attempt int
}

// runInfo is the jobs of one run attempt and what the analysers need to know about the run.
type runInfo struct {
	Key     runKey
	Jobs    []insights.JobFact
	Created time.Time // the earliest job creation, the run's creation
	First   insights.JobFact
}

// runDuration is how long a job ran; zero when a time is unknown (observability R5.9).
func runDuration(j insights.JobFact) time.Duration {
	if j.StartedAt.IsZero() || j.CompletedAt.IsZero() || !j.CompletedAt.After(j.StartedAt) {
		return 0
	}
	return j.CompletedAt.Sub(j.StartedAt)
}

func minutes(d time.Duration) float64 { return d.Minutes() }

// runMinutes is the runner time of all jobs of a run.
func (r runInfo) runMinutes() float64 {
	var m float64
	for _, j := range r.Jobs {
		m += minutes(runDuration(j))
	}
	return m
}

// groupRuns groups job facts by run attempt, ordered by creation then run id.
func groupRuns(jobs []insights.JobFact) []runInfo {
	idx := map[runKey]int{}
	var out []runInfo
	for _, j := range jobs {
		k := runKey{j.RunID, j.RunAttempt}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, runInfo{Key: k, First: j})
		}
		r := &out[i]
		r.Jobs = append(r.Jobs, j)
		if !j.CreatedAt.IsZero() && (r.Created.IsZero() || j.CreatedAt.Before(r.Created)) {
			r.Created = j.CreatedAt
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].Created.Equal(out[b].Created) {
			return out[a].Created.Before(out[b].Created)
		}
		return out[a].Key.ID < out[b].Key.ID
	})
	return out
}

// fmtMin writes minutes for words: one decimal under 10, whole above.
func fmtMin(m float64) string {
	if m >= 10 {
		return fmt.Sprintf("%.0f", math.Round(m))
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", m), ".0")
}

// weekly scales minutes found in w to a week.
func weekly(m float64, w insights.Window) int {
	span := w.To.Sub(w.From)
	if span <= 0 {
		return int(math.Round(m))
	}
	return int(math.Round(m * float64(7*24*time.Hour) / float64(span)))
}

func jobLabel(j insights.JobFact) string {
	if j.JobName != "" {
		return j.JobName
	}
	return j.NodeID
}

// defaultRelease is a release pull request's branch (dev-standards DL-98): its CI never stops at the first failure.
func defaultRelease(branch string) bool {
	return strings.HasPrefix(strings.ToLower(branch), "release")
}
