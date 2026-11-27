package ingest

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/reconcile"
)

// Forecaster makes the forecast of a job that just started, from the history
// the forecast package reads. false means there is not enough to say.
type Forecaster func(ctx context.Context, j jobsdata.Job) (jobsdata.ForecastRecord, bool)

// GraphSource reads a workflow file the way the GitHub integration allows: the
// REST client with its rate-limit handling, the contents API.
type GraphSource interface {
	// WorkflowPath gives the workflow file of a run, for a job that came by webhook.
	WorkflowPath(ctx context.Context, repository string, runID int64) (string, error)
	// WorkflowFile reads the file at a commit.
	WorkflowFile(ctx context.Context, repository, path, headSHA string) ([]byte, error)
}

// Config is what a Recorder needs.
type Config struct {
	Writer jobsdata.Writer
	Reader jobsdata.Reader
	// Forecast is called once, when a job first runs; nil stores no forecast.
	Forecast Forecaster
	// Graphs reads workflow files; nil fetches no graph.
	Graphs GraphSource
	// Origins and Classify record who ran a job and where its steps came from (R5.39, R5.40); nil skips both.
	Origins  OriginSink
	Classify reconcile.Classify
	// OnJobCompleted is called once, after the job row is written, when a job first reaches done: CI run
	// insights re-analyse after each CI run (ci-run-insights R5.44). It must not block; nil does nothing.
	OnJobCompleted func()
	// Repos gives the id of a repository crsp serves; false skips its jobs.
	Repos func(ctx context.Context, fullName string) (int64, bool)
	Now   func() time.Time
	Log   *slog.Logger
	// Context is the lifetime of the background graph reads; Background when nil.
	Context context.Context
}

// Recorder writes job data. All methods are safe for concurrent use.
type Recorder struct {
	cfg Config
	log *slog.Logger
	bg  context.Context

	mu         sync.Mutex
	jobs       *bounded[int64, jobsdata.Job]  // what was last written for a job, GitHub's side only
	forecasted *bounded[int64, struct{}]      // jobs whose forecast is settled
	runs       *bounded[runKey, *runInfo]     // runs seen, to resolve facts and to fill the run row
	paths      *bounded[pathKey, string]      // workflow name to file path
	parked     *bounded[runKey, []parkedFact] // facts that arrived before their job
	graphs     *bounded[graphKey, struct{}]   // graph reads started

	pendingReads int // graph reads started and not finished
	wg           sync.WaitGroup
	sem          chan struct{}
}

type runKey struct {
	repository string
	runID      int64
	attempt    int
}

type runInfo struct {
	run  jobsdata.Run
	jobs []jobsdata.Job
}

type pathKey struct{ repository, name string }

type graphKey struct {
	repositoryID int64
	workflow     string
	sha          string
}

type parkedFact struct {
	ref Ref
	f   jobsdata.Facts
	at  time.Time
}

// parkTTL is how long facts wait for their job to be seen.
const parkTTL = 30 * time.Minute

// New makes a Recorder.
func New(cfg Config) *Recorder {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	r := &Recorder{cfg: cfg, log: cfg.Log, bg: cfg.Context, sem: make(chan struct{}, 2),
		jobs: newBounded[int64, jobsdata.Job](20000), forecasted: newBounded[int64, struct{}](20000),
		runs: newBounded[runKey, *runInfo](4000), paths: newBounded[pathKey, string](2000),
		parked: newBounded[runKey, []parkedFact](2000), graphs: newBounded[graphKey, struct{}](4000)}
	if r.log == nil {
		r.log = slog.Default()
	}
	if r.bg == nil {
		r.bg = context.Background()
	}
	return r
}

// Wait blocks until the background graph reads have finished (tests, shutdown).
func (r *Recorder) Wait() { r.wg.Wait() }

func rank(s jobsdata.State) int {
	switch s {
	case jobsdata.Queued:
		return 1
	case jobsdata.Running:
		return 2
	case jobsdata.Done:
		return 3
	}
	return 0
}

func stateOf(status string) jobsdata.State {
	switch status {
	case "queued", "waiting", "requested", "pending":
		return jobsdata.Queued
	case "in_progress":
		return jobsdata.Running
	case "completed":
		return jobsdata.Done
	}
	return ""
}

