package api

import (
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// CRSP38, machines R5.55 and R5.56: the change names the job that holds the
// drain, how long it has run and its usual duration, flags a job far past it,
// and lists the named choices, none of which stops a job.
func TestChangeShowsTheDrainHolderAndChoices(t *testing.T) {
	started := time.Now().Add(-45 * time.Minute)
	job := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "runner-1", StartedAt: started, GitHubJobID: 99}
	c := machines.Change{
		ID: "ch_1", MachineID: "m1", Kind: machines.KindRunnerUpdate, State: machines.ChangeWaiting, StartedAt: time.Now().Add(-time.Hour),
		Detail: machines.ChangeDetail{
			RunnerNames: []string{"runner-1"}, WaitingFor: []machines.JobRef{job},
			Holder: &machines.Holder{Job: job, Usual: 10 * time.Minute, OverUsual: true},
		},
	}
	out := changeToAPI(c)
	if out.Holder == nil || out.Holder.Job.JobName != "build" || !out.Holder.OverUsual ||
		out.Holder.RunningSeconds < 44*60 || out.Holder.UsualSeconds == nil || *out.Holder.UsualSeconds != 600 {
		t.Fatalf("holder = %+v", out.Holder)
	}
	if out.RunnerNames == nil || (*out.RunnerNames)[0] != "runner-1" {
		t.Fatalf("runner names = %v", out.RunnerNames)
	}
	if out.Choices == nil || len(*out.Choices) != 2 || (*out.Choices)[0] != gen.MachineChangeChoicesWaitLonger || (*out.Choices)[1] != gen.MachineChangeChoicesCancel {
		t.Fatalf("choices = %v", out.Choices)
	}
	// A quiet drain offers no choice yet and no holder flag.
	c.Detail.Holder = &machines.Holder{Job: job}
	out = changeToAPI(c)
	if out.Choices != nil || out.Holder.OverUsual || out.Holder.UsualSeconds != nil {
		t.Fatalf("quiet drain: %+v", out)
	}
}
