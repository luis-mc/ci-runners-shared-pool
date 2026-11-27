package runnercache

import (
	"fmt"
	"path/filepath"
)

// BudgetRunner is one runner's folder for the pool-wide budget. A busy runner
// has a job running: nothing under it is touched (caches R5.48).
type BudgetRunner struct {
	Name   string
	Folder string
	Busy   bool
}

// BudgetItem is one removed top-level cache entry.
type BudgetItem struct {
	Runner string
	Path   string
	Bytes  int64
}

// BudgetResult is what one budget pass did.
type BudgetResult struct {
	Before, After, Budget int64
	Removed               []BudgetItem
	// Over is true when the folders still pass the budget because what is left
	// belongs to runners with a job running.
	Over bool
}

// Lines are the log lines of the pass.
func (r BudgetResult) Lines() []string {
	var out []string
	for _, it := range r.Removed {
		out = append(out, fmt.Sprintf("cache budget: removed %s of %s (%d MiB)", filepath.Base(it.Path), it.Runner, it.Bytes>>20))
	}
	if r.Over {
		out = append(out, fmt.Sprintf("cache budget: still over (%d MiB of %d MiB); the rest is in use by running jobs", r.After>>20, r.Budget>>20))
	}
	return out
}

// BudgetTotal sums the cache folders of the runners, busy or not. It is what the
// timer compares with the budget before it asks for any runner to be idle.
func BudgetTotal(fsys FS, runners []BudgetRunner) (int64, error) {
	var n int64
	for _, r := range runners {
		for _, dir := range cacheFolders(r.Folder) {
			es, err := Scan(fsys, dir)
			if err != nil {
				return 0, err
			}
			n += Total(es)
		}
	}
	return n, nil
}

func cacheFolders(runnerFolder string) []string {
	d := DirsFor(runnerFolder)
	return []string{d.Tools, d.Rust}
}

// EnforceBudget brings every cache folder of the machine under the budget:
// the oldest top-level entries across all runners go first, whole, and it
// stops as soon as the total fits. Entries of a busy runner are counted and
// never removed. A budget of 0 or less does nothing.
func EnforceBudget(fsys FS, runners []BudgetRunner, budget int64) (BudgetResult, error) {
	res := BudgetResult{Budget: budget}
	type cand struct {
		Entry
		runner string
	}
	var cands []cand
	for _, r := range runners {
		for _, dir := range cacheFolders(r.Folder) {
			es, err := Scan(fsys, dir)
			if err != nil {
				return res, err
			}
			res.Before += Total(es)
			if r.Busy {
				continue
			}
			for _, e := range es {
				cands = append(cands, cand{e, r.Name})
			}
		}
	}
	res.After = res.Before
	if budget <= 0 || res.Before <= budget {
		return res, nil
	}
	plain := make([]Entry, len(cands))
	byPath := map[string]string{}
	for i, c := range cands {
		plain[i] = c.Entry
		byPath[c.Path] = c.runner
	}
	for _, e := range Order(plain) {
		if res.After <= budget {
			break
		}
		if err := fsys.RemoveAll(e.Path); err != nil {
			return res, err
		}
		res.After -= e.Size
		res.Removed = append(res.Removed, BudgetItem{Runner: byPath[e.Path], Path: e.Path, Bytes: e.Size})
	}
	res.Over = res.After > budget
	return res, nil
}
