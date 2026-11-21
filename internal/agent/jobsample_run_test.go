package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/jobsample"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

type fakeJobs struct{ c jobsample.JobCounters }

func (f *fakeJobs) Query(string) (jobsample.JobCounters, error) { return f.c, nil }

type fakeAttacher struct {
	mu             sync.Mutex
	attached, shut []string
	fail           bool
}

func (f *fakeAttacher) Attach(name string, _ int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return context.DeadlineExceeded
	}
	f.attached = append(f.attached, name)
	return nil
}

func (f *fakeAttacher) Close(name string) { f.mu.Lock(); f.shut = append(f.shut, name); f.mu.Unlock() }

// OPS66 R5.62: a started job is attached to a Job Object (Windows path), sampled with the fake clock, and
// its final batch carries the outcome; a cut short job says so. No sleep: the clock is a variable.
func TestJobSamplingStartSampleFinish(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	q := &fakeJobs{}
	at := &fakeAttacher{}
	p := jobsample.Platform{JobObject: jobsample.NewJobObjectSampler(q), Jobs: at}
	js := NewJobSampling(p, func(machines.JobRef, string) jobsample.Target { return jobsample.Target{RootPID: 42} })
	js.Now = func() time.Time { return now }
	var sent []*agentv1.SessionRequest
	defer js.Sender.Attach(func(r *agentv1.SessionRequest) error { sent = append(sent, r); return nil }, true)()

	job := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", GitHubJobID: 9, RunnerName: "r1"}
	js.JobStarted("k", job, "persistent")
	if len(at.attached) != 1 || at.attached[0] != "crsp-job-r1-9" {
		t.Fatalf("attached %v", at.attached)
	}
	q.c = jobsample.JobCounters{UserTime100ns: 20_000_000, PeakMemory: 4096} // 2 s CPU in 2 s: 1 core
	now = now.Add(2 * time.Second)
	js.Collector.Tick(context.Background(), now)
	q.c.Terminated = 1
	now = now.Add(time.Second)
	js.JobFinished("k", job, true)
	if len(at.shut) != 1 {
		t.Fatal("job object not closed")
	}
	if len(sent) != 1 {
		t.Fatalf("sent %d", len(sent))
	}
	m := sent[0].GetJobResourceSamples()
	if !m.GetFinal() || !m.GetOomKilled() || !m.GetCutShort() || m.GetMemoryPeakBytes() != 4096 || m.GetSource() != "job_object" ||
		m.GetJob().GetRepository() != "o/r" || m.GetJob().GetJob() != "build" || len(m.GetSamples()) != 2 {
		t.Fatalf("%+v", m)
	}
}

// OPS66 R5.62: a job nothing can read still ends with a final batch that says why.
func TestJobSamplingUnreadableJob(t *testing.T) {
	js := NewJobSampling(jobsample.Platform{}, nil)
	var sent []*agentv1.SessionRequest
	defer js.Sender.Attach(func(r *agentv1.SessionRequest) error { sent = append(sent, r); return nil }, true)()
	job := machines.JobRef{RunnerName: "r2", GitHubJobID: 3}
	js.JobStarted("k", job, "persistent")
	js.JobFinished("k", job, false)
	if len(sent) != 1 || !sent[0].GetJobResourceSamples().GetFinal() || sent[0].GetJobResourceSamples().GetUnknownReason() == "" {
		t.Fatalf("%+v", sent)
	}
}

// OPS66: a controller on protocol version 3 gets nothing.
func TestJobSamplingOldController(t *testing.T) {
	js := NewJobSampling(jobsample.Platform{}, nil)
	var n int
	defer js.Sender.Attach(func(*agentv1.SessionRequest) error { n++; return nil }, false)()
	job := machines.JobRef{RunnerName: "r"}
	js.JobStarted("k", job, "persistent")
	js.JobFinished("k", job, false)
	if n != 0 {
		t.Fatal("sent to a v3 controller")
	}
}
