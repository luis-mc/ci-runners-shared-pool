package main

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/forecast"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/ingest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// jobsIngestQueue is how many job events wait for the ingest worker. A webhook
// answers GitHub at once whatever the store is doing; past this the event is
// dropped and the poller fills the job in.
const jobsIngestQueue = 2048

// repoLookupTTL is how long a repository answer is reused by ingest.
const repoLookupTTL = time.Minute

// jobsWiring feeds the Jobs section: the webhook, the demand loop and the
// scheduler's facts all end in one Recorder, through one worker, so events keep
// their order and never hold the caller.
type jobsWiring struct {
	rec    *ingest.Recorder
	worker *ingest.Worker
	log    *slog.Logger
	ctx    context.Context

	mu      sync.Mutex
	runners runnerReader
	names   machineNames
}

// runnerReader reads the runner records of a machine (runners.Service does).
type runnerReader interface {
	List(ctx context.Context, f runners.Filter) ([]runners.Record, error)
}

// machineNames lists machines for their names (machines.Service does, through machineSource).
type machineNames = machineSource

// newJobsWiring makes the ingest for a store. forecasts may be nil.
func newJobsWiring(ctx context.Context, logger *slog.Logger, st jobsdata.Store, gh *github.REST, rs repos.Store, now func() time.Time) *jobsWiring {
	lookup := &repoLookup{store: rs}
	rec := ingest.New(ingest.Config{Writer: st, Reader: st, Forecast: jobForecaster(st, now),
		Graphs: workflowSource{gh: gh, repos: lookup}, Repos: lookup.id, Now: now, Log: logger, Context: ctx,
		OnJobCompleted: insightsBridge.Trigger})
	return &jobsWiring{rec: rec, worker: ingest.NewWorker(jobsIngestQueue, logger), log: logger, ctx: ctx}
}

// Close lets queued events finish and waits for the graph reads.
func (w *jobsWiring) Close() {
	w.worker.Stop()
	w.rec.Wait()
}

// Job records a job from the webhook, the demand loop, the poller or the reconcile. It never blocks.
func (w *jobsWiring) Job(j github.Job) {
	w.worker.Submit(func() {
		if err := w.rec.OnJob(w.ctx, j); err != nil && w.ctx.Err() == nil {
			w.log.Warn("record a job", "job", j.ID, "err", err)
		}
	})
}

// JobDetail records the steps of a workflow_job event; it runs after Job in the same queue.
func (w *jobsWiring) JobDetail(ev github.JobEvent) {
	if len(ev.Steps) == 0 {
		return
	}
	w.worker.Submit(func() {
		if err := w.rec.OnSteps(w.ctx, ev.Job.ID, ev.Steps); err != nil && w.ctx.Err() == nil {
			w.log.Warn("record job steps", "job", ev.Job.ID, "err", err)
		}
	})
}

// attach hooks the scheduler's facts, with the runner and machine names the
// controller knows. A queue that cannot report facts is left alone.
func (w *jobsWiring) attach(q scheduler.SlotQueue, rr runnerReader, names machineNames) {
	w.mu.Lock()
	w.runners, w.names = rr, names
	w.mu.Unlock()
	if s, ok := q.(interface{ SetFacts(scheduler.FactsFunc) }); ok {
		s.SetFacts(w.fact)
	}
}

// fact queues the facts of one job. The runner and machine lookups run on the worker, never on the scheduler's path.
func (w *jobsWiring) fact(job scheduler.JobRef, f jobsdata.Facts) {
	w.worker.Submit(func() {
		ref := ingest.Ref{Repository: job.Repository, RunID: job.RunID, Attempt: job.RunAttempt, Job: job.Job,
			RunnerName: job.RunnerName, JobID: job.GitHubJobID}
		f = w.enrich(w.ctx, job, f)
		if err := w.rec.OnJobFacts(w.ctx, ref, f); err != nil && w.ctx.Err() == nil {
			w.log.Warn("record job facts", "job", job.GitHubJobID, "run", job.RunID, "err", err)
		}
	})
}

// enrich adds the machine name and the runner's id, kind, backend and isolation.
func (w *jobsWiring) enrich(ctx context.Context, job scheduler.JobRef, f jobsdata.Facts) jobsdata.Facts {
	w.mu.Lock()
	rr, names := w.runners, w.names
	w.mu.Unlock()
	if f.MachineID != "" && names != nil {
		if vs, err := names.List(ctx); err == nil {
			for _, v := range vs {
				if v.Machine.ID == f.MachineID {
					f.MachineName = v.Machine.Name
				}
			}
		}
	}
	if job.RunnerName == "" || f.MachineID == "" {
		return f
	}
	f.RunnerName = job.RunnerName
	if rr == nil {
		return f
	}
	if rs, err := rr.List(ctx, runners.Filter{MachineID: f.MachineID}); err == nil {
		for _, r := range rs {
			if r.Name == job.RunnerName {
				f.RunnerID, f.Backend, f.Isolation = r.ID, r.Backend, r.Isolation
				switch r.Kind {
				case "persistent":
					f.RunnerKind = jobsdata.Persistent
				case "ephemeral":
					f.RunnerKind = jobsdata.Ephemeral
				}
			}
		}
	}
	return f
}

