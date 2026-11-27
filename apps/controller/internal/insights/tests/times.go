package tests

import (
	"sort"
	"strings"
	"time"
)

// JobKey is the job a row ran in without its matrix values: "build (ubuntu, 20)" and "build (windows, 20)" are
// one job key, so a matrix of the same job is never "duplicated" (R5.36).
func JobKey(ident string) string {
	if strings.HasSuffix(ident, ")") {
		if i := strings.Index(ident, " ("); i > 0 {
			return ident[:i]
		}
	}
	return ident
}

// Percentile returns the nearest-rank percentile (0 to 1) of ds, which it sorts.
func Percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	rank := int(p*float64(len(ds)) + 0.999999999)
	if rank < 1 {
		rank = 1
	}
	if rank > len(ds) {
		rank = len(ds)
	}
	return ds[rank-1]
}

// Median is the middle duration; for an even count the mean of the two in the middle.
func Median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	n := len(ds)
	if n%2 == 1 {
		return ds[n/2]
	}
	return (ds[n/2-1] + ds[n/2]) / 2
}

type timeKey struct{ testID, jobKey string }

// Times returns the time insight of each test and job key (R5.34): runs, median and p95 of the runs that
// ran (skipped ones are left out), failures, and the G1 limit when the p95 is over it.
func Times(rows []Row, repository string) []Insight {
	type acc struct {
		in   Insight
		durs []time.Duration
	}
	by := map[timeKey]*acc{}
	var order []timeKey
	for _, r := range rows {
		if r.Result == Skipped {
			continue
		}
		k := timeKey{r.TestID, JobKey(r.JobIdent)}
		a := by[k]
		if a == nil {
			a = &acc{in: Insight{TestID: r.TestID, Name: r.Name, Suite: r.Suite, File: r.File, Repository: repository,
				JobIdents: []string{k.jobKey}, Kind: r.Kind, Assertions: -1}}
			by[k] = a
			order = append(order, k)
		}
		a.in.Runs++
		if r.Result == Failed || r.Result == Error {
			a.in.Failures++
		}
		a.durs = append(a.durs, r.Duration)
		if r.FinishedAt.After(a.in.LastSeen) {
			a.in.LastSeen = r.FinishedAt
		}
	}
	out := make([]Insight, 0, len(order))
	for _, k := range order {
		a := by[k]
		a.in.P50 = Median(a.durs)
		a.in.P95 = Percentile(a.durs, 0.95)
		if lim := Limit(a.in.Kind); a.in.P95 > lim {
			a.in.OverLimit = lim
		}
		out = append(out, a.in)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].P95 != out[j].P95 {
			return out[i].P95 > out[j].P95
		}
		return out[i].TestID < out[j].TestID
	})
	return out
}

// OverLimitCount is how many tests (by id) have a p95 over their G1 limit; the guardrails analyser reads it
// for G1 (R5.32).
func OverLimitCount(rows []Row) int {
	seen := map[string]bool{}
	for _, in := range Times(rows, "") {
		if in.OverLimit > 0 {
			seen[in.TestID] = true
		}
	}
	return len(seen)
}

// SlowRepoMinimum is the weekly test time a repository needs before its slowest tests give findings.
const SlowRepoMinimum = 5 * time.Minute

// SlowTests returns the slowest tests of a repository whose tests together ran SlowRepoMinimum or more in the
// rows (a week), by weekly time, at most limit of them. Excess is the weekly time above each run's G1 limit,
// the minutes the fix would save (R5.34); WeeklyTotal is all the time the test took.
func SlowTests(rows []Row, limit int) []Slow {
	var total time.Duration
	by := map[string]*Slow{}
	var order []string
	for _, r := range rows {
		if r.Result == Skipped {
			continue
		}
		total += r.Duration
		s := by[r.TestID]
		if s == nil {
			s = &Slow{TestID: r.TestID, Name: r.Name, Suite: r.Suite, File: r.File, Kind: r.Kind}
			by[r.TestID] = s
			order = append(order, r.TestID)
		}
		s.Runs++
		s.WeeklyTotal += r.Duration
		if lim := Limit(r.Kind); r.Duration > lim {
			s.Excess += r.Duration - lim
		}
		if r.Duration > s.Slowest {
			s.Slowest = r.Duration
		}
	}
	if total < SlowRepoMinimum {
		return nil
	}
	out := make([]Slow, 0, len(order))
	for _, id := range order {
		out = append(out, *by[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].WeeklyTotal != out[j].WeeklyTotal {
			return out[i].WeeklyTotal > out[j].WeeklyTotal
		}
		return out[i].TestID < out[j].TestID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Slow is one slow test of a repository.
type Slow struct {
	TestID, Name, Suite, File string
	Kind                      TestKind
	Runs                      int
	WeeklyTotal, Excess       time.Duration
	Slowest                   time.Duration
}
