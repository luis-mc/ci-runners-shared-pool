package ingest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// fakeStore is an in-memory Writer and the part of Reader ingest reads.
type fakeStore struct {
	jobsdata.Reader
	mu        sync.Mutex
	runs      []jobsdata.Run
	jobs      map[int64]jobsdata.Job
	upserts   int
	steps     map[int64][]jobsdata.Step
	facts     map[int64][]jobsdata.Facts
	forecasts map[int64]jobsdata.ForecastRecord
	graphs    map[string]jobsdata.Graph
	puts      int
}

func newStore() *fakeStore {
	return &fakeStore{jobs: map[int64]jobsdata.Job{}, steps: map[int64][]jobsdata.Step{}, facts: map[int64][]jobsdata.Facts{},
		forecasts: map[int64]jobsdata.ForecastRecord{}, graphs: map[string]jobsdata.Graph{}}
}

func (s *fakeStore) UpsertRun(_ context.Context, r jobsdata.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs = append(s.runs, r)
	return nil
}

func (s *fakeStore) UpsertJob(_ context.Context, j jobsdata.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	s.upserts++
	return nil
}

func (s *fakeStore) ReplaceSteps(_ context.Context, id int64, st []jobsdata.Step) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps[id] = st
	return nil
}

func (s *fakeStore) SetFacts(_ context.Context, id int64, f jobsdata.Facts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts[id] = append(s.facts[id], f)
	return nil
}

func (s *fakeStore) SetForecast(_ context.Context, id int64, f jobsdata.ForecastRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forecasts[id] = f
	return nil
}

func gkey(repo int64, wf, sha string) string { return string(rune('0'+repo)) + "|" + wf + "|" + sha }

func (s *fakeStore) GetGraph(_ context.Context, repo int64, wf, sha string) (jobsdata.Graph, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.graphs[gkey(repo, wf, sha)]
	return g, ok, nil
}

func (s *fakeStore) PutGraph(_ context.Context, g jobsdata.Graph) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	s.graphs[gkey(g.RepositoryID, g.Workflow, g.HeadSHA)] = g
	return nil
}

func (s *fakeStore) Prune(context.Context, time.Time, jobsdata.Retention) (jobsdata.Pruned, error) {
	return jobsdata.Pruned{}, nil
}

func (s *fakeStore) GetJob(_ context.Context, id int64) (jobsdata.JobDetail, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return jobsdata.JobDetail{}, false, nil
	}
	fr, has := s.forecasts[id]
	if has {
		j.Forecast = &fr
	}
	return jobsdata.JobDetail{Job: j}, true, nil
}

type fakeGraphs struct {
	mu        sync.Mutex
	pathCalls int
	fileCalls int
	file      []byte
	err       error
	path      string
}

func (g *fakeGraphs) WorkflowPath(context.Context, string, int64) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.pathCalls++
	return g.path, g.err
}

func (g *fakeGraphs) WorkflowFile(context.Context, string, string, string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fileCalls++
	return g.file, g.err
}

func newRecorder(st *fakeStore, g GraphSource, f Forecaster) *Recorder {
	return New(Config{Writer: st, Reader: st, Graphs: g, Forecast: f, Now: func() time.Time { return time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC) },
		Repos: func(_ context.Context, name string) (int64, bool) {
			if name == "luis-mc/app" {
				return 7, true
			}
			return 0, false
		}})
}

var t0 = time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

func ghJob(id int64, status string) github.Job {
	return github.Job{ID: id, RunID: 100, RunAttempt: 1, Repository: "luis-mc/app", Workflow: "CI", Name: "test (ubuntu, 22)",
		HeadSHA: "abc", HeadBranch: "OPS34-jobs-section", Status: status, CreatedAt: t0, HTMLURL: "https://github.com/luis-mc/app/actions/runs/100/job/" + "1",
		Labels: []string{"self-hosted"}}
}

func TestOnJobMapsAndStatesNeverMoveBack(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	ctx := context.Background()
	if err := r.OnJob(ctx, ghJob(1, "queued")); err != nil {
		t.Fatal(err)
	}
	j := st.jobs[1]
	if j.State != jobsdata.Queued || j.JobType != "test" || len(j.Matrix) != 2 || j.Matrix[1] != "22" || j.RepositoryID != 7 {
		t.Fatalf("job %+v", j)
	}
	if j.JobKey != "luis-mc/app / CI / test (ubuntu, 22)" {
		t.Fatalf("key %q", j.JobKey) // the workflow name until the file path is known
	}
	run := st.runs[0]
	if run.TaskID != "OPS34" || run.Branch != "OPS34-jobs-section" || run.HTMLURL != "https://github.com/luis-mc/app/actions/runs/100" || run.Attempt != 1 {
		t.Fatalf("run %+v", run)
	}
	done := ghJob(1, "completed")
	done.Conclusion, done.StartedAt, done.CompletedAt = "failure", t0.Add(time.Minute), t0.Add(5*time.Minute)
	_ = r.OnJob(ctx, done)
	if j = st.jobs[1]; j.State != jobsdata.Done || j.Result != jobsdata.Failure || j.CompletedAt.IsZero() {
		t.Fatalf("done job %+v", j)
	}
	n := st.upserts
	_ = r.OnJob(ctx, ghJob(1, "in_progress")) // a late older event
	if st.upserts != n || st.jobs[1].State != jobsdata.Done {
		t.Fatalf("state moved back: %+v", st.jobs[1])
	}
}