// resultOf maps a GitHub conclusion. Conclusions with no result of ours (skipped,
// neutral, stale, action_required) give "": the job is done and has no result.
func resultOf(conclusion string) jobsdata.Result {
	switch conclusion {
	case "success":
		return jobsdata.Success
	case "failure", "startup_failure":
		return jobsdata.Failure
	case "cancelled":
		return jobsdata.Cancelled
	case "timed_out":
		return jobsdata.TimedOut
	}
	return ""
}

func attemptOf(a int) int {
	if a < 1 {
		return 1
	}
	return a
}

// OnJob records one job as GitHub reports it: from the webhook, the demand
// loop, the poller or the reconcile. A job of a repository crsp does not serve
// is skipped. A field the source does not carry keeps its earlier value, and an
// event older than what is known (a queued event after a running one) is dropped.
func (r *Recorder) OnJob(ctx context.Context, gj github.Job) error {
	state := stateOf(gj.Status)
	if gj.ID == 0 || gj.RunID == 0 || state == "" {
		return nil
	}
	repoID, ok := r.cfg.Repos(ctx, gj.Repository)
	if !ok {
		return nil
	}
	path := gj.WorkflowPath
	r.mu.Lock()
	if path != "" && gj.Workflow != "" {
		r.paths.put(pathKey{gj.Repository, gj.Workflow}, path)
	} else if p, ok := r.paths.get(pathKey{gj.Repository, gj.Workflow}); ok && path == "" {
		path = p
	}
	r.mu.Unlock()

	j := toJob(gj, repoID, path, state)
	r.mu.Lock()
	old, had := r.jobs.get(j.ID)
	firstDone := state == jobsdata.Done && (!had || old.State != jobsdata.Done)
	if had {
		if rank(j.State) < rank(old.State) {
			r.mu.Unlock()
			return nil
		}
		j = merge(old, j)
	}
	r.jobs.put(j.ID, j)
	rk := runKey{j.Repository, j.RunID, j.Attempt}
	run := r.noteRun(rk, j, gj)
	pending := r.takeParked(rk)
	r.mu.Unlock()

	if err := r.cfg.Writer.UpsertRun(ctx, run); err != nil {
		return err
	}
	if err := r.cfg.Writer.UpsertJob(ctx, j); err != nil {
		return err
	}
	if firstDone && r.cfg.OnJobCompleted != nil {
		r.cfg.OnJobCompleted()
	}
	r.forecast(ctx, j, state)
	r.applyParked(ctx, rk, pending)
	r.MaybeFetchGraph(repoID, j.Repository, j.Workflow, gj.Workflow, j.RunID, j.HeadSHA)
	return nil
}

func toJob(gj github.Job, repoID int64, path string, state jobsdata.State) jobsdata.Job {
	jobType, matrix := jobsdata.SplitName(gj.Name)
	wf := path
	if wf == "" {
		wf = gj.Workflow // the workflow's name until the file is known
	}
	j := jobsdata.Job{ID: gj.ID, RepositoryID: repoID, Repository: gj.Repository, RunID: gj.RunID,
		Attempt: attemptOf(gj.RunAttempt), Workflow: path, Name: gj.Name, JobType: jobType, Matrix: matrix,
		JobKey: jobsdata.MakeKey(gj.Repository, wf, gj.Name), Branch: gj.HeadBranch, HeadSHA: gj.HeadSHA,
		Event: gj.Event, Labels: gj.Labels, State: state, HTMLURL: gj.HTMLURL, RunnerName: gj.RunnerName,
		CreatedAt: gj.CreatedAt, StartedAt: gj.StartedAt, CompletedAt: gj.CompletedAt}
	if state == jobsdata.Done {
		j.Result = resultOf(gj.Conclusion)
	}
	return j
}

// merge lays new over old: a field new does not carry keeps old's value.
func merge(old, n jobsdata.Job) jobsdata.Job {
	str := func(o, v string) string {
		if v == "" {
			return o
		}
		return v
	}
	tm := func(o, v time.Time) time.Time {
		if v.IsZero() {
			return o
		}
		return v
	}
	n.Workflow, n.JobKey = str(old.Workflow, n.Workflow), str(old.JobKey, n.JobKey)
	if old.Workflow != "" && n.Workflow == old.Workflow { // a key made with the path stays
		n.JobKey = old.JobKey
		if n.JobKey == "" {
			n.JobKey = jobsdata.MakeKey(n.Repository, n.Workflow, n.Name)
		}
	}
	n.Branch, n.HeadSHA, n.Event = str(old.Branch, n.Branch), str(old.HeadSHA, n.HeadSHA), str(old.Event, n.Event)
	n.HTMLURL, n.RunnerName = str(old.HTMLURL, n.HTMLURL), str(old.RunnerName, n.RunnerName)
	if len(n.Labels) == 0 {
		n.Labels = old.Labels
	}
	n.CreatedAt, n.StartedAt, n.CompletedAt = tm(old.CreatedAt, n.CreatedAt), tm(old.StartedAt, n.StartedAt), tm(old.CompletedAt, n.CompletedAt)
	if n.Result == "" {
		n.Result = old.Result
	}
	return n
}

