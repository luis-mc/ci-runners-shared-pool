package api

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// Pure mapping of the Jobs section data to the contract (OPS34): no I/O, so each function is tested alone. The
// HTTP methods (handlers_jobs.go, handlers_runs.go, handlers_jobstats.go) read, call these and answer.

func fl32(v float64) float32 { return float32(v) }

func fl32p(v float64) *float32 { n := float32(v); return &n }

// toForecast is a forecast as the contract shows it. The quartiles are given only when there is a history.
func toForecast(f forecast.Forecast) gen.Forecast {
	out := gen.Forecast{P50: fl32(f.P50), Lo: fl32(f.Lo), Hi: fl32(f.Hi), Basis: gen.ForecastBasis(f.Basis), Samples: f.Samples,
		Machine: ptr(f.Machine)}
	if out.Basis == "" {
		out.Basis = gen.ForecastBasis(forecast.BasisNone)
	}
	if f.Basis != forecast.BasisNone && f.Basis != "" {
		out.P25, out.P75 = fl32p(f.P25), fl32p(f.P75)
		if f.ChangedLately {
			t := true
			out.ChangedLately = &t
		}
	}
	return out
}

func toEta(e forecast.Eta) *gen.Eta {
	return &gen.Eta{P50: fl32(e.P50), Lo: fl32(e.Lo), Hi: fl32(e.Hi)}
}

// recordForecast turns the forecast stored when a job started back into a forecast.
func recordForecast(r jobsdata.ForecastRecord) forecast.Forecast {
	basis := forecast.Basis(r.Basis)
	if basis == "" {
		basis = forecast.BasisNone
	}
	mid := float64(r.MidMS) / 1000
	return forecast.Forecast{P50: mid, P25: float64(r.LoMS) / 1000, P75: float64(r.HiMS) / 1000, Lo: float64(r.LoMS) / 1000,
		Hi: float64(r.HiMS) / 1000, Basis: basis, Samples: r.Samples}
}

func jobClass(j jobsdata.Job) gen.JobJobType {
	// The store keeps light or heavy only (jobsdata.Job.Light); "standard" is left for a job class it cannot tell yet.
	if j.Light {
		return gen.JobJobType("light")
	}
	return gen.JobJobType("heavy")
}

func toRunnerRef(j jobsdata.Job) *gen.RunnerRef {
	if j.RunnerID == "" && j.RunnerName == "" {
		return nil
	}
	r := &gen.RunnerRef{Id: j.RunnerID, Name: j.RunnerName, Kind: gen.RunnerKind(j.RunnerKind), MachineId: ptr(j.MachineID),
		MachineName: ptr(j.MachineName), Backend: ptr(j.Backend), Isolation: isolation(j.Isolation)}
	if r.Kind == "" {
		r.Kind = gen.RunnerKind(jobsdata.Persistent)
	}
	if r.Id == "" {
		r.Id = r.Name
	}
	switch j.Cache {
	case "warm", "cold":
		c := gen.RunnerRefCache(j.Cache)
		r.Cache = &c
	}
	return r
}

// toJobItem is a job without its forecast and queue place; the caller adds those.
func toJobItem(j jobsdata.Job) gen.Job {
	out := gen.Job{
		Id: j.ID, Repository: j.Repository, RepositoryId: ptr(j.RepositoryID), RunId: j.RunID, RunAttempt: j.Attempt,
		GithubJobId: j.ID, Workflow: ptr(j.Workflow), JobName: j.Name, HeadSha: ptr(j.HeadSHA), Trigger: ptr(j.Event),
		Labels: j.Labels, State: gen.JobState(j.State), MachineId: ptr(j.MachineID), RunnerId: ptr(j.RunnerID), RunnerName: ptr(j.RunnerName),
		Backend: ptr(j.Backend), Isolation: isolation(j.Isolation), CreatedAt: tptr(j.CreatedAt), QueuedAt: tptr(j.QueuedAt),
		SlotAt: tptr(j.SlotAt), StartedAt: tptr(j.StartedAt), CompletedAt: tptr(j.CompletedAt),
		GithubWaitSeconds: secs(j.CreatedAt, j.StartedAt), SlotWaitSeconds: secs(j.QueuedAt, j.SlotAt), RunSeconds: secs(j.StartedAt, j.CompletedAt),
		Result: jobResult(string(j.Result)), HtmlUrl: ptr(j.HTMLURL),
		JobIdent: ptr(j.JobKey), NodeId: ptr(j.Name), Branch: ptr(j.Branch), JobType: ptrOf(jobClass(j)), Runner: toRunnerRef(j),
	}
	if out.Labels == nil {
		out.Labels = []string{}
	}
	m := j.Matrix
	if m == nil {
		m = []string{}
	}
	out.Matrix = &m
	if j.State == jobsdata.Queued {
		out.WaitReason, out.WaitText = waitReason(j.WaitReason), ptr(j.WaitText)
	}
	if out.HtmlUrl == nil && j.Repository != "" {
		out.HtmlUrl = ptr(githubSet().JobPage(j.Repository, j.RunID, j.ID))
	}
	if j.CleanupMS > 0 {
		out.CleanupSeconds = ptr(int(j.CleanupMS / 1000))
	}
	return out
}

