package scheduler

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// ErrNoFreeSlot is returned by Place when the machine has no free job slot.
// Nothing was recorded; the job stays queued and the caller tries again when a
// slot is freed (scheduling R5.50).
var ErrNoFreeSlot = errors.New("no free job slot on this machine")

// StartRunnerWork is the job_queue kind of the work that tells the machine's
// agent to start the ephemeral runner for a placed job.
const StartRunnerWork = "start_runner"

// Placement is a job placed on a machine: the slot it took and the work row
// that starts its runner.
type Placement struct {
	MachineID string
	Job       JobRef
	Grant     Grant
	// Existing is true when the job was already placed and nothing changed.
	Existing bool
}

// startRunnerPayload is the job_queue payload. It names the job, never the
// slot claim: the claim stays with the controller and the slot it frees
// (scheduling R5.15). It holds no secret.
type startRunnerPayload struct {
	Machine     string `json:"machine"`
	Repository  string `json:"repository"`
	RunID       int64  `json:"run_id"`
	RunAttempt  int    `json:"run_attempt"`
	Job         string `json:"job"`
	GitHubJobID int64  `json:"github_job_id,omitempty"`
	Slot        int    `json:"slot"`
}

// Place takes a job slot on the chosen machine for an ephemeral runner and
// enqueues the start-runner work for that machine's agent, in one transaction
// (scheduling R5.50). Either the slot, its log row and the work row all exist,
// or none does. The same job placed twice is one placement. The slot is freed
// when GitHub reports the job completed (CompletedOnGitHub) or the runner is
// gone (RunnerGone). Stale slots are freed first, as for a gate (R5.16).
func (e *Engine) Place(ctx context.Context, machineID string, job JobRef) (Placement, error) {
	if machineID == "" {
		return Placement{}, errNoMachine
	}
	var out Placement
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		f, err := e.staleSlots(tx, machineID, now)
		if err != nil {
			return err
		}
		freed = f
		all, err := tx.Slots(machineID)
		if err != nil {
			return err
		}
		sj := toJob(job)
		for _, s := range jobSlots(all, sj) {
			if s.Kind == slots.KindCI {
				out = Placement{MachineID: machineID, Job: job, Grant: grantOf(s), Existing: true}
				return nil
			}
		}
		set, err := tx.Settings(machineID)
		if err != nil {
			return err
		}
		number, _ := freeNumber(all, slots.KindCI, set.JobSlots)
		if number == 0 {
			return ErrNoFreeSlot
		}
		s, ok, err := e.takeSlot(tx, GateRequest{MachineID: machineID, Job: job}, slots.KindCI, number, now, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoFreeSlot
		}
		payload, err := json.Marshal(startRunnerPayload{Machine: machineID, Repository: job.Repository, RunID: job.RunID,
			RunAttempt: job.RunAttempt, Job: job.Job, GitHubJobID: job.GitHubJobID, Slot: s.Number})
		if err != nil {
			return err
		}
		if err := tx.Enqueue(StartRunnerWork, string(payload)); err != nil {
			return err
		}
		out = Placement{MachineID: machineID, Job: job, Grant: grantOf(s)}
		return nil
	})
	e.notifyFreed(freed)
	if err != nil {
		return Placement{}, err
	}
	if !out.Existing {
		light := false
		e.emit(job, "go", out.Grant.TakenAt.String(), jobsdata.Facts{MachineID: machineID, SlotAt: out.Grant.TakenAt, Light: &light})
	}
	return out, nil
}

// RunnerGone frees the slot of a placed job whose ephemeral runner is gone
// (scheduling R5.50): the runner was removed or never came online. It wakes
// waiting gates in the same transaction. A runner with no slot frees nothing.
func (e *Engine) RunnerGone(ctx context.Context, machineID, runnerName string) ([]Freed, error) {
	if machineID == "" || runnerName == "" {
		return nil, errNoMachine
	}
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		all, err := tx.Slots(machineID)
		if err != nil {
			return err
		}
		for _, s := range all {
			if s.Job.RunnerName != runnerName {
				continue
			}
			ok, err := tx.FreeSlot(s.MachineID, s.Kind, s.Number, s.Claim)
			if err != nil {
				return err
			}
			if ok {
				freed = append(freed, e.freedRow(tx, s, slots.EventFreed, "runner-gone", now))
			}
		}
		if len(freed) > 0 {
			return tx.Enqueue("wake_gates", `{"machine":"`+machineID+`"}`)
		}
		return nil
	})
	e.notifyFreed(freed)
	return freed, err
}