func TestOnJobSkipsUnknownRepoAndKeepsKnownFields(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	ctx := context.Background()
	other := ghJob(9, "queued")
	other.Repository = "someone/else"
	_ = r.OnJob(ctx, other)
	if len(st.jobs) != 0 {
		t.Fatal("a repository crsp does not serve was recorded")
	}
	poll := ghJob(1, "queued")
	poll.Event, poll.WorkflowPath = "pull_request", ".github/workflows/ci.yml"
	_ = r.OnJob(ctx, poll)
	hook := ghJob(1, "in_progress") // partial: no event, no path
	hook.Partial, hook.StartedAt = true, t0.Add(time.Minute)
	_ = r.OnJob(ctx, hook)
	j := st.jobs[1]
	if j.Event != "pull_request" || j.Workflow != ".github/workflows/ci.yml" || j.State != jobsdata.Running || j.StartedAt.IsZero() {
		t.Fatalf("job %+v", j)
	}
	if j.JobKey != "luis-mc/app / .github/workflows/ci.yml / test (ubuntu, 22)" {
		t.Fatalf("key %q", j.JobKey)
	}
	if last := st.runs[len(st.runs)-1]; last.Event != "pull_request" || last.Workflow != ".github/workflows/ci.yml" {
		t.Fatalf("run %+v", last)
	}
}

func TestConclusions(t *testing.T) {
	for c, want := range map[string]jobsdata.Result{"success": jobsdata.Success, "failure": jobsdata.Failure, "cancelled": jobsdata.Cancelled,
		"timed_out": jobsdata.TimedOut, "skipped": "", "": ""} {
		if got := resultOf(c); got != want {
			t.Errorf("%q: %q want %q", c, got, want)
		}
	}
}

func TestForecastOnceWhenFirstRunning(t *testing.T) {
	st := newStore()
	calls := 0
	r := newRecorder(st, nil, func(context.Context, jobsdata.Job) (jobsdata.ForecastRecord, bool) {
		calls++
		return jobsdata.ForecastRecord{LoMS: 1, MidMS: 2, HiMS: 3, Basis: "job", Samples: 5}, true
	})
	ctx := context.Background()
	_ = r.OnJob(ctx, ghJob(1, "queued"))
	if calls != 0 {
		t.Fatal("forecast while queued")
	}
	_ = r.OnJob(ctx, ghJob(1, "in_progress"))
	_ = r.OnJob(ctx, ghJob(1, "in_progress"))
	if calls != 1 || st.forecasts[1].MidMS != 2 {
		t.Fatalf("calls %d forecast %+v", calls, st.forecasts[1])
	}
	_ = r.OnJob(ctx, ghJob(2, "completed")) // first seen already done: no forecast
	if calls != 1 {
		t.Fatal("forecast for a job first seen done")
	}
}

func TestForecastKeptAcrossRestart(t *testing.T) {
	st := newStore()
	st.forecasts[1] = jobsdata.ForecastRecord{MidMS: 9}
	st.jobs[1] = jobsdata.Job{ID: 1}
	calls := 0
	r := newRecorder(st, nil, func(context.Context, jobsdata.Job) (jobsdata.ForecastRecord, bool) {
		calls++
		return jobsdata.ForecastRecord{}, true
	})
	_ = r.OnJob(context.Background(), ghJob(1, "in_progress"))
	if calls != 0 || st.forecasts[1].MidMS != 9 {
		t.Fatalf("the stored forecast was replaced: calls %d", calls)
	}
}

