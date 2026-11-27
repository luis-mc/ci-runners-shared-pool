package hooks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// Exit codes of the job-started hook.
const (
	ExitGo      = 0 // the job runs
	ExitTimeout = 1 // on timeout fail: the job fails and can be re-run (R5.20)
)

// GateConfig is everything one run of the job-started hook needs.
type GateConfig struct {
	// Client is the agent link. Nil, or a Client that fails, means the gate
	// waits on local load only (R5.23).
	Client Client
	// Machine settings used when the agent cannot say (the defaults).
	Machine slots.Settings
	Gate    slots.GateSettings
	Job     scheduler.JobRef
	// MachineID is filled from the agent's hello when empty.
	MachineID  string
	EventClass int
	Holder     scheduler.Holder
	// BudgetMemoryPct and BudgetCPUPct are a repository-lowered budget from the
	// runner environment; never above the machine's (R5.4).
	BudgetMemoryPct, BudgetCPUPct int
	GateID                        string
	// Measure reads this machine's load for the request and for the load-only wait.
	Measure func() load.Reading
	// Disk reads the free space of the runner's volume for the disk guard
	// (caches R5.38). Nil turns the guard off. A read error never holds a job.
	Disk func() (runnercache.Disk, error)
	// Caches are the cache folders to bring under their caps before the job
	// starts (caches R5.45); CacheFS is the file access, nil the real one.
	Caches  []runnercache.Cap
	CacheFS runnercache.FS
	// Clean is the always-clean rule of a persistent runner; it runs before the
	// job asks for a slot and holds the job while the runner is unclean. Nil
	// for a hook that does not belong to a runner of the product.
	Clean func() CleanResult
	// MinFreePct is the guard threshold; 0 takes the default (10%).
	MinFreePct int
	// Log writes one line; it goes to the job's Set up job step and the agent log (R5.21).
	Log func(string)
	// Report puts a ::warning annotation on the job and records the problem
	// for the doctor; Healthy clears the record (R5.61). Both may be nil.
	Report  func(string)
	Healthy func()
	// Top names the top processes for hold and timeout lines (R5.46); may be nil.
	Top   func() string
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration)
}

func (c *GateConfig) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *GateConfig) sleep(ctx context.Context, d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// unreachable tells the owner, not only the log, that the gate is off (R5.61).
func (c *GateConfig) unreachable(err error) {
	if c.Report != nil {
		c.Report(unreachableText("job-started", err))
	}
}

func (c *GateConfig) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log("load gate: " + fmt.Sprintf(format, a...))
	}
}

// warnUnmeasured puts one ::warning on the job: the load cannot be measured,
// the job waits, and the doctor says why (R5.22, decision D53).
func (c *GateConfig) warnUnmeasured() {
	if c.Report != nil {
		c.Report("this machine cannot measure its load, so the job waits (it never runs unmeasured); run `crsp doctor` and fix check machine.load.measured")
	}
}

// budget is the machine's budget, lowered by the repository's, never raised (R5.4).
func (c *GateConfig) budget() load.Budget {
	lb := slots.Limits(slots.Budget{MemoryPct: c.Machine.BudgetMemoryPct, CPUPct: c.Machine.BudgetCPUPct}, c.BudgetMemoryPct, c.BudgetCPUPct)
	return load.Budget{MemoryPct: lb.MemoryPct, CPUPct: lb.CPUPct}
}

func toLoad(r load.Reading) scheduler.Load {
	return scheduler.Load{MemoryPct: r.MemoryPct, MemoryKnown: r.MemoryKnown, CPUPct: r.CPUPct, CPUKnown: r.CPUKnown, SampledAt: r.At}
}

// timedOut is true when the next poll would pass the maximum wait (R5.20).
func timedOut(elapsed, poll, maxWait time.Duration) bool { return elapsed+poll > maxWait }