func ptrOf[T any](v T) *T { return &v }

// JobRunnerInfo is who ran a job and where its steps came from (ci-run-insights R5.39 to R5.41).
// Origin is "crsp", "other", "hosted", or "" while not known.
type JobRunnerInfo struct {
	Origin      string
	Group       string
	Labels      []string
	StepsSource string // "events" or "rest", "" while not known
	CheckedAt   time.Time
}

// JobRunnerReader reads JobRunnerInfo of jobs by GitHub job id. A job it has nothing for is left out.
type JobRunnerReader interface {
	JobRunners(ctx context.Context, jobIDs []int64) (map[int64]JobRunnerInfo, error)
}

// elsewhere is true for a job crsp cannot run: another runner or a GitHub-hosted one (owner decision D7).
func (i JobRunnerInfo) elsewhere() bool { return i.Origin == "other" || i.Origin == "hosted" }

// withRunnerInfo adds the origin, group, labels, steps source and read time to a job.
func withRunnerInfo(j gen.Job, i JobRunnerInfo) gen.Job {
	switch i.Origin {
	case "crsp", "other", "hosted":
		o := gen.JobRunnerOrigin(i.Origin)
		j.RunnerOrigin = &o
	}
	if i.Group != "" {
		j.RunnerGroup = ptr(i.Group)
	}
	if len(i.Labels) > 0 {
		l := i.Labels
		j.RunnerLabels = &l
	}
	switch i.StepsSource {
	case "events", "rest":
		ss := gen.JobStepsSource(i.StepsSource)
		j.StepsSource = &ss
	}
	if !i.CheckedAt.IsZero() {
		j.CheckedAt = tptr(i.CheckedAt.UTC())
	}
	return j
}

// runnerInfos reads the runner info of rows; a nil reader or a failed read gives none (the page still shows).
func (s *server) runnerInfos(ctx context.Context, ids []int64) map[int64]JobRunnerInfo {
	if s.opts.JobRunners == nil || len(ids) == 0 {
		return nil
	}
	m, err := s.opts.JobRunners.JobRunners(ctx, ids)
	if err != nil {
		s.opts.Logger.Warn("job runner origin not read", "err", err)
		return nil
	}
	return m
}