func TestSteps(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	_ = r.OnJob(context.Background(), ghJob(1, "in_progress"))
	if err := r.OnSteps(context.Background(), 99, []github.JobStep{{Number: 1}}); err != nil || len(st.steps[99]) != 0 {
		t.Fatalf("steps of an unknown job were kept: %v", err)
	}
	steps := []github.JobStep{
		{Number: 1, Name: "Set up job", Status: "completed", Conclusion: "success", StartedAt: t0, CompletedAt: t0.Add(time.Second)},
		{Number: 2, Name: "Build", Status: "in_progress", StartedAt: t0.Add(time.Second)},
		{Number: 3, Name: "Deploy", Status: "queued"},
		{Number: 4, Name: "Skipped one", Status: "completed", Conclusion: "skipped"},
	}
	if err := r.OnSteps(context.Background(), 1, steps); err != nil {
		t.Fatal(err)
	}
	got := st.steps[1]
	want := []struct{ state, result string }{{"done", "success"}, {"running", ""}, {"queued", ""}, {"skipped", "skipped"}}
	if len(got) != 4 {
		t.Fatalf("steps %+v", got)
	}
	for i, w := range want {
		if got[i].State != w.state || got[i].Result != w.result || got[i].JobID != 1 {
			t.Errorf("step %d: %+v want %+v", i, got[i], w)
		}
	}
	if err := r.OnSteps(context.Background(), 1, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.steps[1]) != 4 {
		t.Fatal("empty steps replaced the stored ones")
	}
}

func TestFactsBeforeJobSeen(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	ctx := context.Background()
	ref := Ref{Repository: "luis-mc/app", RunID: 100, Attempt: 1, Job: "test", RunnerName: "crsp-1"}
	f := jobsdata.Facts{MachineID: "m1", WaitReason: "free_slot"}
	if err := r.OnJobFacts(ctx, ref, f); err != nil {
		t.Fatal(err)
	}
	if len(st.facts) != 0 {
		t.Fatal("facts written for an unseen job")
	}
	j := ghJob(1, "in_progress")
	j.RunnerName = "crsp-1"
	_ = r.OnJob(ctx, j)
	if len(st.facts[1]) != 1 || st.facts[1][0].MachineID != "m1" {
		t.Fatalf("parked facts not written: %+v", st.facts)
	}
	// Seen job: written at once, by runner name or by the one job of that name.
	_ = r.OnJobFacts(ctx, ref, jobsdata.Facts{Backend: "container"})
	if len(st.facts[1]) != 2 {
		t.Fatalf("facts %+v", st.facts)
	}
	// A job id given needs no lookup.
	_ = r.OnJobFacts(ctx, Ref{JobID: 55}, jobsdata.Facts{Isolation: "container"})
	if len(st.facts[55]) != 1 {
		t.Fatal("facts by job id")
	}
}

func TestFactsAmbiguousMatrixWaits(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	ctx := context.Background()
	a, b := ghJob(1, "queued"), ghJob(2, "queued")
	b.Name = "test (ubuntu, 24)"
	_ = r.OnJob(ctx, a)
	_ = r.OnJob(ctx, b)
	_ = r.OnJobFacts(ctx, Ref{Repository: "luis-mc/app", RunID: 100, Job: "test"}, jobsdata.Facts{MachineID: "m1"})
	if len(st.facts) != 0 {
		t.Fatalf("two matrix jobs, no runner: facts must wait, got %+v", st.facts)
	}
	a.RunnerName, a.Status = "crsp-1", "in_progress"
	_ = r.OnJob(ctx, a)
	_ = r.OnJobFacts(ctx, Ref{Repository: "luis-mc/app", RunID: 100, Job: "test", RunnerName: "crsp-1"}, jobsdata.Facts{MachineID: "m2"})
	if len(st.facts[1]) == 0 || st.facts[1][len(st.facts[1])-1].MachineID != "m2" {
		t.Fatalf("by runner name: %+v", st.facts)
	}
}

func TestLostIsDroppedForAFinishedJob(t *testing.T) {
	st := newStore()
	r := newRecorder(st, nil, nil)
	ctx := context.Background()
	done := ghJob(1, "completed")
	done.Conclusion = "success"
	_ = r.OnJob(ctx, done)
	_ = r.OnFacts(ctx, 1, jobsdata.Facts{Lost: true})
	if len(st.facts[1]) != 0 {
		t.Fatalf("a finished job was marked lost: %+v", st.facts[1])
	}
	_ = r.OnFacts(ctx, 2, jobsdata.Facts{Lost: true})
	if len(st.facts[2]) != 1 || !st.facts[2][0].Lost {
		t.Fatal("a lost job was not marked")
	}
}

const wf = `
name: CI
jobs:
  lint: {runs-on: x}
  test:
    needs: lint
    strategy: {matrix: {os: [a, b]}}
  ship:
    name: Ship it
    needs: [lint, test]
`

