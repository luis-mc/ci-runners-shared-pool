package waits

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/insights"
)

type splitter struct{}

// NewSplitter returns the pure Splitter (R5.12).
func NewSplitter() Splitter { return splitter{} }

// span returns b-a and whether both ends are known and in order (never negative, unknown is not zero).
func span(a, b time.Time) (time.Duration, bool) {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return 0, false
	}
	return b.Sub(a), true
}

func (splitter) Split(j insights.JobFact) Split {
	var s Split
	crsp := j.Origin == insights.OriginCrsp || !j.SlotAt.IsZero()
	if crsp {
		s.Wait, s.WaitKnown = span(j.CreatedAt, j.SlotAt)
		s.Hold, s.HoldKnown = span(j.SlotAt, j.StartedAt)
		s.GitHubWait, _ = span(j.CreatedAt, j.QueuedAt)
	} else {
		s.Wait, s.WaitKnown = span(j.CreatedAt, j.StartedAt)
	}
	s.Run, s.RunKnown = span(j.StartedAt, j.CompletedAt)
	return s
}

// ComputeStats gives count, total, median and p95 (nearest rank) of ds.
func ComputeStats(ds []time.Duration) Stats {
	if len(ds) == 0 {
		return Stats{}
	}
	c := append([]time.Duration(nil), ds...)
	sort.Slice(c, func(i, k int) bool { return c[i] < c[k] })
	var tot time.Duration
	for _, d := range c {
		tot += d
	}
	return Stats{Known: len(c), Total: tot, P50: rank(c, 50), P90: rank(c, 90), P95: rank(c, 95)}
}

func rank(c []time.Duration, p int) time.Duration {
	i := (p*len(c)+99)/100 - 1
	if i < 0 {
		i = 0
	}
	return c[i]
}

// LabelOf is the runner label a job asked for (LOOK66): the crsp-prefixed one, else every label joined, else
// "no label".
func LabelOf(j insights.JobFact) string {
	for _, l := range j.Labels {
		if strings.HasPrefix(strings.ToLower(l), "crsp-") {
			return l
		}
	}
	if len(j.Labels) == 0 {
		return "no label"
	}
	return strings.Join(j.Labels, ", ")
}

// GroupKey is the group key of a job (R5.13): repository, job identity or UTC day of creation.
func GroupKey(j insights.JobFact, g Group) string {
	switch g {
	case ByJobKey:
		return j.JobIdent
	case ByLabel:
		return LabelOf(j)
	case ByDay:
		return j.CreatedAt.UTC().Format("2006-01-02")
	default:
		return j.Repository
	}
}

type acc struct {
	row             Row
	wait, hold, run []time.Duration
}

// Aggregate groups jobs by g into rows sorted by key. A hold over holdCap counts in HoldsOverCap and, when
// ex is not nil, its reason in HoldReasons (an explainer error counts as unknown).
func Aggregate(ctx context.Context, jobs []insights.JobFact, g Group, holdCap time.Duration, ex HoldExplainer) ([]Row, error) {
	sp := NewSplitter()
	m := map[string]*acc{}
	for _, j := range jobs {
		k := GroupKey(j, g)
		a := m[k]
		if a == nil {
			a = &acc{row: Row{Key: k, HoldReasons: map[HoldReason]int{}}}
			m[k] = a
		}
		a.row.Jobs++
		s := sp.Split(j)
		if s.WaitKnown {
			a.wait = append(a.wait, s.Wait)
		}
		if s.HoldKnown {
			a.hold = append(a.hold, s.Hold)
			if holdCap > 0 && s.Hold > holdCap {
				a.row.HoldsOverCap++
				r := HoldUnknown
				if ex != nil {
					if got, err := ex.Explain(ctx, j); err == nil && got != "" {
						r = got
					} else if err != nil && ctx.Err() != nil {
						return nil, fmt.Errorf("explain hold: %w", ctx.Err())
					}
				}
				a.row.HoldReasons[r]++
			}
		}
		if s.RunKnown {
			a.run = append(a.run, s.Run)
		}
	}
	rows := make([]Row, 0, len(m))
	for _, a := range m {
		a.row.Wait, a.row.Hold, a.row.Run = ComputeStats(a.wait), ComputeStats(a.hold), ComputeStats(a.run)
		rows = append(rows, a.row)
	}
	sort.Slice(rows, func(i, k int) bool { return rows[i].Key < rows[k].Key })
	return rows, nil
}

type reader struct {
	src insights.JobSource
	ex  HoldExplainer
	now func() time.Time
}

// NewReader returns the Reader over a JobSource. ex may be nil (every reason is unknown).
func NewReader(src insights.JobSource, ex HoldExplainer, now func() time.Time) Reader {
	return reader{src: src, ex: ex, now: now}
}

func (r reader) SplitBy(ctx context.Context, days int, g Group, repositoryID int64, holdCap time.Duration) ([]Row, error) {
	if days != 7 && days != 14 && days != 30 {
		return nil, fmt.Errorf("waits: days must be 7, 14 or 30, got %d", days)
	}
	to := r.now().UTC()
	jobs, err := r.src.JobsBetween(ctx, insights.Window{From: to.AddDate(0, 0, -days), To: to}, repositoryID)
	if err != nil {
		return nil, fmt.Errorf("waits: read jobs: %w", err)
	}
	return Aggregate(ctx, jobs, g, holdCap, r.ex)
}
