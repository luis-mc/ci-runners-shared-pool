package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// ErrTicketGone is returned by Beat for a ticket that does not exist: a beat
// never makes a ticket (R5.18). The caller sends a new GateRequest with the
// first queued time to keep its place (R5.19).
var ErrTicketGone = errors.New("ticket is gone")

// Gate takes or keeps a ticket for the request and answers go or wait. Before
// it checks the ticket it frees stale slots (R5.16). The order of checks is:
// first in line, then a free slot, and the load only when both hold (R5.13).
func (e *Engine) Gate(ctx context.Context, req GateRequest) (Decision, error) {
	if req.MachineID == "" || req.GateID == "" {
		return Decision{}, errNoMachine
	}
	var dec Decision
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		f, err := e.staleSlots(tx, req.MachineID, now)
		if err != nil {
			return err
		}
		freed = f
		dec, err = e.evaluate(tx, req, true, now)
		return err
	})
	e.notifyFreed(freed)
	if err == nil {
		e.emitDecision(req.MachineID, req.Job, dec)
		e.lifeStarted(req.MachineID, req.Job, dec)
	}
	return dec, err
}

// Beat keeps a waiting ticket alive without changing its place (R5.18) and
// answers as Gate does. A ticket that is gone gives ErrTicketGone.
func (e *Engine) Beat(ctx context.Context, machineID, gateID string, load Load) (Decision, error) {
	var dec Decision
	var freed []Freed
	var job JobRef
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		f, err := e.staleSlots(tx, machineID, now)
		if err != nil {
			return err
		}
		freed = f
		t, ok, err := tx.Ticket(gateID)
		if err != nil {
			return err
		}
		if !ok || t.MachineID != machineID {
			return ErrTicketGone
		}
		req := GateRequest{GateID: gateID, MachineID: machineID, Load: load,
			Job: fromJob(t.Job), EventClass: t.EventClass,
			Holder: Holder{Machine: t.Holder.Machine, HolderPID: t.Holder.PID, GatePID: t.Holder.GatePID, HolderStarted: t.Holder.Started}}
		job = req.Job
		dec, err = e.evaluate(tx, req, false, now)
		return err
	})
	e.notifyFreed(freed)
	if err == nil {
		e.emitDecision(machineID, job, dec)
		e.lifeStarted(machineID, job, dec)
	}
	return dec, err
}

