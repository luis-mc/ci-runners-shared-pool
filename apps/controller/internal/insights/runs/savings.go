package runs

import (
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// minSaving is the smallest saving worth saying (R5.23 words are whole tenths of minutes).
const minSaving = 30 * time.Second

// NewSavings returns the critical path calculator of R5.23.
func NewSavings() Savings { return savings{} }

type savings struct{}

type node struct {
	job   insights.JobFact
	needs []string
	run   time.Duration
	delay time.Duration // from the end of the last need (or the run's creation) to the start
	qwait time.Duration // the part of the delay spent queued or holding a slot
}

// Top returns up to 3 changes that end the run sooner, most first. Jobs with unknown times are left out.
func (savings) Top(g Graph) []Saving {
	nodes := map[string]*node{}
	var runStart time.Time
	for _, j := range g.Jobs {
		if j.NodeID == "" || runDuration(j) == 0 {
			continue
		}
		nodes[j.NodeID] = &node{job: j}
		t := j.CreatedAt
		if t.IsZero() || t.After(j.StartedAt) {
			t = j.StartedAt
		}
		if runStart.IsZero() || t.Before(runStart) {
			runStart = t
		}
	}
	if len(nodes) == 0 {
		return nil
	}
	for id, n := range nodes {
		for _, d := range g.Needs[id] {
			if _, ok := nodes[d]; ok && d != id {
				n.needs = append(n.needs, d)
			}
		}
		sort.Strings(n.needs)
		n.run = runDuration(n.job)
	}
	// Actual ready time of each node; delays follow. Cycles are cut by the visiting mark.
	actualEnd := func(id string) time.Time { return nodes[id].job.CompletedAt }
	for _, n := range nodes {
		ready := runStart
		for _, d := range n.needs {
			if e := actualEnd(d); e.After(ready) {
				ready = e
			}
		}
		if n.job.StartedAt.After(ready) {
			n.delay = n.job.StartedAt.Sub(ready)
		}
		ref := n.job.QueuedAt
		if ref.IsZero() {
			ref = n.job.CreatedAt
		}
		if ref.Before(ready) {
			ref = ready
		}
		if n.job.StartedAt.After(ref) {
			n.qwait = n.job.StartedAt.Sub(ref)
		}
	}
	ids := make([]string, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	// simEnd is the end of the whole run when node override has the given delay and run time.
	simEnd := func(override string, delay, run time.Duration) time.Time {
		memo := map[string]time.Time{}
		visiting := map[string]bool{}
		var end func(id string) time.Time
		end = func(id string) time.Time {
			if e, ok := memo[id]; ok {
				return e
			}
			if visiting[id] {
				return runStart
			}
			visiting[id] = true
			n := nodes[id]
			ready := runStart
			for _, d := range n.needs {
				if e := end(d); e.After(ready) {
					ready = e
				}
			}
			dl, rn := n.delay, n.run
			if id == override {
				dl, rn = delay, run
			}
			e := ready.Add(dl + rn)
			visiting[id] = false
			memo[id] = e
			return e
		}
		var last time.Time
		for _, id := range ids {
			if e := end(id); e.After(last) {
				last = e
			}
		}
		return last
	}
	base := simEnd("", 0, 0)

	// The critical path: from the job that ended last, back through the need that ended last.
	var cur string
	for _, id := range ids {
		if cur == "" || nodes[id].job.CompletedAt.After(nodes[cur].job.CompletedAt) {
			cur = id
		}
	}
	var path []string
	seen := map[string]bool{}
	for cur != "" && !seen[cur] {
		seen[cur] = true
		path = append(path, cur)
		next := ""
		for _, d := range nodes[cur].needs {
			if next == "" || nodes[d].job.CompletedAt.After(nodes[next].job.CompletedAt) {
				next = d
			}
		}
		cur = next
	}

	var out []Saving
	add := func(id string, c Change, sooner time.Duration, words string) {
		if sooner >= minSaving {
			out = append(out, Saving{Job: id, Change: c, Sooner: sooner,
				Words: fmt.Sprintf("The run would end %s min sooner if %s.", fmtMin(minutes(sooner)), words)})
		}
	}
	for _, id := range path {
		n := nodes[id]
		label := jobLabel(n.job)
		add(id, NoWait, base.Sub(simEnd(id, n.delay-n.qwait, n.run)), label+" had not waited for a runner slot")
		if m := g.Medians[n.job.JobIdent]; m > 0 && m < n.run {
			add(id, MedianRun, base.Sub(simEnd(id, n.delay, m)), fmt.Sprintf("%s had run in its usual %s min", label, fmtMin(minutes(m))))
		}
		add(id, StartAfterNeeds, base.Sub(simEnd(id, 0, n.run)), label+" had started as soon as the jobs it needs ended")
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Sooner != out[b].Sooner {
			return out[a].Sooner > out[b].Sooner
		}
		if out[a].Job != out[b].Job {
			return out[a].Job < out[b].Job
		}
		return out[a].Change < out[b].Change
	})
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}
