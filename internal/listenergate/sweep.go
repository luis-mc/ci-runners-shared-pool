package listenergate

import (
	"context"
	"errors"
	"time"
)

// Sweep is one round of the background work: it finishes stops that failed,
// ends weekly wakes, parks listeners nobody needs (enforce only), and starts
// the weekly wake of every runner that is due. A failure of one runner never
// stops the others; the first error is returned.
func (g *Gate) Sweep(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	stopping, err := g.Store.ListState(ctx, Stopping)
	keep(err)
	for _, l := range stopping {
		keep(g.finish(ctx, l))
	}
	rs, err := g.Runners.Gated(ctx)
	if err != nil {
		return errors.Join(first, err)
	}
	waking, err := g.Store.Waking(ctx)
	if err != nil {
		return errors.Join(first, err)
	}
	now := g.now()
	enforce := g.enforce()
	for _, r := range rs {
		open, err := g.Store.OpenFor(ctx, r.ID)
		if err != nil {
			keep(err)
			continue
		}
		if len(open) > 0 {
			continue // a job needs the listener
		}
		if since, on := waking[r.ID]; on {
			if now.Sub(since) >= WakeHold {
				keep(g.endWake(ctx, r, enforce))
			}
			continue
		}
		last, err := g.Store.LastWake(ctx, r.ID)
		if err != nil {
			keep(err)
			continue
		}
		if last.IsZero() || now.Sub(last) >= WakeEvery {
			keep(g.startWake(ctx, r, enforce, now))
			continue
		}
		if enforce {
			keep(g.park(ctx, r))
		}
	}
	return first
}

// park turns off a listener nobody needs. A busy runner is never touched.
func (g *Gate) park(ctx context.Context, r Runner) error {
	st, err := g.Service.ListenerState(ctx, r.MachineID, r.ServiceRef)
	if err != nil || st != ListenerOnline {
		return err
	}
	if err := g.Service.StopListener(ctx, r.MachineID, r.ServiceRef, false); err != nil {
		return err
	}
	g.record(ctx, Event{Action: ActionStop, Runner: r.Name, Detail: "no job needs the runner: listener stopped (gated: offline by design)"})
	return nil
}

func (g *Gate) startWake(ctx context.Context, r Runner, enforce bool, now time.Time) error {
	if enforce {
		if err := g.Service.StartListener(ctx, r.MachineID, r.ServiceRef, true); err != nil {
			g.record(ctx, Event{Action: ActionFailed, Runner: r.Name, Detail: "weekly wake-up did not start: " + err.Error()})
			return err
		}
		g.record(ctx, Event{Action: ActionWake, Runner: r.Name, Detail: "weekly wake-up: listener on for about a minute, with no job and no job label"})
	} else {
		g.record(ctx, Event{Action: ActionWouldStart, Runner: r.Name, Detail: "observe mode: would wake the listener for the weekly wake-up"})
	}
	return g.Store.WakeStarted(ctx, r.ID, now)
}

func (g *Gate) endWake(ctx context.Context, r Runner, enforce bool) error {
	if enforce {
		if err := g.Service.StopListener(ctx, r.MachineID, r.ServiceRef, false); err != nil {
			g.record(ctx, Event{Action: ActionFailed, Runner: r.Name, Detail: "weekly wake-up did not stop: " + err.Error()})
			return err
		}
		g.record(ctx, Event{Action: ActionStop, Runner: r.Name, Detail: "weekly wake-up ended: listener stopped"})
	} else {
		g.record(ctx, Event{Action: ActionWouldStop, Runner: r.Name, Detail: "observe mode: would stop the listener after the weekly wake-up"})
	}
	return g.Store.WakeEnded(ctx, r.ID)
}

// RunnerStatus is one gated runner as the console shows it.
type RunnerStatus struct {
	Runner Runner
	// Text is "gated: offline by design" while no job holds the listener.
	Text string
	// ListenerOn is true while an open lease holds the listener.
	ListenerOn bool
	JobIDs     []int64
	// NextWake is when the weekly wake-up is due; LastWake is the zero time when
	// the runner was never woken.
	NextWake  time.Time
	LastWake  time.Time
	QueueText string
}

// Status lists the gated runners with what the console shows.
func (g *Gate) Status(ctx context.Context) ([]RunnerStatus, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rs, err := g.Runners.Gated(ctx)
	if err != nil {
		return nil, err
	}
	now := g.now()
	out := make([]RunnerStatus, 0, len(rs))
	for _, r := range rs {
		open, err := g.Store.OpenFor(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		last, err := g.Store.LastWake(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		s := RunnerStatus{Runner: r, Text: "gated: offline by design", LastWake: last, NextWake: now}
		if !last.IsZero() {
			s.NextWake = last.Add(WakeEvery)
		}
		for _, l := range open {
			s.ListenerOn = true
			s.JobIDs = append(s.JobIDs, l.JobID)
		}
		if s.ListenerOn {
			s.Text = "listener on for the admitted job"
		}
		if g.Queue != nil {
			s.QueueText = g.Queue(ctx, r)
		}
		out = append(out, s)
	}
	return out, nil
}

// Enforcing says whether the gate acts; false is observe mode. The doctor reads it.
func (g *Gate) Enforcing(context.Context) (bool, error) { return g.enforce(), nil }
