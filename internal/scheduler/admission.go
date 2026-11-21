package scheduler

import (
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/admit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// Admission by pool room, passes and the reserved job (scheduling R5.90 to
// R5.94). It runs inside the gate's transaction and is on only for a machine
// whose pool size is known (slots.Admission.On); otherwise the unit weights of
// R5.69 decide as before.

// WithRunnerClass sets where the gate reads the size class of a runner (runners
// R5.71): "" when the runner or its class is not known. A runner of unknown
// class is not judged by a ceiling and gets no memory cap.
func WithRunnerClass(f func(machineID, runnerName string) string) Option {
	return func(e *Engine) { e.runnerClass = f }
}

// WithForecast sets where the gate reads the forecast end of a job that starts
// at start (observability R5.40). ok is false when there is no forecast.
func WithForecast(f func(machineID string, job slots.Job, start time.Time) (end time.Time, ok bool)) Option {
	return func(e *Engine) { e.forecastEnd = f }
}

// WithZone sets the zone of the times in wait lines (the owner's zone, UTC by default).
func WithZone(z *time.Location) Option { return func(e *Engine) { e.zone = z } }

type admitted struct {
	class  string // the class of the runner, "" when unknown
	capGiB int
	// passed are the tickets this start passes.
	passed []string
	// reserved is true when this job was the machine's reserved job.
	reserved bool
}

type cand struct {
	t       slots.Ticket
	req     admit.Req
	class   string
	ceiling admit.Ceiling
	known   bool
	verdict admit.Verdict
	w       admit.Waiter
}

func (e *Engine) ceilingOf(set slots.Settings, machineID, runner string) (admit.Ceiling, bool) {
	if e.runnerClass == nil || runner == "" {
		return admit.Ceiling{}, false
	}
	c, ok := set.Admission.Ceiling(e.runnerClass(machineID, runner))
	if !ok {
		return admit.Ceiling{}, false
	}
	return admit.Ceiling{Class: c.Class, Req: admit.Req{CPUs: c.CPUs, MemoryGiB: c.MemoryGiB}}, true
}

func (e *Engine) heldOf(set slots.Settings, machineID string, all []slots.Slot) []admit.Held {
	var out []admit.Held
	for _, s := range all {
		if s.Kind != slots.KindCI {
			continue
		}
		w, _ := set.RequirementOf(slots.JobNames{Repository: s.Job.Repository, Job: s.Job.Job, Shard: s.Job.Shard})
		h := admit.Held{Req: admit.Req{CPUs: w.CPU, MemoryGiB: w.Memory}}
		if e.forecastEnd != nil {
			h.End, h.EndKnown = e.forecastEnd(machineID, s.Job, s.TakenAt)
		}
		out = append(out, h)
	}
	return out
}

func (e *Engine) zoneOrUTC() *time.Location {
	if e.zone != nil {
		return e.zone
	}
	return time.UTC
}

// admissionGate runs the admission part of the gate for the ticket t. It returns
// a hold (non-nil Decision) when the job waits, else what the start brings.
func (e *Engine) admissionGate(tx slots.Tx, set slots.Settings, machineID string, t slots.Ticket,
	ordered []slots.Ordered, all []slots.Slot, now time.Time, wait func(WaitReason, string) (Decision, error)) (admitted, *Decision, error) {
	adm := set.Admission
	pool := admit.Pool{CPUs: adm.PoolCPUs, MemoryGiB: adm.PoolMemoryGiB}
	held := e.heldOf(set, machineID, all)
	passes, err := tx.Passes(machineID)
	if err != nil {
		return admitted{}, nil, err
	}
	res, haveRes, err := tx.Reservation(machineID)
	if err != nil {
		return admitted{}, nil, err
	}

	// One entry per ticket in the order of service, passed-over ones left out.
	var cs []*cand
	present := map[string]bool{}
	for _, o := range ordered {
		present[o.Ticket.ID] = true
		if o.PassedOver {
			continue
		}
		tk := o.Ticket
		w, _ := set.RequirementOf(slots.JobNames{Repository: tk.Job.Repository, Job: tk.Job.Job, Shard: tk.Job.Shard})
		req := admit.Req{CPUs: w.CPU, MemoryGiB: w.Memory}
		smallest := adm.Smallest(req.CPUs, req.MemoryGiB)
		ceil, known := e.ceilingOf(set, machineID, tk.Job.RunnerName)
		c := &cand{t: tk, req: req, class: smallest, ceiling: ceil, known: known}
		c.verdict = admit.Check(smallest, req, ceil, known, pool, held)
		c.w = admit.Waiter{ID: tk.ID, Req: req, Class: smallest, QueuedAt: tk.QueuedAt(), Passes: passes[tk.ID],
			Blocked: !c.verdict.OK(), Could: admit.CouldRunHere(req, ceil, known, pool)}
		cs = append(cs, c)
	}

	// A reservation whose ticket is gone ends.
	if haveRes && !present[res.TicketID] {
		if err := tx.ClearReservation(machineID); err != nil {
			return admitted{}, nil, err
		}
		haveRes = false
	}
	// A waiter becomes the reserved job after 3 passes or 10 minutes (R5.93).
	if !haveRes {
		ws := make([]admit.Waiter, len(cs))
		for i, c := range cs {
			ws[i] = c.w
		}
		lim := admit.Limits{Passes: adm.AfterPasses, Wait: time.Duration(adm.AfterMinutes) * time.Minute}
		if p := admit.Promote(ws, false, lim, now); p != nil {
			exp, known := admit.ExpectedStart(p.Req, pool, held, now)
			res = slots.Reservation{MachineID: machineID, TicketID: p.ID, CPUs: p.Req.CPUs, MemoryGiB: p.Req.MemoryGiB,
				Class: p.Class, Since: now}
			if known {
				res.Expected = exp
			}
			if err := tx.SetReservation(res); err != nil {
				return admitted{}, nil, err
			}
			haveRes = true
		}
	} else if exp, known := admit.ExpectedStart(admit.Req{CPUs: res.CPUs, MemoryGiB: res.MemoryGiB}, pool, held, now); known != !res.Expected.IsZero() || (known && !exp.Equal(res.Expected)) {
		// The forecasts moved: keep the expected start current.
		res.Expected = time.Time{}
		if known {
			res.Expected = exp
		}
		if err := tx.SetReservation(res); err != nil {
			return admitted{}, nil, err
		}
	}
	areservation := admit.Reservation{MachineID: machineID, TicketID: res.TicketID, Req: admit.Req{CPUs: res.CPUs, MemoryGiB: res.MemoryGiB},
		Class: res.Class, Since: res.Since, Expected: res.Expected, ExpectedKnown: !res.Expected.IsZero()}

	// Backfill: while a job is reserved, any other job starts only as backfill (R5.94).
	for _, c := range cs {
		if !haveRes || c.t.ID == res.TicketID || c.w.Blocked {
			continue
		}
		var end time.Time
		endKnown := false
		if e.forecastEnd != nil {
			end, endKnown = e.forecastEnd(machineID, c.t.Job, now)
		}
		if ok, text := admit.Backfill(areservation, c.req, end, endKnown, pool, held); !ok {
			c.w.Blocked = true
			c.verdict = admit.Verdict{Why: admit.WhyPool, Text: text}
		}
	}

	// Walk the order of service: the first job that can start now goes.
	ahead := 0
	var skipped []admit.Waiter
	var mine *cand
	for _, c := range cs {
		if c.t.ID == t.ID {
			mine = c
		}
	}
	if mine == nil {
		return admitted{}, nil, ErrTicketGone
	}
	for _, c := range cs {
		if c.t.ID == t.ID {
			break
		}
		if c.w.Blocked {
			skipped = append(skipped, c.w)
		} else {
			ahead++
		}
	}
	reservedLine := ""
	if haveRes && res.TicketID == t.ID {
		reservedLine = "; " + areservation.Text(e.zoneOrUTC())
	}
	if mine.w.Blocked {
		d, err := wait(WaitFreeSlot, mine.verdict.Text+reservedLine)
		return admitted{}, &d, err
	}
	if ahead > 0 {
		d, err := wait(WaitFreeSlot, fmt.Sprintf("%d ahead in the queue", ahead))
		return admitted{}, &d, err
	}
	out := admitted{passed: admit.Passed(skipped), reserved: haveRes && res.TicketID == t.ID}
	if mine.known {
		out.class, out.capGiB = mine.ceiling.Class, mine.ceiling.MemoryGiB
	}
	return out, nil, nil
}

// settleAdmission records what a start brought: a pass for each job it passed,
// and the end of the started job's own passes and reservation.
func settleAdmission(tx slots.Tx, machineID, ticketID string, a admitted) error {
	for _, id := range a.passed {
		if err := tx.AddPass(machineID, id); err != nil {
			return err
		}
	}
	if err := tx.DropPasses(ticketID); err != nil {
		return err
	}
	if a.reserved {
		return tx.ClearReservation(machineID)
	}
	return nil
}

// CountsForMeasured says whether a finished run may feed the measured size of
// its job. A run killed for memory never lowers it (scheduling R5.79, R5.96).
func CountsForMeasured(overMemory bool) bool { return !overMemory }