// evaluate is one poll of the gate for the request inside tx. With create
// false it never makes a ticket.
func (e *Engine) evaluate(tx slots.Tx, req GateRequest, create bool, now time.Time) (Decision, error) {
	set, err := tx.Settings(req.MachineID)
	if err != nil {
		return Decision{}, err
	}
	job := toJob(req.Job)
	names := slots.JobNames{Repository: job.Repository, Job: job.Job, Shard: job.Shard}
	pol, _, err := tx.Policy(job.Repository)
	if err != nil {
		return Decision{}, err
	}
	budget := slots.Limits(slots.Limits(slots.Budget{MemoryPct: set.BudgetMemoryPct, CPUPct: set.BudgetCPUPct},
		pol.BudgetMemoryPct, pol.BudgetCPUPct), req.BudgetMemoryPct, req.BudgetCPUPct)

	all, err := tx.Slots(req.MachineID)
	if err != nil {
		return Decision{}, err
	}
	held := jobSlots(all, job)
	// A request or beat from the holder of a slot renews its lease (R5.70).
	for _, h := range held {
		if h.HolderID == req.GateID && slots.LeaseEnd(h).After(now) {
			if _, err := tx.RenewSlot(h.MachineID, h.Kind, h.Number, h.HolderID, h.Claim, slots.LeaseExpiry(now, set)); err != nil {
				return Decision{}, err
			}
		}
	}
	var grants []Grant
	var heavyHeld, ciHeld *slots.Slot
	for i := range held {
		g := Grant{Kind: SlotKind(held[i].Kind), Number: held[i].Number, Claim: held[i].Claim, TakenAt: held[i].TakenAt}
		grants = append(grants, g)
		switch held[i].Kind {
		case slots.KindHeavy:
			heavyHeld = &held[i]
		case slots.KindCI:
			ciHeld = &held[i]
		}
	}
	// A retried request for a job that already holds its slot answers go again.
	if ciHeld != nil || (len(held) > 0 && heavyHeld == nil) {
		return Decision{Go: grants}, nil
	}

	if job.RunnerName != "" && e.unclean != nil {
		if why := e.unclean(req.MachineID, job.RunnerName); why != "" {
			// R5.91: a runner whose clean-up failed holds the job until it is clean.
			if tk, ok, err := tx.Ticket(req.GateID); err != nil {
				return Decision{}, err
			} else if ok {
				if err := tx.SetTicketWait(tk.ID, string(WaitPaused)); err != nil {
					return Decision{}, err
				}
			}
			return Decision{Wait: []WaitReason{WaitPaused}, WaitText: "this runner is not clean yet: " + why}, nil
		}
	}

	if r, text, hold := e.notAccepting(req.MachineID); hold {
		// R5.68: a machine that does not accept holds the job, whatever slot is free.
		if tk, ok, err := tx.Ticket(req.GateID); err != nil {
			return Decision{}, err
		} else if ok {
			if err := tx.SetTicketWait(tk.ID, string(r)); err != nil {
				return Decision{}, err
			}
		}
		return Decision{Wait: []WaitReason{r}, WaitText: text}, nil
	}

	over, overText := overBudget(req.Load, budget)
	measured := req.Load.MemoryKnown || req.Load.CPUKnown

	// Light job: a free light slot at once, no ticket, when under budget (R5.24).
	if slots.IsLight(names, set) {
		if _, tk, err := tx.Ticket(req.GateID); err != nil {
			return Decision{}, err
		} else if !tk {
			if n, free := freeNumber(all, slots.KindLight, set.LightSlots); free > 0 {
				if !measured {
					return Decision{Wait: []WaitReason{WaitLoadNotMeasured}, WaitText: LoadNotMeasuredText}, nil
				}
				if over {
					return Decision{Wait: []WaitReason{WaitLoadOverBudget}, WaitText: overText}, nil
				}
				s, taken, err := e.takeSlot(tx, req, slots.KindLight, n, time.Time{}, now)
				if err != nil {
					return Decision{}, err
				}
				if taken {
					return Decision{Go: []Grant{grantOf(s)}, SlotWait: 0}, nil
				}
				return Decision{Wait: []WaitReason{WaitLostRace}, WaitText: "lost the race for the slot"}, nil
			}
			// every light slot busy: fall through to the normal queue
		}
	}

	// Heavy job: a heavy slot first, no job slot and no ticket while it waits (R5.26).
	heavy := slots.IsHeavy(names, set)
	var heavyGrant *Grant
	if heavy {
		if heavyHeld != nil {
			g := Grant{Kind: SlotHeavy, Number: heavyHeld.Number, Claim: heavyHeld.Claim, TakenAt: heavyHeld.TakenAt}
			heavyGrant = &g
		} else {
			n, free := freeNumber(all, slots.KindHeavy, set.HeavySlots)
			if free == 0 {
				return Decision{Wait: []WaitReason{WaitFreeSlot},
					WaitText: fmt.Sprintf("no free heavy slot (all %d busy)", set.HeavySlots)}, nil
			}
			s, taken, err := e.takeSlot(tx, req, slots.KindHeavy, n, time.Time{}, now)
			if err != nil {
				return Decision{}, err
			}
			if !taken {
				return Decision{Wait: []WaitReason{WaitLostRace}, WaitText: "lost the race for the slot"}, nil
			}
			g := grantOf(s)
			heavyGrant = &g
			all = append(all, s)
		}
	}

	// The normal queue.
	t, ok, err := tx.Ticket(req.GateID)
	if err != nil {
		return Decision{}, err
	}
	if !ok {
		if !create {
			return Decision{}, ErrTicketGone
		}
		queued := now
		if !req.FirstQueued.IsZero() && now.Sub(req.FirstQueued) < time.Duration(set.SlotMaxAgeMin)*time.Minute && !req.FirstQueued.After(now) {
			queued = req.FirstQueued
		}
		// A hook that comes back under a new gate id for the same job takes the
		// place of its older ticket: one waiting job holds one ticket (R5.6).
		old, err := tx.Tickets(req.MachineID)
		if err != nil {
			return Decision{}, err
		}
		for _, o := range old {
			if o.Job.SameJob(job) && o.Job.Shard == job.Shard && o.Kind == slots.KindCI {
				if o.QueuedAtNs < queued.UnixNano() && o.QueuedAt().After(now.Add(-time.Duration(set.SlotMaxAgeMin)*time.Minute)) {
					queued = o.QueuedAt()
				}
				if _, err := tx.DeleteTicket(o.ID); err != nil {
					return Decision{}, err
				}
			}
		}
		t = slots.Ticket{ID: req.GateID, MachineID: req.MachineID, Job: job, Kind: slots.KindCI,
			Priority: pol.Priority, EventClass: req.EventClass, QueuedAtNs: queued.UnixNano(), Random: randomSuffix(),
			BeatAt: now, Holder: toHolder(req.Holder)}
		if t.Holder.Machine == "" {
			t.Holder.Machine = req.MachineID
		}
		t.ExpiresAt = slots.TicketExpiry(queued, now, set)
		if err := tx.InsertTicket(t); err != nil {
			return Decision{}, err
		}
	} else if _, err := tx.BeatTicket(t.ID, now, slots.TicketExpiry(t.QueuedAt(), now, set)); err != nil {
		return Decision{}, err
	}

	tickets, err := tx.Tickets(req.MachineID)
	if err != nil {
		return Decision{}, err
	}
	running, err := tx.RunningByRepository()
	if err != nil {
		return Decision{}, err
	}
	limits := map[string]int{}
	for _, tk := range tickets {
		if _, seen := limits[tk.Job.Repository]; seen {
			continue
		}
		p, _, err := tx.Policy(tk.Job.Repository)
		if err != nil {
			return Decision{}, err
		}
		limits[tk.Job.Repository] = p.MaxParallel
	}
	ordered := slots.Order(tickets, running, limits)
	var me slots.Ordered
	for _, o := range ordered {
		if o.Ticket.ID == t.ID {
			me = o
		}
	}
	wait := func(reason WaitReason, text string) (Decision, error) {
		if err := tx.SetTicketWait(t.ID, string(reason)); err != nil {
			return Decision{}, err
		}
		return Decision{Wait: []WaitReason{reason}, WaitText: text, Place: me.Place}, nil
	}

	// R5.13: first in line, then a free slot, then the load.
	if me.PassedOver {
		return wait(WaitRepoParallelLimit, fmt.Sprintf("repository %s runs its maximum parallel jobs (%d)", job.Repository, limits[job.Repository]))
	}
	need, class := set.WeightOf(names)
	var adm admitted
	if set.Admission.On {
		// R5.90 to R5.94: pool room, passes and the reserved job decide who goes.
		if _, free := freeNumber(all, slots.KindCI, set.JobSlots); free == 0 {
			return wait(WaitFreeSlot, fmt.Sprintf("no free slot (all %d busy)", set.JobSlots))
		}
		a, hold, err := e.admissionGate(tx, set, req.MachineID, t, ordered, all, now, wait)
		if err != nil {
			return Decision{}, err
		}
		if hold != nil {
			return *hold, nil
		}
		adm = a
	} else if first, ok := slots.FirstInLine(ordered); !ok || first.ID != t.ID {
		return wait(WaitFreeSlot, fmt.Sprintf("%d ahead in the queue", slots.Ahead(ordered, t.ID)))
	}
	n, free := freeNumber(all, slots.KindCI, set.JobSlots)
	if free == 0 {
		return wait(WaitFreeSlot, fmt.Sprintf("no free slot (all %d busy)", set.JobSlots))
	}
	if !set.Admission.On {
		// R5.69: the job's weight must fit what the held ci jobs leave of the budget.
		capacity, used := set.Capacity(), set.HeldWeight(all)
		if used.CPU+need.CPU > capacity.CPU || used.Memory+need.Memory > capacity.Memory {
			return wait(WaitFreeSlot, WeightWaitText(class, need, used, capacity))
		}
	}
	// R5.22: a load that cannot be measured never counts as under budget.
	if !measured {
		return wait(WaitLoadNotMeasured, LoadNotMeasuredText)
	}
	if over {
		return wait(WaitLoadOverBudget, overText)
	}
	// R5.66: a warm runner yields its budget to a real job, oldest first.
	e.yieldWarm(req.MachineID, job.RunnerName, set, all, need)
	s, taken, err := e.takeSlot(tx, req, slots.KindCI, n, t.QueuedAt(), now)
	if err != nil {
		return Decision{}, err
	}
	if !taken {
		return wait(WaitLostRace, "lost the race for the slot")
	}
	if _, err := tx.DeleteTicket(t.ID); err != nil {
		return Decision{}, err
	}
	if set.Admission.On {
		if err := settleAdmission(tx, req.MachineID, t.ID, adm); err != nil {
			return Decision{}, err
		}
	}
	out := Decision{Go: []Grant{grantOf(s)}, SlotWait: now.Sub(t.QueuedAt()), Class: adm.class, MemoryCapGiB: adm.capGiB}
	if heavyGrant != nil {
		out.Go = append([]Grant{*heavyGrant}, out.Go...)
	}
	return out, nil
}

