package assign

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// Sweep does the timed work of the controller: it deletes runners nobody used,
// tells the owner about a job that waits too long, lets go of a job GitHub
// cancels, and plans again. The poller calls it after every round, and a
// restart's first round calls it too.
func (c *Controller) Sweep(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	open, err := c.Store.List(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, l := range open {
		switch {
		case l.State == Starting && c.unused(l, now), l.State == Gone && l.RunnerName != "" && l.RunnerRecord != "" && c.unused(l, now):
			keep(c.dropRunner(ctx, l, now))
		case l.State == Waiting:
			keep(c.age(ctx, l, now))
		}
	}
	keep(c.plan(ctx))
	return firstErr
}

// unused is true when the runner of a lease had its start-up time and two
// minutes more and no job came to it (runners R5.112). macOS counts its VM boot.
func (c *Controller) unused(l Lease, now time.Time) bool {
	if l.StartedAt.IsZero() {
		return false
	}
	return now.Sub(l.StartedAt) >= runners.StartupAllowance(l.OS)+UnusedGrace
}

// dropRunner deletes a runner nobody used through GitHub and the agent. A
// starting lease whose job is still queued waits again for a new runner.
func (c *Controller) dropRunner(ctx context.Context, l Lease, now time.Time) error {
	if c.Gate != nil && c.Gate.Holds(ctx, l.JobID) {
		// A gated persistent runner is never deleted: its listener is stopped and
		// its label removed, and the job waits again.
		if err := c.Gate.Done(ctx, l.JobID); err != nil {
			return fmt.Errorf("stop the listener of %s: %w", l.RunnerName, err)
		}
	} else if l.RunnerRecord != "" {
		err := c.Runners.RemoveIdleRunner(ctx, l.RunnerRecord, Actor)
		if errors.Is(err, runners.ErrRunnerBusy) {
			return nil // a job came in between: its in_progress webhook follows
		}
		if err != nil {
			return fmt.Errorf("delete the unused runner %s: %w", l.RunnerName, err)
		}
	} else if c.Purge != nil && l.RunnerName != "" {
		// A start that crashed before its record: GitHub may hold a runner by this name.
		if err := c.Purge(ctx, l.Repository, l.RunnerName); err != nil {
			return fmt.Errorf("delete the unused runner %s: %w", l.RunnerName, err)
		}
	}
	l.RunnerName, l.RunnerRecord, l.GitHubRunner, l.MachineID, l.StartedAt = "", "", 0, "", time.Time{}
	if l.State == Starting {
		l.State = Waiting
	}
	l.Reason, l.ReasonCode, l.UpdatedAt = "", CodeNone, now
	return c.Store.Save(ctx, l)
}

// age tells the owner about a job that waits 12 h and lets go of one GitHub
// cancels after 24 h (runners R5.114).
func (c *Controller) age(ctx context.Context, l Lease, now time.Time) error {
	wait := now.Sub(l.QueuedAt)
	switch {
	case wait >= CancelAfter:
		l.State, l.Reason, l.ReasonCode, l.UpdatedAt = Gone, "GitHub cancels a job queued for 24 hours", CodeNone, now
		return c.Store.Save(ctx, l)
	case wait >= AlertAfter && !l.Alerted:
		l.Alerted, l.UpdatedAt = true, now
		if c.Notify != nil {
			c.Notify.Notify(ctx, l, fmt.Sprintf("a job of %s has waited %d hours; GitHub cancels it at 24 hours. %s", l.Repository, int(wait.Hours()), l.Reason))
		}
		return c.Store.Save(ctx, l)
	}
	return nil
}

// Unserved lists the waiting jobs no machine can ever serve: no runner of their
// class, label or OS. The Doctor shows each as a finding (runners R5.114).
func (c *Controller) Unserved(ctx context.Context) ([]Lease, error) {
	ls, err := c.Store.List(ctx, Waiting)
	if err != nil {
		return nil, err
	}
	var out []Lease
	for _, l := range ls {
		if l.ReasonCode == CodeNever || l.ReasonCode == CodeNoMachine {
			out = append(out, l)
		}
	}
	return out, nil
}

// LongWaiting lists the waiting jobs past the 12 h alert.
func (c *Controller) LongWaiting(ctx context.Context) ([]Lease, error) {
	ls, err := c.Store.List(ctx, Waiting)
	if err != nil {
		return nil, err
	}
	now := c.now()
	var out []Lease
	for _, l := range ls {
		if now.Sub(l.QueuedAt) >= AlertAfter {
			out = append(out, l)
		}
	}
	return out, nil
}