// teeJobs passes every job of in to out unchanged and shows it to fn on the way,
// so the demand loop is not slowed: fn must not block (jobsWiring.Job queues).
func teeJobs(ctx context.Context, in <-chan github.Job, fn func(github.Job)) <-chan github.Job {
	out := make(chan github.Job)
	go func() {
		defer close(out)
		for j := range in {
			fn(j)
			select {
			case out <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// repoLookup answers which repositories crsp serves (the pool is on) and their
// installation, with a short memory so a burst of events reads the list once.
type repoLookup struct {
	store repos.Store
	mu    sync.Mutex
	seen  map[string]repoAnswer
}

type repoAnswer struct {
	repo repos.Repository
	ok   bool
	at   time.Time
}

func (l *repoLookup) find(ctx context.Context, fullName string) (repos.Repository, bool) {
	l.mu.Lock()
	if a, ok := l.seen[fullName]; ok && time.Since(a.at) < repoLookupTTL {
		l.mu.Unlock()
		return a.repo, a.ok
	}
	l.mu.Unlock()
	on := true
	rs, err := l.store.ListRepositories(ctx, repos.ListFilter{Pool: &on, Query: fullName})
	if err != nil {
		return repos.Repository{}, false
	}
	var out repos.Repository
	found := false
	for _, r := range rs {
		if strings.EqualFold(r.FullName, fullName) {
			out, found = r, true
		}
	}
	l.mu.Lock()
	if l.seen == nil || len(l.seen) > 1000 {
		l.seen = map[string]repoAnswer{}
	}
	l.seen[fullName] = repoAnswer{out, found, time.Now()}
	l.mu.Unlock()
	return out, found
}

func (l *repoLookup) id(ctx context.Context, fullName string) (int64, bool) {
	r, ok := l.find(ctx, fullName)
	return r.ID, ok
}

// workflowSource reads a workflow file with the shared REST client: the
// installation token, the contents API, and the background mark that stops the
// call when half of the hourly limit is used.
type workflowSource struct {
	gh    *github.REST
	repos *repoLookup
}

var errNotServed = errors.New("repository is not served")

func (s workflowSource) target(ctx context.Context, repository string) (inst int64, owner, name string, err error) {
	r, ok := s.repos.find(ctx, repository)
	if !ok {
		return 0, "", "", errNotServed
	}
	return r.InstallationID, r.Owner, r.Name, nil
}

func (s workflowSource) WorkflowPath(ctx context.Context, repository string, runID int64) (string, error) {
	inst, owner, name, err := s.target(ctx, repository)
	if err != nil {
		return "", err
	}
	return s.gh.GetRunWorkflowPath(github.Background(ctx), inst, owner, name, runID)
}

func (s workflowSource) WorkflowFile(ctx context.Context, repository, path, headSHA string) ([]byte, error) {
	inst, owner, name, err := s.target(ctx, repository)
	if err != nil {
		return nil, err
	}
	return s.gh.GetFileAt(github.Background(ctx), inst, owner, name, path, headSHA)
}

// jobForecaster makes the forecast of a job that starts: the job key's history,
// else the workflow's, else the repository's (internal/forecast). No history gives none.
func jobForecaster(rd jobsdata.Reader, now func() time.Time) ingest.Forecaster {
	return func(ctx context.Context, j jobsdata.Job) (jobsdata.ForecastRecord, bool) {
		at := now()
		since := at.Add(-forecast.MaxAge)
		get := func(q jobsdata.SampleQuery) []jobsdata.Sample {
			q.RepositoryID, q.Since, q.Limit = j.RepositoryID, since, forecast.Window
			s, err := rd.Samples(ctx, q)
			if err != nil {
				return nil
			}
			return s
		}
		c := forecast.Chain{JobKey: get(jobsdata.SampleQuery{JobKey: j.JobKey}), Repository: get(jobsdata.SampleQuery{})}
		if j.Workflow != "" {
			c.Workflow = get(jobsdata.SampleQuery{Workflow: j.Workflow, JobType: j.JobType})
		}
		f := forecast.ForChain(c, at, "")
		if f.Basis == forecast.BasisNone {
			return jobsdata.ForecastRecord{}, false
		}
		ms := func(s float64) int64 { return int64(s * 1000) }
		return jobsdata.ForecastRecord{LoMS: ms(f.Lo), MidMS: ms(f.P50), HiMS: ms(f.Hi), Basis: string(f.Basis), Samples: f.Samples}, true
	}
}