// noteRun merges the job into its run and gives the run row to write. Called with r.mu held.
func (r *Recorder) noteRun(rk runKey, j jobsdata.Job, gj github.Job) jobsdata.Run {
	ri, ok := r.runs.get(rk)
	if !ok {
		ri = &runInfo{}
		r.runs.put(rk, ri)
	}
	run := ri.run
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	run.RepositoryID, run.Repository, run.RunID, run.Attempt = j.RepositoryID, j.Repository, j.RunID, j.Attempt
	set(&run.Workflow, j.Workflow)
	set(&run.WorkflowName, gj.Workflow)
	set(&run.Event, j.Event)
	set(&run.Branch, j.Branch)
	set(&run.HeadSHA, j.HeadSHA)
	if run.Branch != "" {
		run.TaskID = jobsdata.TaskFromBranch(run.Branch)
	}
	if i := strings.Index(j.HTMLURL, "/job/"); i > 0 {
		run.HTMLURL = j.HTMLURL[:i]
	}
	if !j.CreatedAt.IsZero() && (run.CreatedAt.IsZero() || j.CreatedAt.Before(run.CreatedAt)) {
		run.CreatedAt = j.CreatedAt
	}
	if !j.StartedAt.IsZero() && (run.StartedAt.IsZero() || j.StartedAt.Before(run.StartedAt)) {
		run.StartedAt = j.StartedAt
	}
	ri.run = run
	replaced := false
	for i := range ri.jobs {
		if ri.jobs[i].ID == j.ID {
			ri.jobs[i], replaced = j, true
		}
	}
	if !replaced {
		ri.jobs = append(ri.jobs, j)
	}
	return run
}

// forecast stores the forecast the first time a job is seen running.
func (r *Recorder) forecast(ctx context.Context, j jobsdata.Job, state jobsdata.State) {
	if r.cfg.Forecast == nil || state != jobsdata.Running {
		return
	}
	r.mu.Lock()
	_, done := r.forecasted.get(j.ID)
	if !done {
		r.forecasted.put(j.ID, struct{}{})
	}
	r.mu.Unlock()
	if done {
		return
	}
	if r.cfg.Reader != nil {
		if d, ok, err := r.cfg.Reader.GetJob(ctx, j.ID); err == nil && ok && d.Forecast != nil {
			return // stored before a restart
		}
	}
	f, ok := r.cfg.Forecast(ctx, j)
	if !ok {
		return
	}
	if err := r.cfg.Writer.SetForecast(ctx, j.ID, f); err != nil {
		r.log.Warn("store a job forecast", "job", j.ID, "err", err)
	}
}

// OnSteps replaces the steps of a job with those of a workflow_job event.
func (r *Recorder) OnSteps(ctx context.Context, jobID int64, steps []github.JobStep) error {
	if jobID == 0 || len(steps) == 0 || !r.known(ctx, jobID) {
		return nil // a job of a repository crsp does not serve has no steps kept
	}
	out := make([]jobsdata.Step, 0, len(steps))
	for _, s := range steps {
		st := jobsdata.Step{JobID: jobID, Number: s.Number, Name: s.Name, StartedAt: s.StartedAt, CompletedAt: s.CompletedAt}
		switch s.Status {
		case "in_progress":
			st.State = "running"
		case "completed":
			st.State = "done"
			st.Result = s.Conclusion
			if s.Conclusion == "skipped" {
				st.State = "skipped"
			}
		default:
			st.State = "queued"
		}
		out = append(out, st)
	}
	if err := r.cfg.Writer.ReplaceSteps(ctx, jobID, out); err != nil {
		return err
	}
	if r.cfg.Origins != nil {
		return r.cfg.Origins.SetStepsSource(ctx, jobID, "events")
	}
	return nil
}