// notAccepting reports the hold of a machine that is paused or on battery
// (machines R5.62).
func (e *Engine) notAccepting(machineID string) (WaitReason, string, bool) {
	if e.accepting == nil {
		return "", "", false
	}
	ok, reason := e.accepting(machineID)
	if ok {
		return "", "", false
	}
	switch reason {
	case "paused":
		return WaitPaused, "this machine is paused", true
	case "on_battery":
		return WaitOnBattery, "this machine is on battery", true
	}
	return "", "", false
}

func grantOf(s slots.Slot) Grant {
	return Grant{Kind: SlotKind(s.Kind), Number: s.Number, Claim: s.Claim, TakenAt: s.TakenAt}
}

// takeSlot inserts the slot; the key makes a second take of the same slot fail,
// which reports taken=false (R5.14). Each take gets a new random claim (R5.15).
func (e *Engine) takeSlot(tx slots.Tx, req GateRequest, kind slots.Kind, number int, queuedAt time.Time, now time.Time) (slots.Slot, bool, error) {
	set, err := tx.Settings(req.MachineID)
	if err != nil {
		return slots.Slot{}, false, err
	}
	if queuedAt.IsZero() {
		queuedAt = now
	}
	h := toHolder(req.Holder)
	if h.Machine == "" {
		h.Machine = req.MachineID
	}
	s := slots.Slot{MachineID: req.MachineID, Kind: kind, Number: number, Claim: newClaim(), Job: toJob(req.Job),
		Holder: h, QueuedAt: queuedAt, TakenAt: now, ExpiresAt: slots.SlotExpiry(now, set),
		HolderID: req.GateID, LeaseExpiresAt: slots.LeaseExpiry(now, set)}
	if err := tx.InsertSlot(s); err != nil {
		if errors.Is(err, slots.ErrSlotTaken) {
			return slots.Slot{}, false, nil
		}
		return slots.Slot{}, false, err
	}
	e.logRow(tx, slots.LogRow{MachineID: s.MachineID, Event: slots.EventTaken, Kind: kind, Number: number, Job: s.Job,
		QueuedAt: queuedAt, SlotAt: now, At: now})
	return s, true, nil
}

