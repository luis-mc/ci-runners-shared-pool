package runs

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// SupersededMinutes is the runner time of jobs that started after a later run of the same workflow, event and
// branch had been created, and were not cancelled (R5.24). Runs are grouped by workflow and branch; schedule
// runs and runs without a branch are left out. Per run attempt id.
func SupersededMinutes(jobs []insights.JobFact) map[runKey]float64 {
	type gk struct {
		Repo     int64
		Workflow string
		Branch   string
		Event    string
	}
	groups := map[gk][]runInfo{}
	for _, r := range groupRuns(jobs) {
		f := r.First
		if f.Branch == "" || f.Event == "schedule" || r.Created.IsZero() {
			continue
		}
		k := gk{f.RepositoryID, f.Workflow, f.Branch, f.Event}
		groups[k] = append(groups[k], r)
	}
	out := map[runKey]float64{}
	for _, rs := range groups {
		for i, r := range rs {
			// the first later run (by creation) with another head, of another run id
			var newer time.Time
			for _, n := range rs[i+1:] {
				if n.Key.ID != r.Key.ID && n.First.HeadSHA != r.First.HeadSHA && n.Created.After(r.Created) {
					newer = n.Created
					break
				}
			}
			if newer.IsZero() {
				continue
			}
			var m float64
			for _, j := range r.Jobs {
				if j.Result != "cancelled" && j.StartedAt.After(newer) {
					m += minutes(runDuration(j))
				}
			}
			if m > 0 {
				out[r.Key] += m
			}
		}
	}
	return out
}

// Duplicate is the second run of one workflow on one head from another event (R5.25).
type Duplicate struct {
	Workflow string
	Events   string // "pull_request+push": the two events, sorted
	Minutes  float64
	Run      runKey
}

// TwoEventsOneHead finds runs of one workflow on one head commit from two events; the later run is the waste.
func TwoEventsOneHead(jobs []insights.JobFact) []Duplicate {
	type hk struct {
		Repo     int64
		Workflow string
		Head     string
	}
	byHead := map[hk][]runInfo{}
	var order []hk
	for _, r := range groupRuns(jobs) {
		f := r.First
		if f.HeadSHA == "" || f.Event == "" {
			continue
		}
		k := hk{f.RepositoryID, f.Workflow, f.HeadSHA}
		if _, ok := byHead[k]; !ok {
			order = append(order, k)
		}
		byHead[k] = append(byHead[k], r)
	}
	var out []Duplicate
	for _, k := range order {
		rs := byHead[k]
		first := rs[0]
		for _, r := range rs[1:] {
			if r.First.Event == first.First.Event {
				continue
			}
			ev := []string{first.First.Event, r.First.Event}
			sort.Strings(ev)
			if m := r.runMinutes(); m > 0 {
				out = append(out, Duplicate{Workflow: k.Workflow, Events: strings.Join(ev, "+"), Minutes: m, Run: r.Key})
			}
		}
	}
	return out
}

// FindRetestedTrees finds push runs on the default branch whose commit tree a pull request run of the same workflow
// had already tested green (R5.26). It returns the minutes per run attempt. Trees come from the reader, which
// caches them; a commit the reader cannot say yet (ok false) is skipped.
func FindRetestedTrees(ctx context.Context, trees TreeReader, jobs []insights.JobFact) (map[runKey]float64, error) {
	type wk struct {
		Repo     int64
		Workflow string
	}
	var pushes []runInfo
	green := map[wk][]runInfo{}
	for _, r := range groupRuns(jobs) {
		f := r.First
		k := wk{f.RepositoryID, f.Workflow}
		switch {
		case f.Event == "push" && f.DefaultBranch:
			pushes = append(pushes, r)
		case f.Event == "pull_request" && allGreen(r.Jobs):
			green[k] = append(green[k], r)
		}
	}
	local := map[string]struct {
		tree string
		ok   bool
	}{}
	tree := func(repo int64, sha string) (string, bool, error) {
		key := string(rune(repo)) + "/" + sha
		if v, ok := local[key]; ok {
			return v.tree, v.ok, nil
		}
		t, ok, err := trees.Tree(ctx, repo, sha)
		if err != nil {
			return "", false, err
		}
		local[key] = struct {
			tree string
			ok   bool
		}{t, ok && t != ""}
		return t, ok && t != "", nil
	}
	out := map[runKey]float64{}
	for _, p := range pushes {
		cands := green[wk{p.First.RepositoryID, p.First.Workflow}]
		if len(cands) == 0 || p.First.HeadSHA == "" {
			continue
		}
		pt, ok, err := tree(p.First.RepositoryID, p.First.HeadSHA)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, c := range cands {
			if !c.Created.Before(p.Created) || c.First.HeadSHA == "" {
				continue
			}
			ct, ok, err := tree(c.First.RepositoryID, c.First.HeadSHA)
			if err != nil {
				return nil, err
			}
			if ok && ct == pt {
				if m := p.runMinutes(); m > 0 {
					out[p.Key] = m
				}
				break
			}
		}
	}
	return out, nil
}

func allGreen(jobs []insights.JobFact) bool {
	ok := false
	for _, j := range jobs {
		switch j.Result {
		case "success":
			ok = true
		case "skipped":
		default:
			return false
		}
	}
	return ok
}