// RunGate runs the job-started hook and returns its exit code (R5.12 to R5.23).
func RunGate(ctx context.Context, cfg GateConfig) int {
	start := cfg.now()
	poll := time.Duration(cfg.Gate.PollSec) * time.Second
	maxWait := time.Duration(cfg.Gate.MaxWaitMin) * time.Minute
	if cfg.Measure == nil {
		cfg.Measure = func() load.Reading { return load.Reading{} }
	}
	budget := cfg.budget()

	// Decide in the log before the first step that can be slow: a hang after
	// this line is never silence (scheduling R5.63).
	cfg.logf("job-started: checking disk space, then asking the agent (limit %.0f s per call); if it does not answer the job goes on with a warning", CallTimeout.Seconds())
	if code, held := cleanHold(ctx, &cfg, start, poll, maxWait); held {
		return code
	}
	// The Rust cap runs first: a full cache folder is what the disk guard would
	// otherwise hold the job for (caches R5.45).
	if len(cfg.Caches) > 0 {
		SweepCaches(cfg.CacheFS, cfg.Caches, func(f string, a ...any) { cfg.logf(f, a...) })
	}
	if code, held := diskHold(ctx, &cfg, start, poll, maxWait); held {
		return code
	}

	// Ask the agent who we are. A failure here is the load-only fallback.
	if cfg.Client != nil {
		cfg.logf("asking the agent who this machine is")
		h, err := cfg.Client.Hello(ctx)
		if err != nil {
			cfg.logf("agent not reachable (%v): waiting on this machine's load only", err)
			cfg.unreachable(err)
			cfg.Client = nil
		} else {
			if cfg.MachineID == "" {
				cfg.MachineID = h.MachineID
			}
			cfg.Machine = h.Settings
			if cfg.Healthy != nil {
				cfg.Healthy()
			}
			budget = cfg.budget()
		}
	}
	if cfg.Client == nil {
		return loadOnlyWait(ctx, &cfg, start, poll, maxWait, budget)
	}

	var firstQueued time.Time
	var lastReasons string
	began := false
	haveTicket := false
	warned := false
	for {
		reading := cfg.Measure()
		var dec scheduler.Decision
		var err error
		if haveTicket {
			dec, err = cfg.Client.Beat(ctx, cfg.MachineID, cfg.GateID, toLoad(reading))
			if errors.Is(err, scheduler.ErrTicketGone) {
				cfg.logf("our ticket was gone; queuing again and keeping our place")
				haveTicket = false
			}
		}
		if !haveTicket {
			if firstQueued.IsZero() {
				cfg.logf("asking the agent for a slot")
			}
			dec, err = cfg.Client.Gate(ctx, scheduler.GateRequest{
				GateID: cfg.GateID, MachineID: cfg.MachineID, Job: cfg.Job, EventClass: cfg.EventClass, Holder: cfg.Holder,
				FirstQueued: firstQueued, Load: toLoad(reading), BudgetMemoryPct: cfg.BudgetMemoryPct, BudgetCPUPct: cfg.BudgetCPUPct})
			if err == nil {
				haveTicket = true
				if firstQueued.IsZero() {
					firstQueued = cfg.now()
				}
			}
		}
		if err != nil && !errors.Is(err, scheduler.ErrTicketGone) {
			// The agent or the controller went away: wait on local load only for what is left.
			cfg.logf("%v: waiting on this machine's load only", err)
			cfg.unreachable(err)
			if haveTicket {
				// Take our place out of the queue, with a short limit, so the
				// job that goes on without a slot does not also hold one.
				cctx, stop := context.WithTimeout(context.WithoutCancel(ctx), CallTimeout)
				_ = cfg.Client.Cancel(cctx, cfg.MachineID, cfg.GateID)
				stop()
			}
			return loadOnlyWait(ctx, &cfg, start, poll, maxWait, budget)
		}
		if len(dec.Go) > 0 {
			g := dec.Go[len(dec.Go)-1]
			cfg.logf("slot %s taken after %.0f s", slots.SlotLabel(slots.Kind(g.Kind), g.Number), cfg.now().Sub(start).Seconds())
			return ExitGo
		}
		unmeasured := slices.Contains(dec.Wait, scheduler.WaitLoadNotMeasured)
		if dec.Timeout != "" {
			return finishTimeout(ctx, &cfg, dec.Timeout, nil, haveTicket, unmeasured)
		}
		// Hold.
		if unmeasured && !warned {
			warned = true
			cfg.warnUnmeasured()
		}
		reasons := dec.WaitText
		if reasons == "" {
			reasons = "waiting"
		}
		if !began {
			cfg.logf("holding: %s (poll %d s, max wait %d min)", reasons, cfg.Gate.PollSec, cfg.Gate.MaxWaitMin)
			began = true
			lastReasons = reasons
		} else if reasons != lastReasons {
			cfg.logf("still holding: %s", reasons)
			lastReasons = reasons
		}
		if timedOut(cfg.now().Sub(start), poll, maxWait) {
			return finishTimeout(ctx, &cfg, cfg.Gate.OnTimeout, []string{lastReasons}, true, unmeasured)
		}
		cfg.sleep(ctx, poll)
		if ctx.Err() != nil {
			_ = cfg.Client.Cancel(context.Background(), cfg.MachineID, cfg.GateID)
			return ExitTimeout
		}
	}
}

// finishTimeout removes the ticket and exits by the on-timeout rule (R5.20).
// A job held because its load is not measured never runs on timeout: the rule
// "run" becomes "fail" for it, with the reason in the log (R5.22, D53).
func finishTimeout(ctx context.Context, cfg *GateConfig, onTimeout string, reasons []string, cancel, unmeasured bool) int {
	if cancel && cfg.Client != nil {
		cctx, stop := context.WithTimeout(context.WithoutCancel(ctx), CallTimeout)
		defer stop()
		_ = cfg.Client.Cancel(cctx, cfg.MachineID, cfg.GateID)
	}
	line := fmt.Sprintf("timed out after waiting for %s", strings.Join(reasons, "; "))
	if cfg.Top != nil {
		if top := cfg.Top(); top != "" {
			line += " (" + top + ")"
		}
	}
	if unmeasured {
		onTimeout = slots.OnTimeoutFail
	}
	if onTimeout == slots.OnTimeoutRun {
		cfg.logf("%s; the job runs without a slot", line)
		return ExitGo
	}
	cfg.logf("%s; the job fails and can be re-run", line)
	return ExitTimeout
}

