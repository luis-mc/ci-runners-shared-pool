package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// Release frees every slot and ticket the job holds on the machine, of every
// kind, with the outcome ran (R5.28). When claims are given, a slot is freed
// only with its claim (R5.15). A job that holds nothing frees nothing.
//
// A job-completed hook that frees nothing means the job never took a slot: its
// job-started hook did not reach the gate (crsp missing, broken or cut off from
// its agent, or the gate let the job go on its on-timeout rule). The engine
// raises AlertJobStartedMissing for it (R5.61), once per call, and still
// returns no error: the job has finished and nothing fails.
func (e *Engine) Release(ctx context.Context, machineID string, job JobRef, claims []string) ([]Freed, error) {
	freed, err := e.freeJob(ctx, machineID, toJob(job), claims, slots.OutcomeRan)
	if err == nil && len(claims) == 0 {
		e.lifeEnded(machineID, job)
	}
	if err == nil && len(freed) == 0 && machineID != "" && len(claims) == 0 {
		a := Alert{Kind: AlertJobStartedMissing, MachineID: machineID, Job: job, At: e.now(),
			Message: "A job ended on this machine that never took a slot, so its job-started hook did not reach crsp or the gate let it go without one. " +
				"Run 'crsp doctor --check runner.hooks' for the runner that ran it."}
		e.log.Warn("job ended without a slot", "machine", machineID, "repository", job.Repository, "run", job.RunID, "job", job.Job, "runner", job.RunnerName)
		if e.alert != nil {
			e.alert(a)
		}
	}
	return freed, err
}

// CompletedOnGitHub frees leftovers of a job GitHub reports completed, on
// every machine (R5.36).
func (e *Engine) CompletedOnGitHub(ctx context.Context, job JobRef) ([]Freed, error) {
	freed, err := e.freeJob(ctx, "", toJob(job), nil, slots.OutcomeCompletedGH)
	if err == nil {
		e.lifeEnded("", job)
	}
	return freed, err
}

func (e *Engine) freeJob(ctx context.Context, machineID string, job slots.Job, claims []string, outcome string) ([]Freed, error) {
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		var all []slots.Slot
		var err error
		if machineID == "" {
			all, err = tx.AllSlots()
		} else {
			all, err = tx.Slots(machineID)
		}
		if err != nil {
			return err
		}
		for _, s := range jobSlots(all, job) {
			if len(claims) > 0 && !has(claims, s.Claim) {
				continue
			}
			ok, err := tx.FreeSlot(s.MachineID, s.Kind, s.Number, s.Claim)
			if err != nil {
				return err
			}
			if ok {
				freed = append(freed, e.freedRow(tx, s, slots.EventFreed, outcome, now))
			}
		}
		var tickets []slots.Ticket
		if machineID == "" {
			tickets, err = tx.AllTickets()
		} else {
			tickets, err = tx.Tickets(machineID)
		}
		if err != nil {
			return err
		}
		for _, t := range tickets {
			if !t.Job.SameJob(job) {
				continue
			}
			if ok, err := tx.DeleteTicket(t.ID); err != nil {
				return err
			} else if ok {
				freed = append(freed, e.droppedRow(tx, t, outcome, now))
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

func has(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// ErrLeaseLost is returned by Renew for a slot that is gone or was taken again
// by another holder: a renewal never makes a slot (R5.70).
var ErrLeaseLost = errors.New("lease is gone")

// Renew extends the lease of the slots the holder holds on the machine, by the
// holder id the gate request carried and the claims the grants gave (R5.70).
// Only slots that still hold both are renewed. It returns ErrLeaseLost when
// none was, so the caller knows the slot was reclaimed.
func (e *Engine) Renew(ctx context.Context, machineID, holderID string, claims []string) error {
	n := 0
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		set, err := tx.Settings(machineID)
		if err != nil {
			return err
		}
		all, err := tx.Slots(machineID)
		if err != nil {
			return err
		}
		for _, s := range all {
			if s.HolderID != holderID || !has(claims, s.Claim) {
				continue
			}
			// A lease that already ended is reclaimed, not revived (R5.70).
			if !slots.LeaseEnd(s).After(now) {
				continue
			}
			ok, err := tx.RenewSlot(machineID, s.Kind, s.Number, s.HolderID, s.Claim, slots.LeaseExpiry(now, set))
			if err != nil {
				return err
			}
			if ok {
				n++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

// RenewJobs extends the lease of every slot on the machine that a job in
// running holds. The agent of the machine lists the jobs it runs on each holder
// report (agent reconnect R5.35, R5.70); this is the heartbeat of a job that no
// hook is waiting for any more. A job that is not listed keeps its lease end, so
// its slot is reclaimed once the lease passes. No process id is looked at.
func (e *Engine) RenewJobs(ctx context.Context, machineID string, running []JobRef) error {
	if len(running) == 0 {
		return nil
	}
	return e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		set, err := tx.Settings(machineID)
		if err != nil {
			return err
		}
		all, err := tx.Slots(machineID)
		if err != nil {
			return err
		}
		for _, j := range running {
			for _, s := range jobSlots(all, toJob(j)) {
				if !slots.LeaseEnd(s).After(now) {
					continue
				}
				if _, err := tx.RenewSlot(machineID, s.Kind, s.Number, s.HolderID, s.Claim, slots.LeaseExpiry(now, set)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Sweep frees slots past the slot maximum age and drops expired tickets on
// every machine (R5.33, R5.34).
func (e *Engine) Sweep(ctx context.Context, now time.Time) ([]Freed, error) {
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		f, err := e.staleSlots(tx, "", now)
		freed = f
		return err
	})
	e.notifyFreed(freed)
	return freed, err
}

// FreeStale frees the stale slots of a machine on demand. Live slots named in
// live are freed too, because the caller asked for a yes (R5.37).
func (e *Engine) FreeStale(ctx context.Context, machineID string, live []Grant) ([]Freed, error) {
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		f, err := e.staleSlots(tx, machineID, now)
		if err != nil {
			return err
		}
		freed = f
		for _, g := range live {
			all, err := tx.Slots(machineID)
			if err != nil {
				return err
			}
			for _, s := range all {
				if s.Kind != slots.Kind(g.Kind) || s.Number != g.Number {
					continue
				}
				if ok, err := tx.FreeSlot(machineID, s.Kind, s.Number, s.Claim); err != nil {
					return err
				} else if ok {
					freed = append(freed, e.freedRow(tx, s, slots.EventFreed, "freed by hand", now))
				}
			}
		}
		return nil
	})
	e.notifyFreed(freed)
	return freed, err
}

// RunSweeper runs Sweep every interval until ctx ends, so slots past the slot
// maximum age and tickets past their beat limit are freed even when no gate
// polls (R5.33, R5.34). A failed sweep is logged and tried again.
func RunSweeper(ctx context.Context, q SlotQueue, log *slog.Logger, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			freed, err := q.Sweep(ctx, now.UTC())
			if err != nil {
				log.Warn("slot sweep failed", "err", err)
				continue
			}
			for _, f := range freed {
				log.Info("slot sweep freed", "machine", f.MachineID, "kind", f.Kind, "slot", f.Number,
					"repository", f.Job.Repository, "run", f.Job.RunID, "job", f.Job.Job, "reason", f.Reason)
			}
		}
	}
}
