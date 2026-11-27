package jobsdata

import "time"

// Follow-up runs (CIX24, ci-run-insights D27). A run started by another run's completion (event workflow_run, such
// as the Installers workflow after CI) is a follow-up of that run. The console shows a run as not finished while a
// follow-up is queued or running.

// EventWorkflowRun is the trigger of a follow-up run.
const EventWorkflowRun = "workflow_run"

// Link sources: how the parent of a follow-up is known.
const (
	LinkPayload  = "payload"  // GitHub named the triggering run
	LinkInferred = "inferred" // crsp matched head sha and completion time
)

// FollowUp is one run a run started next.
type FollowUp struct {
	RunID        int64
	Attempt      int
	WorkflowName string
	State        State // queued, running or done, from the follow-up's own conclusion or jobs
	Result       Result
	CreatedAt    time.Time
	StartedAt    time.Time
	CompletedAt  time.Time
	HTMLURL      string
}

// Open says whether the follow-up has not finished.
func (f FollowUp) Open() bool { return f.State != Done }

// RunCandidate is a run of one head sha as the link resolver sees it. CompletedAt is zero while the run is open.
type RunCandidate struct {
	RunID       int64
	CreatedAt   time.Time
	CompletedAt time.Time
}

// InferParent picks the run that started child among the other runs of the same repository and head sha. The
// parent has a lower run id and finished before the child was created; of those the one that finished last wins
// (the child is created seconds after its parent completes). A run whose completion is not stored yet counts only
// when it is the single possible parent: the child can arrive before the parent's completion does, and the link is
// recomputed when the completion arrives.
func InferParent(child RunCandidate, others []RunCandidate) (int64, bool) {
	var best RunCandidate
	var open []RunCandidate
	for _, o := range others {
		if o.RunID == child.RunID || o.RunID > child.RunID {
			continue
		}
		switch {
		case o.CompletedAt.IsZero():
			open = append(open, o)
		case child.CreatedAt.IsZero() || !o.CompletedAt.After(child.CreatedAt):
			if best.RunID == 0 || o.CompletedAt.After(best.CompletedAt) {
				best = o
			}
		}
	}
	if best.RunID != 0 {
		return best.RunID, true
	}
	if len(open) == 1 {
		return open[0].RunID, true
	}
	return 0, false
}

// Overall is the state a run shows when its follow-ups count.
type Overall string

const (
	OverallQueued          Overall = "queued"
	OverallRunning         Overall = "running"
	OverallFollowUpRunning Overall = "follow_up_running" // the run is done, a follow-up is queued or running
	OverallDone            Overall = "done"
)

// OverallOf gives the overall state of a run: the run's own state until it is done, then follow_up_running while
// any follow-up is queued or running. The run's own state and conclusion stay as they are.
func OverallOf(own State, followUps []FollowUp) Overall {
	switch own {
	case Queued:
		return OverallQueued
	case Running:
		return OverallRunning
	}
	for _, f := range followUps {
		if f.Open() {
			return OverallFollowUpRunning
		}
	}
	return OverallDone
}