// loadOnlyWait is the fallback when the agent or controller cannot be reached:
// poll the load until under budget, with the same maximum wait and on-timeout
// rule. When the load cannot be measured the job waits too, and on timeout it
// fails, never runs (R5.22, R5.23, decision D53).
func loadOnlyWait(ctx context.Context, cfg *GateConfig, start time.Time, poll, maxWait time.Duration, b load.Budget) int {
	var last string
	began := false
	warned := false
	for {
		r := cfg.Measure()
		unmeasured := !r.Measurable()
		over, why := load.Over(r, b)
		if unmeasured {
			over, why = true, []string{scheduler.LoadNotMeasuredText}
			if !warned {
				warned = true
				cfg.warnUnmeasured()
			}
		}
		if !over {
			cfg.logf("under budget (memory %.0f%%, CPU %.0f%%): the job runs without a slot count", r.MemoryPct, r.CPUPct)
			return ExitGo
		}
		reasons := strings.Join(why, ", ")
		if !began {
			cfg.logf("holding on load only: %s (poll %d s, max wait %d min)", reasons, cfg.Gate.PollSec, cfg.Gate.MaxWaitMin)
			began, last = true, reasons
		} else if reasons != last {
			cfg.logf("still holding: %s", reasons)
			last = reasons
		}
		if timedOut(cfg.now().Sub(start), poll, maxWait) {
			return finishTimeout(ctx, cfg, cfg.Gate.OnTimeout, []string{last}, false, unmeasured)
		}
		cfg.sleep(ctx, poll)
		if ctx.Err() != nil {
			return ExitTimeout
		}
	}
}

// diskHold holds the job while the runner's volume has under the minimum free
// space (caches R5.38). The job waits and is not failed; it goes on as soon as
// the guard lets go (with a 2 point margin, so it does not flap). The guard
// uses the gate's maximum wait; when that passes the job fails and can be
// re-run, whatever the on-timeout rule says, because a job that runs on a full
// disk can stop every runner on the machine. The second result is true when
// the hook has its answer (the exit code); false means go on to the queue.
func diskHold(ctx context.Context, cfg *GateConfig, start time.Time, poll, maxWait time.Duration) (int, bool) {
	if cfg.Disk == nil {
		return 0, false
	}
	g := runnercache.NewGuard(cfg.MinFreePct)
	began := false
	for {
		d, err := cfg.Disk()
		if err != nil {
			cfg.logf("disk guard: cannot read the disk (%v); the job goes on", err)
			return 0, false
		}
		held, why := g.Check(d)
		if !held {
			if began {
				cfg.logf("disk guard: %.1f%% free, the job goes on after %.0f s", d.FreePct(), cfg.now().Sub(start).Seconds())
			}
			return 0, false
		}
		if !began {
			cfg.logf("disk guard: holding the job: %s (poll %d s, max wait %d min)", why, cfg.Gate.PollSec, cfg.Gate.MaxWaitMin)
			began = true
		}
		if timedOut(cfg.now().Sub(start), poll, maxWait) {
			cfg.logf("disk guard: timed out after waiting for disk space (%s); the job fails and can be re-run", why)
			return ExitTimeout, true
		}
		cfg.sleep(ctx, poll)
		if ctx.Err() != nil {
			return ExitTimeout, true
		}
	}
}

// cleanHold runs the always-clean rule before the job asks for a slot. A runner
// that cannot clean takes no job: the hook retries the clean-up at every poll
// and holds, up to the maximum wait, and then fails the job so it can be re-run
// (runners R5.91). A runner with no clean-up (a hook run by hand) is not held.
func cleanHold(ctx context.Context, cfg *GateConfig, start time.Time, poll, maxWait time.Duration) (int, bool) {
	if cfg.Clean == nil {
		return 0, false
	}
	began := false
	for {
		res := cfg.Clean()
		why := res.Unclean()
		if why == "" {
			if began {
				cfg.logf("clean-up: the runner is clean, the job goes on after %.0f s", cfg.now().Sub(start).Seconds())
			}
			return 0, false
		}
		if !began {
			cfg.logf("clean-up: holding the job, this runner is not clean: %s (poll %d s, max wait %d min)", why, cfg.Gate.PollSec, cfg.Gate.MaxWaitMin)
			if cfg.Report != nil {
				cfg.Report("this runner is not clean (" + why + "), so it takes no job until it is")
			}
			began = true
		}
		if timedOut(cfg.now().Sub(start), poll, maxWait) {
			cfg.logf("clean-up: timed out, the runner is still not clean; the job fails and can be re-run")
			return ExitTimeout, true
		}
		cfg.sleep(ctx, poll)
		if ctx.Err() != nil {
			return ExitTimeout, true
		}
	}
}