// withRun adds what the run knows: its pull request, task and worktree.
func withRun(j gen.Job, r jobsdata.Run, worktree string) gen.Job {
	j.PullRequest, j.TaskId = ptr(r.PullRequest), ptr(r.TaskID)
	j.Worktree = ptr(firstOf(worktree, r.Worktree))
	return j
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// jobSeconds is a finished job's run time in seconds, or false.
func jobSeconds(j jobsdata.Job) (float64, bool) {
	if j.StartedAt.IsZero() || j.CompletedAt.IsZero() || j.CompletedAt.Before(j.StartedAt) {
		return 0, false
	}
	return j.CompletedAt.Sub(j.StartedAt).Seconds(), true
}

func toStepItem(st jobsdata.Step) gen.StepItem {
	out := gen.StepItem{Number: st.Number, Name: st.Name, State: gen.StepItemState(st.State), StartedAt: tptr(st.StartedAt), CompletedAt: tptr(st.CompletedAt)}
	if st.Result != "" {
		r := gen.StepItemResult(st.Result)
		out.Result = &r
	}
	if !st.StartedAt.IsZero() && !st.CompletedAt.IsZero() && !st.CompletedAt.Before(st.StartedAt) {
		out.RunSeconds = fl32p(st.CompletedAt.Sub(st.StartedAt).Seconds())
	}
	return out
}

func toHistory(h []jobsdata.HistoryPoint) []gen.HistoryPoint {
	out := make([]gen.HistoryPoint, 0, len(h))
	for _, p := range h {
		out = append(out, gen.HistoryPoint{At: p.At, Seconds: fl32(p.Seconds), Result: jobResult(string(p.Result)), Branch: p.Branch, Machine: ptr(p.Machine)})
	}
	return out
}

func toAttempts(a []jobsdata.JobAttempt) []gen.AttemptRef {
	out := make([]gen.AttemptRef, 0, len(a))
	for _, x := range a {
		out = append(out, gen.AttemptRef{Attempt: x.Attempt, JobId: x.JobID, State: gen.AttemptRefState(x.State), Result: jobResult(string(x.Result))})
	}
	return out
}

// ---- the queue

// queueInfo is the place of a queued job and when it starts, from the simulation.
type queueInfo struct {
	Position int
	Eta      forecast.Eta
	Known    bool
}

// queueETAs gives every queued job in open its place and its start ETA. fc holds the forecast of each open job by id,
// slots the slot count per machine id; the key "" is the whole pool, used by a job no machine took yet (it also
// takes every running job). A job queues behind the jobs of its machine in the order of its ticket (QueuedAt, else
// CreatedAt, then id). A machine without a slot count leaves its ETAs unknown.
func queueETAs(open []jobsdata.Job, fc map[int64]forecast.Forecast, slots map[string]int, now time.Time) map[int64]queueInfo {
	var queued []jobsdata.Job
	for _, j := range open {
		if j.State == jobsdata.Queued {
			queued = append(queued, j)
		}
	}
	sort.SliceStable(queued, func(a, b int) bool {
		ta, tb := firstTime(queued[a].QueuedAt, queued[a].CreatedAt), firstTime(queued[b].QueuedAt, queued[b].CreatedAt)
		if !ta.Equal(tb) {
			return ta.Before(tb)
		}
		return queued[a].ID < queued[b].ID
	})
	groups := map[string][]jobsdata.Job{}
	for _, j := range queued {
		groups[j.MachineID] = append(groups[j.MachineID], j)
	}
	out := make(map[int64]queueInfo, len(queued))
	for machine, jobs := range groups {
		var running []forecast.SlotJob
		for _, r := range open {
			if r.State != jobsdata.Running || (machine != "" && r.MachineID != machine) {
				continue
			}
			elapsed := 0.0
			if !r.StartedAt.IsZero() && now.After(r.StartedAt) {
				elapsed = now.Sub(r.StartedAt).Seconds()
			}
			running = append(running, forecast.SlotJob{Remaining: forecast.RemainingAfter(fc[r.ID], elapsed)})
		}
		wait := make([]forecast.SlotJob, len(jobs))
		for i, j := range jobs {
			wait[i] = forecast.SlotJob{Remaining: fc[j.ID]}
		}
		etas, known := forecast.SimulateQueue(slots[machine], running, wait)
		for i, j := range jobs {
			out[j.ID] = queueInfo{Position: i + 1, Eta: etas[i], Known: known[i]}
		}
	}
	return out
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

// ---- the needs graph

// graphEdges gives the edges of a run ([a, b]: b needs a, by node id), the needs of each node and where they came from.
func graphEdges(d jobsdata.RunDetail) (edges [][]string, needs map[string][]string, graph gen.RunGraph) {
	graph = gen.RunGraph{DynamicNodes: []string{}}
	var set map[[2]string]bool
	if d.Graph.OK {
		graph.Source = gen.RunGraphSource("workflow_file")
		set = fileEdges(d)
		for _, j := range d.Jobs {
			if dynamicJob(d.Graph, j) {
				graph.DynamicNodes = append(graph.DynamicNodes, j.Name)
			}
		}
		if len(graph.DynamicNodes) > 0 {
			graph.Note = ptr("Some jobs expand at run time, so only the ones that have started are shown.")
		}
	} else {
		graph.Source = gen.RunGraphSource("timing_overlap")
		set = reduceEdges(timingEdges(d.Jobs))
		graph.Note = ptr("The workflow file could not be read, so the order is taken from when the jobs ran.")
	}
	needs = map[string][]string{}
	edges = make([][]string, 0, len(set))
	for e := range set {
		edges = append(edges, []string{e[0], e[1]})
		needs[e[1]] = append(needs[e[1]], e[0])
	}
	sort.Slice(edges, func(a, b int) bool {
		if edges[a][0] != edges[b][0] {
			return edges[a][0] < edges[b][0]
		}
		return edges[a][1] < edges[b][1]
	})
	for k := range needs {
		sort.Strings(needs[k])
	}
	sort.Strings(graph.DynamicNodes)
	return edges, needs, graph
}

// inFile says whether a job is the workflow file's job id (its type, or its whole name when the file names no matrix).
func inFile(j jobsdata.Job, id string) bool { return j.JobType == id || j.Name == id }

func dynamicJob(g jobsdata.Graph, j jobsdata.Job) bool {
	for id, dyn := range g.Dynamic {
		if dyn && inFile(j, id) {
			return true
		}
	}
	return false
}

func fileEdges(d jobsdata.RunDetail) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, b := range d.Jobs {
		for id, needed := range d.Graph.Needs {
			if !inFile(b, id) {
				continue
			}
			for _, need := range needed {
				for _, a := range d.Jobs {
					if a.Name != b.Name && inFile(a, need) {
						out[[2]string{a.Name, b.Name}] = true
					}
				}
			}
		}
	}
	return out
}

// timingEdges: b needs a when a completed before b started.
func timingEdges(jobs []jobsdata.Job) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, a := range jobs {
		for _, b := range jobs {
			if a.Name == b.Name || a.CompletedAt.IsZero() || b.StartedAt.IsZero() {
				continue
			}
			if !a.CompletedAt.After(b.StartedAt) && a.StartedAt.Before(b.StartedAt) {
				out[[2]string{a.Name, b.Name}] = true
			}
		}
	}
	return out
}

