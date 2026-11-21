package agent

import (
	"context"
	"strconv"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/jobsample"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// JobSampling measures the jobs the gate starts (OPS66 R5.62). The gate tells it when a job starts and ends;
// every second it reads each running job through the reader of the operating system and sends the batches
// over the agent link. It costs under 1 percent of one CPU core (R6.4) and never fails or holds a job.
type JobSampling struct {
	Collector *jobsample.Collector
	Sender    *JobSampleSender
	// Jobs creates the Job Objects of Windows; nil elsewhere.
	Jobs jobsample.JobAttacher
	// Locate says where the processes of a started job live: the cgroup of a container or VM job, the process
	// id of the runner for a persistent one. Nil, or an empty answer, leaves the job unmeasured with a reason.
	Locate func(job machines.JobRef, backend string) jobsample.Target
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// NewJobSampling builds the sampling of an operating system's readers.
func NewJobSampling(p jobsample.Platform, locate func(machines.JobRef, string) jobsample.Target) *JobSampling {
	snd := &JobSampleSender{}
	return &JobSampling{
		Collector: &jobsample.Collector{
			Cgroup: p.Cgroup, ProcessTree: p.ProcessTree, JobObject: p.JobObject, Sender: snd, Enabled: snd.Enabled,
			Forget: forgetFunc(p),
		},
		Sender: snd, Jobs: p.Jobs, Locate: locate,
	}
}

func forgetFunc(p jobsample.Platform) func(jobsample.Target) {
	type forgetter interface{ Forget(jobsample.Target) }
	var all []forgetter
	for _, s := range []jobsample.Sampler{p.Cgroup, p.ProcessTree, p.JobObject} {
		if f, ok := s.(forgetter); ok {
			all = append(all, f)
		}
	}
	return func(t jobsample.Target) {
		for _, f := range all {
			f.Forget(t)
		}
	}
}

func (s *JobSampling) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var _ JobObserver = (*JobSampling)(nil)

func (s *JobSampling) target(job machines.JobRef, backend string) jobsample.Target {
	var t jobsample.Target
	if s.Locate != nil {
		t = s.Locate(job, backend)
	}
	t.RunnerID, t.Repository, t.RunID, t.RunAttempt, t.Job, t.JobID = job.RunnerName, job.Repository, job.RunID, job.RunAttempt, job.Job, job.GitHubJobID
	if s.Jobs != nil && t.JobObject == "" && t.RootPID > 0 {
		name := "crsp-job-" + job.RunnerName + "-" + strconv.FormatInt(job.GitHubJobID, 10)
		if err := s.Jobs.Attach(name, t.RootPID); err == nil {
			t.JobObject = name
		}
	}
	return t
}

// JobStarted implements JobObserver.
func (s *JobSampling) JobStarted(_ string, job machines.JobRef, backend string) {
	s.Collector.Start(s.target(job, backend), s.now())
}

// JobFinished implements JobObserver. The final batch goes out here; the gate does not wait for the link.
func (s *JobSampling) JobFinished(_ string, job machines.JobRef, cut bool) {
	t := jobsample.Target{RunnerID: job.RunnerName, Repository: job.Repository, RunID: job.RunID, RunAttempt: job.RunAttempt, Job: job.Job, JobID: job.GitHubJobID}
	s.Collector.Finish(context.Background(), t, s.now(), cut)
	if s.Jobs != nil {
		s.Jobs.Close("crsp-job-" + job.RunnerName + "-" + strconv.FormatInt(job.GitHubJobID, 10))
	}
}

// Run samples every second until ctx ends. A job still running then is finished as cut short, so the
// controller does not size from it.
func (s *JobSampling) Run(ctx context.Context, g *Gate) {
	g.SetObserver(s)
	tk := time.NewTicker(jobsample.Interval)
	defer tk.Stop()
	s.Collector.Run(ctx, tk.C)
	for _, k := range g.RunningKeys() {
		g.FinishCut(k)
	}
}
