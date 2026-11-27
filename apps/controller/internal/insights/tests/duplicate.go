package tests

import (
	"sort"
	"time"
)

// Duplicate is one test id that ran in two or more job keys of the same run (R5.36).
type Duplicate struct {
	TestID, Name, Suite, File string
	Kind                      TestKind
	JobIdents                 []string
	Runs                      int           // runs of the workflow where it happened
	Extra                     time.Duration // the time of the extra runs: every job's run but the longest
	LastSeen                  time.Time
}

type dupKey struct {
	runID   int64
	attempt int
	testID  string
}

// DuplicateTests finds the tests run by more than one job of one run. A matrix of the same job is one job key.
func DuplicateTests(rows []Row) []Duplicate {
	type perJob struct {
		dur time.Duration
		row Row
	}
	groups := map[dupKey]map[string]*perJob{}
	var order []dupKey
	for _, r := range rows {
		if r.Result == Skipped || r.RunID == 0 {
			continue
		}
		k := dupKey{r.RunID, r.RunAttempt, r.TestID}
		m := groups[k]
		if m == nil {
			m = map[string]*perJob{}
			groups[k] = m
			order = append(order, k)
		}
		jk := JobKey(r.JobIdent)
		if p := m[jk]; p != nil {
			p.dur += r.Duration
		} else {
			m[jk] = &perJob{dur: r.Duration, row: r}
		}
	}
	by := map[string]*Duplicate{}
	var ids []string
	for _, k := range order {
		m := groups[k]
		if len(m) < 2 {
			continue
		}
		d := by[k.testID]
		var longest time.Duration
		var sum time.Duration
		keys := make([]string, 0, len(m))
		for jk := range m {
			keys = append(keys, jk)
		}
		sort.Strings(keys)
		for _, jk := range keys {
			p := m[jk]
			if d == nil {
				d = &Duplicate{TestID: k.testID, Name: p.row.Name, Suite: p.row.Suite, File: p.row.File, Kind: p.row.Kind}
				by[k.testID] = d
				ids = append(ids, k.testID)
			}
			d.JobIdents = appendUnique(d.JobIdents, jk)
			sum += p.dur
			if p.dur > longest {
				longest = p.dur
			}
			if p.row.FinishedAt.After(d.LastSeen) {
				d.LastSeen = p.row.FinishedAt
			}
		}
		d.Runs++
		d.Extra += sum - longest
	}
	out := make([]Duplicate, 0, len(ids))
	for _, id := range ids {
		d := *by[id]
		sort.Strings(d.JobIdents)
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Extra != out[j].Extra {
			return out[i].Extra > out[j].Extra
		}
		return out[i].TestID < out[j].TestID
	})
	return out
}
