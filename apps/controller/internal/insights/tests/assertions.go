package tests

import (
	"sort"
	"time"
)

// NoAssertionTest is a test that passed with zero assertions in a format that reports them (R5.37).
type NoAssertionTest struct {
	TestID, Name, Suite, File string
	Kind                      TestKind
	JobIdents                 []string
	Runs                      int
	LastSeen                  time.Time
}

// FindNoAssertions returns the tests that passed with Assertions == 0. A row with Assertions -1 (the format does
// not report them) is "not reported", never zero: it never counts here.
func FindNoAssertions(rows []Row) []NoAssertionTest {
	by := map[string]*NoAssertionTest{}
	var ids []string
	for _, r := range rows {
		if r.Result != Passed || r.Assertions != 0 {
			continue
		}
		n := by[r.TestID]
		if n == nil {
			n = &NoAssertionTest{TestID: r.TestID, Name: r.Name, Suite: r.Suite, File: r.File, Kind: r.Kind}
			by[r.TestID] = n
			ids = append(ids, r.TestID)
		}
		n.Runs++
		n.JobIdents = appendUnique(n.JobIdents, JobKey(r.JobIdent))
		if r.FinishedAt.After(n.LastSeen) {
			n.LastSeen = r.FinishedAt
		}
	}
	out := make([]NoAssertionTest, 0, len(ids))
	for _, id := range ids {
		out = append(out, *by[id])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		return out[i].TestID < out[j].TestID
	})
	return out
}