func TestGraphFetchedOncePerSHA(t *testing.T) {
	st := newStore()
	g := &fakeGraphs{file: []byte(wf), path: ".github/workflows/ci.yml"}
	r := newRecorder(st, g, nil)
	ctx := context.Background()
	for i := int64(1); i <= 4; i++ {
		_ = r.OnJob(ctx, ghJob(i, "queued"))
	}
	// The first commit's read finishes before the second commit's job comes in, so the
	// second read reuses the workflow path it found (one path lookup in all).
	r.Wait()
	other := ghJob(9, "queued")
	other.HeadSHA = "def"
	_ = r.OnJob(ctx, other)
	r.Wait()
	if g.pathCalls != 1 || g.fileCalls != 2 || st.puts != 2 {
		t.Fatalf("path %d file %d put %d: one path lookup, one file read and one put per sha", g.pathCalls, g.fileCalls, st.puts)
	}
	got := st.graphs[gkey(7, ".github/workflows/ci.yml", "abc")]
	if !got.OK || len(got.Needs["Ship it"]) != 2 || got.Needs["Ship it"][0] != "lint" || !got.Dynamic["test"] || got.Dynamic["lint"] {
		t.Fatalf("graph %+v", got)
	}
}

func TestGraphWithPathSkipsLookupAndStoredGraphSkipsFetch(t *testing.T) {
	st := newStore()
	st.graphs[gkey(7, ".github/workflows/ci.yml", "abc")] = jobsdata.Graph{OK: true}
	g := &fakeGraphs{file: []byte(wf)}
	r := newRecorder(st, g, nil)
	j := ghJob(1, "queued")
	j.WorkflowPath = ".github/workflows/ci.yml"
	_ = r.OnJob(context.Background(), j)
	r.Wait()
	if g.pathCalls != 0 || g.fileCalls != 0 {
		t.Fatalf("path %d file %d", g.pathCalls, g.fileCalls)
	}
}

func TestGraphFailureStoresNotOK(t *testing.T) {
	st := newStore()
	g := &fakeGraphs{err: errors.New("403"), path: ".github/workflows/ci.yml"}
	r := newRecorder(st, g, nil)
	j := ghJob(1, "queued")
	j.WorkflowPath = ".github/workflows/ci.yml"
	if err := r.OnJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	r.Wait()
	got, ok := st.graphs[gkey(7, ".github/workflows/ci.yml", "abc")]
	if !ok || got.OK {
		t.Fatalf("graph %+v %v", got, ok)
	}
	_ = r.OnJob(context.Background(), j)
	r.Wait()
	if g.fileCalls != 1 {
		t.Fatalf("a failed read was tried again: %d", g.fileCalls)
	}
}

func TestGraphUnparsableStoresNotOK(t *testing.T) {
	st := newStore()
	g := &fakeGraphs{file: []byte("jobs: [oops"), path: ".github/workflows/ci.yml"}
	r := newRecorder(st, g, nil)
	_ = r.OnJob(context.Background(), ghJob(1, "queued"))
	r.Wait()
	got := st.graphs[gkey(7, ".github/workflows/ci.yml", "abc")]
	if got.OK || got.Workflow == "" {
		t.Fatalf("graph %+v", got)
	}
}

func TestWorkerDropsWhenFullAndRunsInOrder(t *testing.T) {
	w := NewWorker(2, nil)
	block := make(chan struct{})
	var order []int
	var mu sync.Mutex
	started := make(chan struct{})
	w.Submit(func() { close(started); <-block })
	<-started
	ok1 := w.Submit(func() { mu.Lock(); order = append(order, 1); mu.Unlock() })
	ok2 := w.Submit(func() { mu.Lock(); order = append(order, 2); mu.Unlock() })
	if !ok1 || !ok2 || w.Submit(func() {}) {
		t.Fatal("queue bound")
	}
	close(block)
	w.Stop()
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("order %v", order)
	}
	if w.Submit(func() {}) {
		t.Fatal("submit after stop")
	}
}

// CIX23 (D18): a job reaching done tells the insights engine once; a repeated event and a queued job do not.
func TestOnJobCompletedCallsOnceAtDone(t *testing.T) {
	st := newStore()
	calls := 0
	r := New(Config{Writer: st, Reader: st, Now: func() time.Time { return t0 }, OnJobCompleted: func() { calls++ },
		Repos: func(_ context.Context, name string) (int64, bool) { return 7, name == "luis-mc/app" }})
	ctx := context.Background()
	_ = r.OnJob(ctx, ghJob(1, "queued"))
	if calls != 0 {
		t.Fatalf("a queued job is not a finished run: %d calls", calls)
	}
	done := ghJob(1, "completed")
	done.Conclusion, done.StartedAt, done.CompletedAt = "success", t0.Add(time.Minute), t0.Add(2*time.Minute)
	_ = r.OnJob(ctx, done)
	_ = r.OnJob(ctx, done) // GitHub redelivers
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}