// reduceEdges drops every edge a to b that a longer path from a reaches b by (the transitive reduction).
func reduceEdges(set map[[2]string]bool) map[[2]string]bool {
	next := map[string][]string{}
	for e := range set {
		next[e[0]] = append(next[e[0]], e[1])
	}
	reaches := func(from, to string, skip [2]string) bool {
		seen := map[string]bool{from: true}
		stack := []string{from}
		for len(stack) > 0 {
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, n := range next[at] {
				if (skip == [2]string{at, n}) || seen[n] {
					continue
				}
				if n == to {
					return true
				}
				seen[n] = true
				stack = append(stack, n)
			}
		}
		return false
	}
	out := map[[2]string]bool{}
	for e := range set {
		if !reaches(e[0], e[1], e) {
			out[e] = true
		}
	}
	return out
}

// ---- the critical path and the ETA of a run

// runNodes are the nodes of the critical path calculation. An open run counts what is left: nothing for a finished
// job, the rest of the forecast for a running one, the whole forecast and the wait in the queue for a queued one.
// A finished run counts the real times, so its critical path is the one that took the time.
func runNodes(d jobsdata.RunDetail, needs map[string][]string, fc map[int64]forecast.Forecast, qi map[int64]queueInfo, now time.Time) []forecast.Node {
	open := d.State != jobsdata.Done
	nodes := make([]forecast.Node, 0, len(d.Jobs))
	for _, j := range d.Jobs {
		n := forecast.Node{ID: j.Name, Needs: needs[j.Name]}
		switch {
		case j.State == jobsdata.Done && !open:
			s, _ := jobSeconds(j)
			n.Remaining = forecast.Forecast{P50: s, P25: s, P75: s, Basis: forecast.BasisJobKey}
		case j.State == jobsdata.Done:
			n.Remaining = forecast.Forecast{Basis: forecast.BasisJobKey}
		case j.State == jobsdata.Running:
			elapsed := 0.0
			if !j.StartedAt.IsZero() && now.After(j.StartedAt) {
				elapsed = now.Sub(j.StartedAt).Seconds()
			}
			n.Remaining = forecast.RemainingAfter(fc[j.ID], elapsed)
		default:
			n.Remaining = fc[j.ID]
			if q, ok := qi[j.ID]; ok && q.Known && n.Remaining.Basis != forecast.BasisNone {
				n.Remaining.P50 += q.Eta.P50
				n.Remaining.P25 += q.Eta.Lo
				n.Remaining.P75 += q.Eta.Hi
			}
		}
		nodes = append(nodes, n)
	}
	return nodes
}

// criticalPath gives the node ids of the path and, for an open run whose path has a forecast all along, the ETA.
func criticalPath(d jobsdata.RunDetail, nodes []forecast.Node) (path []string, eta *gen.Eta) {
	path, e, complete := forecast.CriticalPath(nodes)
	if path == nil {
		path = []string{}
	}
	if d.State != jobsdata.Done && complete && len(nodes) > 0 {
		eta = toEta(e)
	}
	return path, eta
}

// JobEventJSON is the `job` field of a job.queued, job.started or job.finished live event: the job as the
// console reads it in a list, with its run id and job id (observability R5.4).
func JobEventJSON(j jobsdata.Job) []byte {
	b, err := json.Marshal(toJobItem(j))
	if err != nil {
		return nil
	}
	return b
}
