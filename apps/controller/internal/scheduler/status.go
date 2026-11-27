package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// Status lists the slots and the waiting queue of a machine (R5.58). A slot is
// flagged stale when it is past its maximum age; the holder process is judged
// by the agent (R5.32), not here.
func (e *Engine) Status(ctx context.Context, machineID string) (Status, error) {
	st := Status{MachineID: machineID}
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
		for _, k := range []slots.Kind{slots.KindCI, slots.KindLight, slots.KindHeavy} {
			for _, s := range all {
				if s.Kind != k {
					continue
				}
				h := HeldSlot{Grant: Grant{Kind: SlotKind(s.Kind), Number: s.Number, Claim: "", TakenAt: s.TakenAt}, Job: fromJob(s.Job)}
				if !s.ExpiresAt.After(now) {
					h.StaleReason = fmt.Sprintf("older than %d min", set.SlotMaxAgeMin)
				}
				st.Slots = append(st.Slots, h)
			}
		}
		tickets, err := tx.Tickets(machineID)
		if err != nil {
			return err
		}
		running, err := tx.RunningByRepository()
		if err != nil {
			return err
		}
		limits := map[string]int{}
		for _, t := range tickets {
			p, _, err := tx.Policy(t.Job.Repository)
			if err != nil {
				return err
			}
			limits[t.Job.Repository] = p.MaxParallel
		}
		for _, o := range slots.Order(tickets, running, limits) {
			t := o.Ticket
			st.Tickets = append(st.Tickets, Ticket{GateID: t.ID, Job: fromJob(t.Job), Priority: t.Priority, Class: t.EventClass,
				QueuedAt: t.QueuedAt(), BeatAt: t.BeatAt, ExpiresAt: t.ExpiresAt, Place: o.Place, Reason: WaitReason(t.WaitReason)})
		}
		return nil
	})
	return st, err
}

// Age is the age of a held slot in seconds on the controller clock.
func Age(now, takenAt time.Time) int { return int(now.Sub(takenAt).Seconds()) }
