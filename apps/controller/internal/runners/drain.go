package runners

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultWarnAfter is when a drain warns and names the blocking job (owner
// decision for M1: warn at 30 minutes, keep waiting).
const DefaultWarnAfter = 30 * time.Minute

// ErrDrainTimeout means the drain timeout ran out with a job still running.
// The change waits and asks the user; it never stops the job (R6.4).
type ErrDrainTimeout struct{ Job string }

func (e ErrDrainTimeout) Error() string {
	return fmt.Sprintf("the drain timed out while job %s still runs; nothing was stopped", e.Job)
}

// IsDrainTimeout reports whether err is an ErrDrainTimeout.
func IsDrainTimeout(err error) bool {
	var t ErrDrainTimeout
	return errors.As(err, &t)
}

// IdleFunc says whether the runner has no job, and names the job when it has.
// An error (the runner list cannot be read) fails closed: no change is made.
type IdleFunc func(ctx context.Context) (idle bool, job string, err error)

// Drain waits for a runner to be idle and acts in the same moment (runners
// R6.4: the idle check runs right before the action, not earlier).
type Drain struct {
	Idle      IdleFunc
	Poll      time.Duration
	WarnAfter time.Duration
	// Timeout of zero means wait until ctx ends.
	Timeout time.Duration
	// OnWarn is called once, at WarnAfter, with the blocking job.
	OnWarn func(job string)
	// OnStep is told each decision before the slow step it leads to: before the
	// first idle check (it asks GitHub), when the blocking job changes, and
	// before the action. A drain that hangs is never silent (scheduling R5.63).
	OnStep func(line string)
	// Now and Sleep are replaceable for tests.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Act waits until the runner is idle, then runs act at once. The idle answer
// is read again immediately before every act call; act is never run on an
// older answer. On timeout it returns ErrDrainTimeout and does not run act.
func (d Drain) Act(ctx context.Context, act func(ctx context.Context) error) error {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	sleep := d.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	poll := d.Poll
	if poll <= 0 {
		poll = 5 * time.Second
	}
	warnAfter := d.WarnAfter
	if warnAfter <= 0 {
		warnAfter = DefaultWarnAfter
	}
	step := func(format string, a ...any) {
		if d.OnStep != nil {
			d.OnStep("drain: " + fmt.Sprintf(format, a...))
		}
	}
	start := now()
	warned := false
	lastJob := ""
	step("asking whether the runner is idle (polls every %s, the warning comes at %s)", poll, warnAfter)
	for {
		idle, job, err := d.Idle(ctx)
		if err != nil {
			return fmt.Errorf("the runner list cannot be read, so no change is made: %w", err)
		}
		if idle {
			step("the runner is idle; acting now")
			return act(ctx)
		}
		elapsed := now().Sub(start)
		if job != lastJob {
			step("waiting for job %s (%s so far); nothing is stopped", job, elapsed.Round(time.Second))
			lastJob = job
		}
		if !warned && elapsed >= warnAfter {
			warned = true
			if d.OnWarn != nil {
				d.OnWarn(job)
			}
		}
		if d.Timeout > 0 && elapsed >= d.Timeout {
			step("the drain timeout passed with job %s still running; nothing was stopped", job)
			return ErrDrainTimeout{Job: job}
		}
		if err := sleep(ctx, poll); err != nil {
			return err
		}
	}
}
