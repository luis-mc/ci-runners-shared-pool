package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// GraphSource reads the needs edges of one run attempt by node id (observability R5.42). Contract gap: the
// CIX8 contract has no reader for it, so the analyser takes this local interface.
type GraphSource interface {
	Needs(ctx context.Context, repositoryID, runID int64, attempt int) (map[string][]string, error)
}

// Analyser is the runs analyser (insights.Analyser).
type Analyser struct {
	Jobs   insights.JobSource
	Graphs GraphSource
	Trees  TreeReader
	// Release says a branch belongs to a release pull request, whose CI never stops at the first failure
	// (dev-standards DL-98). Nil uses the "release" branch prefix.
	Release func(branch string) bool
}

var _ insights.Analyser = Analyser{}

// Name is the analyser's stable name.
func (Analyser) Name() string { return "runs" }

// Kinds are the finding kinds the analyser owns.
func (Analyser) Kinds() []insights.Kind { return Kinds }

type acc struct {
	f   insights.Finding
	min float64
	n   int
}

// Analyse reads the window's jobs and returns the run findings.
func (a Analyser) Analyse(ctx context.Context, w insights.Window) ([]insights.Finding, error) {
	jobs, err := a.Jobs.JobsBetween(ctx, w, 0)
	if err != nil {
		return nil, err
	}
	release := a.Release
	if release == nil {
		release = defaultRelease
	}
	byKey := map[string]*acc{}
	add := func(first insights.JobFact, kind insights.Kind, subject, title, fix, snippet string, m float64) {
		k := string(kind) + "\x00" + fmt.Sprint(first.RepositoryID) + "\x00" + subject
		x := byKey[k]
		if x == nil {
			x = &acc{f: insights.Finding{Kind: kind, Scope: insights.ScopeRepo, RepositoryID: first.RepositoryID,
				Repository: first.Repository, Subject: subject, Area: insights.AreaRuns, Title: title, Fix: fix, Snippet: snippet}}
			byKey[k] = x
		}
		x.min += m
		x.n++
	}
	runs := groupRuns(jobs)
	byRun := map[runKey]runInfo{}
	for _, r := range runs {
		byRun[r.Key] = r
	}

	// R5.24
	for k, m := range SupersededMinutes(jobs) {
		f := byRun[k].First
		add(f, insights.KindRunSuperseded, f.Workflow,
			fmt.Sprintf("%s keeps running work that a newer push replaced", f.Workflow),
			"Add a concurrency group with cancel-in-progress so a new push on a branch cancels the older run. Keep it off for the default branch if every commit there must finish. "+
				"Skipping queued jobs of an older run is only advice here: crsp does not do it unless the owner turns it on.",
			"concurrency:\n  group: ${{ github.workflow }}-${{ github.ref }}\n  cancel-in-progress: true", m)
	}
	// R5.25
	for _, d := range TwoEventsOneHead(jobs) {
		f := byRun[d.Run].First
		add(f, insights.KindRunDuplicateEvent, d.Workflow+"|"+d.Events,
			fmt.Sprintf("%s runs twice on one commit (%s)", d.Workflow, d.Events),
			fmt.Sprintf("The same commit starts this workflow from two events (%s). Narrow one trigger, for example limit `push` to the default branch.", d.Events), "", d.Minutes)
	}
	// R5.26
	if a.Trees != nil {
		re, err := FindRetestedTrees(ctx, a.Trees, jobs)
		if err != nil {
			return nil, err
		}
		for k, m := range re {
			f := byRun[k].First
			add(f, insights.KindRunRetestedTree, f.Workflow,
				fmt.Sprintf("%s re-runs on the default branch a tree a pull request already tested", f.Workflow),
				"The push to the default branch has the same files as a pull request that passed. Skip the job when the tree was tested, or use a merge queue.", "", m)
		}
	}
	// R5.27, never for a release pull request (DL-98)
	for _, r := range runs {
		if release(r.First.Branch) {
			continue
		}
		if m := FailFastMinutes(r.Jobs); m > 0 {
			f := r.First
			add(f, insights.KindRunFailFast, f.Workflow,
				fmt.Sprintf("%s keeps running after a job has failed", f.Workflow), failFastFix, "", m)
		}
	}
	// R5.23
	if a.Graphs != nil {
		if err := a.criticalPath(ctx, runs, jobs, add); err != nil {
			return nil, err
		}
	}

	var out []insights.Finding
	for _, x := range byKey {
		if x.min < thresholdMinutes {
			continue
		}
		x.f.MinutesSavedWeek = weekly(x.min, w)
		x.f.Evidence, _ = json.Marshal(map[string]any{"runs": x.n, "minutes": int(x.min + 0.5)})
		out = append(out, x.f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

type addFn func(first insights.JobFact, kind insights.Kind, subject, title, fix, snippet string, m float64)

func (a Analyser) criticalPath(ctx context.Context, runs []runInfo, jobs []insights.JobFact, add addFn) error {
	medians := Medians(jobs)
	calc := NewSavings()
	for _, r := range runs {
		needs, err := a.Graphs.Needs(ctx, r.First.RepositoryID, r.Key.ID, r.Key.Attempt)
		if err != nil {
			return err
		}
		if len(needs) == 0 && len(r.Jobs) < 2 {
			continue
		}
		byNode := map[string]insights.JobFact{}
		for _, j := range r.Jobs {
			byNode[j.NodeID] = j
		}
		for _, s := range calc.Top(Graph{Jobs: r.Jobs, Needs: needs, Medians: medians}) {
			j := byNode[s.Job]
			add(j, insights.KindRunCriticalPath, j.Workflow+"|"+jobLabel(j)+"|"+string(s.Change),
				fmt.Sprintf("%s: the run is held up by %s", j.Workflow, jobLabel(j)),
				"Fix the job on the critical path: "+changeAdvice(s.Change), "", minutes(s.Sooner))
		}
	}
	return nil
}

func changeAdvice(c Change) string {
	switch c {
	case NoWait:
		return "give it a runner without a wait (more slots for its labels)."
	case MedianRun:
		return "find why it runs longer than its usual time (a cache miss or a slow step)."
	default:
		return "start it as soon as the jobs it needs end (check its needs and its trigger)."
	}
}

// Medians is the median run time per job key over successful jobs.
func Medians(jobs []insights.JobFact) map[string]time.Duration {
	by := map[string][]time.Duration{}
	for _, j := range jobs {
		if d := runDuration(j); d > 0 && j.Result == "success" {
			by[j.JobIdent] = append(by[j.JobIdent], d)
		}
	}
	out := map[string]time.Duration{}
	for k, ds := range by {
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		out[k] = ds[len(ds)/2]
	}
	return out
}