// known says whether the job was recorded, in this process or before it.
func (r *Recorder) known(ctx context.Context, jobID int64) bool {
	r.mu.Lock()
	_, ok := r.jobs.get(jobID)
	r.mu.Unlock()
	if ok || r.cfg.Reader == nil {
		return ok
	}
	_, ok, err := r.cfg.Reader.GetJob(ctx, jobID)
	return err == nil && ok
}

// OnFacts stores crsp's own facts for a GitHub job id.
func (r *Recorder) OnFacts(ctx context.Context, jobID int64, f jobsdata.Facts) error {
	if jobID == 0 {
		return nil
	}
	if f.Lost && r.finished(ctx, jobID) {
		f.Lost = false // GitHub already said how the job ended
	}
	if isEmpty(f) {
		return nil
	}
	return r.cfg.Writer.SetFacts(ctx, jobID, f)
}

func isEmpty(f jobsdata.Facts) bool {
	return f == jobsdata.Facts{}
}

// finished says whether the job is known to be done.
func (r *Recorder) finished(ctx context.Context, jobID int64) bool {
	r.mu.Lock()
	j, ok := r.jobs.get(jobID)
	r.mu.Unlock()
	if ok && j.State == jobsdata.Done {
		return true
	}
	if r.cfg.Reader != nil {
		if d, ok, err := r.cfg.Reader.GetJob(ctx, jobID); err == nil && ok && d.State == jobsdata.Done {
			return true
		}
	}
	return false
}

var errNoRepo = errors.New("ingest: facts need a repository")

// Ref names a job the way the controller's own code knows it: by the run, the
// job's name in the workflow and the runner that took it. JobID is set when known.
type Ref struct {
	Repository string
	RunID      int64
	Attempt    int
	Job        string // GITHUB_JOB, the job id in the workflow file
	RunnerName string
	JobID      int64
}

// OnJobFacts stores facts for a job named by a Ref. With no job id the job is
// found among the run's jobs seen so far; when it is not there yet the facts
// wait (up to half an hour) and are written when OnJob sees it.
func (r *Recorder) OnJobFacts(ctx context.Context, ref Ref, f jobsdata.Facts) error {
	if ref.JobID != 0 {
		return r.OnFacts(ctx, ref.JobID, f)
	}
	if ref.Repository == "" || ref.RunID == 0 {
		return errNoRepo
	}
	ref.Attempt = attemptOf(ref.Attempt)
	rk := runKey{ref.Repository, ref.RunID, ref.Attempt}
	r.mu.Lock()
	if id, ok := r.resolve(rk, ref); ok {
		r.mu.Unlock()
		return r.OnFacts(ctx, id, f)
	}
	prev, _ := r.parked.get(rk)
	now := r.cfg.Now()
	kept := prev[:0:0]
	for _, p := range prev {
		if now.Sub(p.at) < parkTTL {
			kept = append(kept, p)
		}
	}
	r.parked.put(rk, append(kept, parkedFact{ref: ref, f: f, at: now}))
	r.mu.Unlock()
	return nil
}

// resolve finds the job of a ref among the run's jobs seen. Called with r.mu held.
func (r *Recorder) resolve(rk runKey, ref Ref) (int64, bool) {
	ri, ok := r.runs.get(rk)
	if !ok {
		return 0, false
	}
	var byName []jobsdata.Job
	for _, j := range ri.jobs {
		if ref.RunnerName != "" && j.RunnerName == ref.RunnerName {
			return j.ID, true
		}
		if j.State != jobsdata.Done && (j.Name == ref.Job || j.JobType == ref.Job) {
			byName = append(byName, j)
		}
	}
	// A runner name that no job carries yet cannot pick among several jobs of
	// the same name; one candidate is certain.
	if len(byName) == 1 && (ref.RunnerName == "" || byName[0].RunnerName == "") {
		return byName[0].ID, true
	}
	return 0, false
}

// takeParked removes the parked facts of a run. Called with r.mu held.
func (r *Recorder) takeParked(rk runKey) []parkedFact {
	p, _ := r.parked.get(rk)
	if len(p) > 0 {
		r.parked.del(rk)
	}
	return p
}

func (r *Recorder) applyParked(ctx context.Context, rk runKey, pending []parkedFact) {
	now := r.cfg.Now()
	for _, p := range pending {
		if now.Sub(p.at) >= parkTTL {
			continue
		}
		if err := r.OnJobFacts(ctx, p.ref, p.f); err != nil {
			r.log.Warn("store parked job facts", "run", rk.runID, "err", err)
		}
	}
}
