package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata/ingest"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// recordingStore keeps what ingest writes; every method ingest does not use is the nil interface's.
type recordingStore struct {
	jobsdata.Store
	mu    sync.Mutex
	jobs  map[int64]jobsdata.Job
	steps map[int64][]jobsdata.Step
	facts map[int64][]jobsdata.Facts
}

func newRecordingStore() *recordingStore {
	return &recordingStore{jobs: map[int64]jobsdata.Job{}, steps: map[int64][]jobsdata.Step{}, facts: map[int64][]jobsdata.Facts{}}
}

func (s *recordingStore) UpsertRun(context.Context, jobsdata.Run) error { return nil }
func (s *recordingStore) UpsertJob(_ context.Context, j jobsdata.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return nil
}
func (s *recordingStore) ReplaceSteps(_ context.Context, id int64, st []jobsdata.Step) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps[id] = st
	return nil
}
func (s *recordingStore) SetFacts(_ context.Context, id int64, f jobsdata.Facts) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.facts[id] = append(s.facts[id], f)
	return nil
}

func testWiring(t *testing.T, st *recordingStore) *jobsWiring {
	t.Helper()
	rec := ingest.New(ingest.Config{Writer: st, Reader: nil, Now: time.Now,
		Repos: func(_ context.Context, name string) (int64, bool) { return 5, name == "luis-mc/app" }})
	return &jobsWiring{rec: rec, worker: ingest.NewWorker(16, slog.New(slog.NewTextHandler(io.Discard, nil))),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), ctx: context.Background()}
}

const webhookJobBody = `{"action":"in_progress","workflow_job":{"id":42,"run_id":7,"run_attempt":1,"workflow_name":"CI",
"head_branch":"OPS34-x","head_sha":"abc","name":"test (a, 1)","status":"in_progress","conclusion":null,
"html_url":"https://github.com/luis-mc/app/actions/runs/7/job/42","created_at":"2026-11-18T09:00:00Z",
"started_at":"2026-11-18T09:00:05Z","runner_name":"crsp-1","labels":["self-hosted"],
"steps":[{"name":"Set up job","status":"completed","conclusion":"success","number":1,"started_at":"2026-11-18T09:00:05Z","completed_at":"2026-11-18T09:00:06Z"}]},
"repository":{"full_name":"luis-mc/app"}}`

// A webhook delivery is answered at once and ends in the recorder: job, steps, in order.
func TestWebhookDeliveryEndsInTheRecorder(t *testing.T) {
	st := newRecordingStore()
	w := testWiring(t, st)
	secret := []byte("s3cret")
	seen := false
	h := &github.WebhookHandler{
		Verifier:      func() (github.WebhookVerifier, error) { return github.NewWebhookVerifier(secret), nil },
		FirstDelivery: func(string, string) (bool, error) { first := !seen; seen = true; return first, nil },
		Events:        webhookEvents{ctx: context.Background(), logger: slog.New(slog.NewTextHandler(io.Discard, nil)), jobs: w},
	}
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/github", strings.NewReader(webhookJobBody))
	req.Header.Set("X-Hub-Signature-256", github.Sign(secret, []byte(webhookJobBody)))
	req.Header.Set("X-GitHub-Delivery", "d1")
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	w.Close() // the worker drains what was queued
	j, ok := st.jobs[42]
	if !ok || j.State != jobsdata.Running || j.JobType != "test" || j.Branch != "OPS34-x" || j.RunnerName != "crsp-1" || j.RepositoryID != 5 {
		t.Fatalf("job %+v", j)
	}
	if len(st.steps[42]) != 1 || st.steps[42][0].Name != "Set up job" || st.steps[42][0].State != "done" {
		t.Fatalf("steps %+v", st.steps[42])
	}
}

// The tee shows each job to the recorder and passes it on unchanged.
func TestTeeJobsPassesEveryJobOn(t *testing.T) {
	in := make(chan github.Job)
	var mu sync.Mutex
	var seen []int64
	out := teeJobs(context.Background(), in, func(j github.Job) { mu.Lock(); seen = append(seen, j.ID); mu.Unlock() })
	go func() {
		for i := int64(1); i <= 3; i++ {
			in <- github.Job{ID: i}
		}
		close(in)
	}()
	var got []int64
	for j := range out {
		got = append(got, j.ID)
	}
	if len(got) != 3 || got[2] != 3 || len(seen) != 3 {
		t.Fatalf("passed %v seen %v", got, seen)
	}
}

func TestTeeJobsStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	in := make(chan github.Job, 1)
	in <- github.Job{ID: 1}
	out := teeJobs(ctx, in, func(github.Job) {})
	cancel()
	select {
	case <-time.After(2 * time.Second):
		t.Fatal("tee did not stop")
	case _, ok := <-out:
		_ = ok // either the job or the close; the goroutine must end
	}
	close(in)
}

type oneRunner struct{}

func (oneRunner) List(context.Context, runners.Filter) ([]runners.Record, error) {
	return []runners.Record{{ID: "r_9", Name: "crsp-1", MachineID: "m1", Kind: "ephemeral", Backend: "container", Isolation: "container"}}, nil
}

// The scheduler's facts reach the store with the runner and machine facts added, and wait for a job seen later.
func TestSchedulerFactsAreEnrichedAndParked(t *testing.T) {
	st := newRecordingStore()
	w := testWiring(t, st)
	w.runners = oneRunner{}
	w.fact(scheduler.JobRef{Repository: "luis-mc/app", RunID: 7, RunAttempt: 1, Job: "test", RunnerName: "crsp-1"},
		jobsdata.Facts{MachineID: "m1", WaitReason: "free_slot", WaitText: "1 ahead in the queue"})
	w.Job(github.Job{ID: 42, RunID: 7, RunAttempt: 1, Repository: "luis-mc/app", Workflow: "CI", Name: "test", Status: "in_progress", RunnerName: "crsp-1", HeadSHA: "abc"})
	w.Close()
	fs := st.facts[42]
	if len(fs) != 1 {
		t.Fatalf("facts %+v", st.facts)
	}
	f := fs[0]
	if f.RunnerID != "r_9" || f.RunnerKind != jobsdata.Ephemeral || f.Backend != "container" || f.WaitReason != "free_slot" || f.MachineID != "m1" {
		t.Fatalf("fact %+v", f)
	}
}
