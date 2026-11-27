package tests

import (
	"sort"
	"time"
)

// FlakyRun is one result that proves a flaky test.
type FlakyRun struct {
	RunID    int64  `json:"runId"`
	Attempt  int    `json:"attempt"`
	JobIdent string `json:"jobIdent"`
	HeadSHA  string `json:"headSha"`
	Result   Result `json:"result"`
	Retries  int    `json:"retries,omitempty"`
}

// FlakyTest is one flaky test: the same job key and head commit gave different results, or it passed after a
// retry inside one job (R5.35). No reruns are made: crsp only reads what ran.
type FlakyTest struct {
	TestID, Name, Suite, File string
	Kind                      TestKind
	JobIdents                 []string
	Groups                    int           // job key and commit pairs that prove it
	Runs                      []FlakyRun    // the proof
	Rerun                     time.Duration // the re-run time it caused
	LastSeen                  time.Time
}

type flakyKey struct{ testID, jobKey, sha string }

// FlakyTests finds the flaky tests of rows (R5.35).
func FlakyTests(rows []Row) []FlakyTest {
	groups := map[flakyKey][]Row{}
	var order []flakyKey
	for _, r := range rows {
		if r.Result == Skipped || r.HeadSHA == "" && r.Retries == 0 {
			continue
		}
		k := flakyKey{r.TestID, JobKey(r.JobIdent), r.HeadSHA}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	by := map[string]*FlakyTest{}
	var ids []string
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool {
			if g[i].RunID != g[j].RunID {
				return g[i].RunID < g[j].RunID
			}
			return g[i].RunAttempt < g[j].RunAttempt
		})
		var rerun time.Duration
		if !isFlakyGroup(g) {
			continue
		}
		f := by[k.testID]
		if f == nil {
			f = &FlakyTest{TestID: k.testID, Name: g[0].Name, Suite: g[0].Suite, File: g[0].File, Kind: g[0].Kind}
			by[k.testID] = f
			ids = append(ids, k.testID)
		}
		f.Groups++
		f.JobIdents = appendUnique(f.JobIdents, k.jobKey)
		for i, r := range g {
			if i > 0 {
				rerun += r.Duration
			}
			rerun += time.Duration(r.Retries) * r.Duration
			f.Runs = append(f.Runs, FlakyRun{RunID: r.RunID, Attempt: r.RunAttempt, JobIdent: r.JobIdent, HeadSHA: r.HeadSHA, Result: r.Result, Retries: r.Retries})
			if r.FinishedAt.After(f.LastSeen) {
				f.LastSeen = r.FinishedAt
			}
		}
		f.Rerun += rerun
	}
	out := make([]FlakyTest, 0, len(ids))
	for _, id := range ids {
		out = append(out, *by[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Groups != out[j].Groups {
			return out[i].Groups > out[j].Groups
		}
		return out[i].TestID < out[j].TestID
	})
	return out
}

// isFlakyGroup: different results across the attempts of one job key and commit, or a pass after a retry.
func isFlakyGroup(g []Row) bool {
	var pass, bad bool
	for _, r := range g {
		if r.Result == Passed {
			pass = true
			if r.Retries > 0 {
				return true
			}
		} else {
			bad = true
		}
	}
	return pass && bad && len(g) > 1
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}
