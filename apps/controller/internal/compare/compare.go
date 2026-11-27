// Package compare measures the same job on crsp and on GitHub-hosted runners
// from finished workflow-run timings (observability R5.34).
//
// A job is matched by its name across the two runner kinds. A job on a runner
// whose labels hold "self-hosted" ran on crsp; any other ran on a hosted
// runner. Queue wait is started minus created; run time is completed minus
// started. Public repositories are excluded: hosted minutes there are free.
package compare

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// DefaultDays is the window when none is given; MaxDays caps it.
const (
	DefaultDays = 14
	MaxDays     = 90
)

// Stat is the median and 95th percentile of a sample, in seconds.
type Stat struct {
	Count  int     `json:"count"`
	Median float64 `json:"median_seconds"`
	P95    float64 `json:"p95_seconds"`
}

// Side is the timings of one runner kind for one job.
type Side struct {
	Queue Stat `json:"queue"`
	Run   Stat `json:"run"`
}

// Row is one job name seen on both runner kinds.
type Row struct {
	Job    string `json:"job"`
	Crsp   Side   `json:"crsp"`
	Hosted Side   `json:"hosted"`
	// MinutesSaved is, for every crsp run, the hosted median (queue + run)
	// minus the crsp median (queue + run), summed. Negative when crsp is slower.
	MinutesSaved float64 `json:"minutes_saved"`
	// CostAvoided is the hosted price of the crsp runs: each run's minutes
	// rounded up, times the per-minute price of the hosted label.
	CostAvoided float64 `json:"cost_avoided_usd"`
}

// Report is the comparison of one repository.
type Report struct {
	Repository string    `json:"repository"`
	Days       int       `json:"days"`
	Since      time.Time `json:"since"`
	// Excluded is set, with the reason, when the repository is public.
	Excluded bool   `json:"excluded"`
	Reason   string `json:"reason,omitempty"`
	Rows     []Row  `json:"rows"`
	// Unmatched counts jobs seen on one runner kind only.
	Unmatched         int     `json:"unmatched_jobs"`
	TotalMinutesSaved float64 `json:"total_minutes_saved"`
	TotalCostAvoided  float64 `json:"total_cost_avoided_usd"`
}

// Service builds reports.
type Service struct {
	Source Source
	Prices Prices
	Now    func() time.Time
}

// Compare reads the jobs of owner/repo of the last days and compares them.
func (s *Service) Compare(ctx context.Context, owner, repo string, days int) (Report, error) {
	if owner == "" || repo == "" {
		return Report{}, fmt.Errorf("compare: name the repository as owner/repo")
	}
	if days <= 0 {
		days = DefaultDays
	}
	if days > MaxDays {
		days = MaxDays
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	rep := Report{Repository: owner + "/" + repo, Days: days, Since: now().UTC().AddDate(0, 0, -days), Rows: []Row{}}
	vis, err := s.Source.Visibility(ctx, owner, repo)
	if err != nil {
		return Report{}, err
	}
	if vis == VisibilityPublic {
		rep.Excluded = true
		rep.Reason = "public repository: GitHub-hosted minutes are free, so there is no cost to compare"
		return rep, nil
	}
	jobs, err := s.Source.Jobs(ctx, owner, repo, rep.Since)
	if err != nil {
		return Report{}, err
	}
	rep.Rows, rep.Unmatched = Build(jobs, s.Prices)
	for _, r := range rep.Rows {
		rep.TotalMinutesSaved += r.MinutesSaved
		rep.TotalCostAvoided += r.CostAvoided
	}
	rep.TotalMinutesSaved = round2(rep.TotalMinutesSaved)
	rep.TotalCostAvoided = round2(rep.TotalCostAvoided)
	return rep, nil
}

// IsCrsp reports whether a job ran on a self-hosted runner.
func IsCrsp(labels []string) bool {
	for _, l := range labels {
		if strings.EqualFold(l, "self-hosted") {
			return true
		}
	}
	return false
}

type sample struct{ queue, run float64 }

type bucket struct {
	crsp, hosted []sample
	hostedLabels []string
	crspRuns     []float64 // run seconds of each crsp job, for the cost
}

// Build matches jobs by name and computes each row. Only successful and failed
// jobs with real timestamps count; cancelled and skipped jobs say nothing
// about speed.
func Build(jobs []Job, prices Prices) (rows []Row, unmatched int) {
	by := map[string]*bucket{}
	for _, j := range jobs {
		if j.Conclusion != "success" && j.Conclusion != "failure" {
			continue
		}
		if j.CreatedAt.IsZero() || j.StartedAt.IsZero() || j.CompletedAt.IsZero() ||
			j.StartedAt.Before(j.CreatedAt) || j.CompletedAt.Before(j.StartedAt) {
			continue
		}
		b := by[j.Name]
		if b == nil {
			b = &bucket{}
			by[j.Name] = b
		}
		sm := sample{queue: j.StartedAt.Sub(j.CreatedAt).Seconds(), run: j.CompletedAt.Sub(j.StartedAt).Seconds()}
		if IsCrsp(j.Labels) {
			b.crsp = append(b.crsp, sm)
			b.crspRuns = append(b.crspRuns, sm.run)
		} else {
			b.hosted = append(b.hosted, sm)
			if len(b.hostedLabels) == 0 {
				b.hostedLabels = j.Labels
			}
		}
	}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	rows = []Row{}
	for _, n := range names {
		b := by[n]
		if len(b.crsp) == 0 || len(b.hosted) == 0 {
			unmatched += len(b.crsp) + len(b.hosted)
			continue
		}
		row := Row{Job: n, Crsp: side(b.crsp), Hosted: side(b.hosted)}
		perRun := (row.Hosted.Queue.Median + row.Hosted.Run.Median) - (row.Crsp.Queue.Median + row.Crsp.Run.Median)
		row.MinutesSaved = round2(perRun * float64(len(b.crsp)) / 60)
		price := prices.For(b.hostedLabels)
		var billed float64
		for _, r := range b.crspRuns {
			billed += math.Max(1, math.Ceil(r/60))
		}
		row.CostAvoided = round2(billed * price)
		rows = append(rows, row)
	}
	return rows, unmatched
}

func side(s []sample) Side {
	q := make([]float64, len(s))
	r := make([]float64, len(s))
	for i, v := range s {
		q[i], r[i] = v.queue, v.run
	}
	return Side{Queue: stat(q), Run: stat(r)}
}

func stat(v []float64) Stat {
	if len(v) == 0 {
		return Stat{}
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return Stat{Count: len(s), Median: median(s), P95: percentile(s, 95)}
}

// median of a sorted slice; an even count averages the middle two.
func median(s []float64) float64 {
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// percentile is the nearest-rank percentile of a sorted slice.
func percentile(s []float64, p float64) float64 {
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	if rank < 1 {
		rank = 1
	}
	return s[rank-1]
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
