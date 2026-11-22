package assign

import (
	"context"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
)

// Gate is the listener gate of persistent runners on the machine itself (runners
// R5.134, package listenergate). When admission admits a job on a machine, the
// controller asks the gate before it makes a JIT runner: in enforce mode a gated
// persistent runner of that machine takes the job (its label is added and its
// listener started); in observe mode the gate only writes would_start and the
// controller goes on as before.
type Gate interface {
	// Admit offers an admitted job to the gated runners of its machine. ok is
	// true when one takes the job; then no JIT runner is made.
	Admit(ctx context.Context, j GateJob) (GateRunner, bool, error)
	// Holds is true while the gate keeps a label or a listener for the job.
	Holds(ctx context.Context, jobID int64) bool
	// Done is called when the job ended or is gone: stop the listener, remove
	// the label. A job with no gate lease changes nothing.
	Done(ctx context.Context, jobID int64) error
}

// GateJob is an admitted job as the gate needs it.
type GateJob struct {
	JobID      int64
	Repository string
	JobName    string
	JobLabel   string
	MachineID  string
	OS         string
	Labels     []string
}

// GateRunner is the runner that took the job.
type GateRunner struct {
	Name string
}

// gateAdmit offers the job to the gate. taken is true when a gated runner took
// it; stop is true when launch must end here (taken, or the gate failed and the
// job waits for its next try).
func (c *Controller) gateAdmit(ctx context.Context, l *Lease, v *machineView, now time.Time) (taken, stop bool, err error) {
	if c.Gate == nil {
		return false, false, nil
	}
	r, ok, gerr := c.Gate.Admit(ctx, GateJob{JobID: l.JobID, Repository: l.Repository, JobName: l.JobName,
		JobLabel: l.JobLabel, MachineID: v.m.ID, OS: l.OS, Labels: l.Labels})
	if gerr != nil {
		l.Fails++
		l.NextTry = now.Add(backoff(l.Fails))
		l.ReasonCode = CodeBackoff
		l.Reason = fmt.Sprintf("could not start the gated runner on %s: %v; trying again at %s", v.m.Name, gerr, l.NextTry.Format("15:04:05"))
		l.UpdatedAt = now
		return false, true, c.Store.Save(ctx, *l)
	}
	if !ok {
		return false, false, nil
	}
	begun, err := c.Store.Begin(ctx, l.JobID, v.m.ID, r.Name, now)
	if err != nil || !begun {
		return false, true, err
	}
	// The record stays empty: a persistent runner is never finished or deleted
	// as a job runner; the gate stops its listener (runners R5.134).
	l.State, l.MachineID, l.RunnerName, l.RunnerRecord, l.GitHubRunner = Starting, v.m.ID, r.Name, "", 0
	l.StartedAt, l.ReservedSince, l.NextTry, l.Fails = now, time.Time{}, time.Time{}, 0
	l.Reason, l.ReasonCode, l.UpdatedAt = fmt.Sprintf("starting the listener of %s on %s", r.Name, v.m.Name), CodeStarting, now
	if err := c.Store.Save(ctx, *l); err != nil {
		return false, true, err
	}
	v.held = append(v.held, scheduler.RoomHeld{Req: l.Req})
	return true, true, nil
}

// gateDone tells the gate a job ended. A failure is retried by the gate's sweep.
func (c *Controller) gateDone(ctx context.Context, jobID int64) {
	if c.Gate != nil {
		_ = c.Gate.Done(ctx, jobID)
	}
}