// overBudget reports whether the load is over budget: memory strictly above
// the memory budget or CPU strictly above the CPU budget, naming each figure
// (R5.45). A figure that is not known never counts as over; when neither is
// known the caller holds the job (R5.22).
func overBudget(l Load, b slots.Budget) (bool, string) {
	var parts []string
	if l.MemoryKnown && l.MemoryPct > float64(b.MemoryPct) {
		parts = append(parts, fmt.Sprintf("memory %.0f%% > %d%%", l.MemoryPct, b.MemoryPct))
	}
	if l.CPUKnown && l.CPUPct > float64(b.CPUPct) {
		parts = append(parts, fmt.Sprintf("CPU %.0f%% > %d%%", l.CPUPct, b.CPUPct))
	}
	return len(parts) > 0, strings.Join(parts, ", ")
}

// WeightWaitText names the weight that holds a job: its class and weight, and
// what is free of the machine's budget (R5.69, R5.48).
func WeightWaitText(class string, need, used, capacity slots.Weight) string {
	return fmt.Sprintf("job class %s weighs %d CPU and %d memory units; %d of %d CPU and %d of %d memory units free",
		class, need.CPU, need.Memory, capacity.CPU-used.CPU, capacity.CPU, capacity.Memory-used.Memory, capacity.Memory)
}

// LoadNotMeasuredText is the wait text while the machine's load is unknown.
const LoadNotMeasuredText = "load not measured yet"

// Cancel drops a ticket at once (the hook went away). A heavy slot the job
// holds without a job slot is freed too, with the outcome no-ci-slot (R5.26).
func (e *Engine) Cancel(ctx context.Context, machineID, gateID string) error {
	var freed []Freed
	err := e.store.Atomically(ctx, func(tx slots.Tx) error {
		now := e.now()
		t, ok, err := tx.Ticket(gateID)
		if err != nil || !ok || t.MachineID != machineID {
			return err
		}
		if _, err := tx.DeleteTicket(gateID); err != nil {
			return err
		}
		if err := tx.DropPasses(gateID); err != nil {
			return err
		}
		freed = append(freed, e.droppedRow(tx, t, "gate cancelled", now))
		all, err := tx.Slots(machineID)
		if err != nil {
			return err
		}
		held := jobSlots(all, t.Job)
		hasCI := false
		for _, s := range held {
			if s.Kind == slots.KindCI {
				hasCI = true
			}
		}
		if hasCI {
			return nil
		}
		for _, s := range held {
			if s.Kind != slots.KindHeavy {
				continue
			}
			if ok, err := tx.FreeSlot(machineID, s.Kind, s.Number, s.Claim); err != nil {
				return err
			} else if ok {
				freed = append(freed, e.freedRow(tx, s, slots.EventFreed, slots.OutcomeNoCISlot, now))
			}
		}
		return nil
	})
	e.notifyFreed(freed)
	return err
}
