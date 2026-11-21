package agent

import (
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/load"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/power"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

// Gate is the machine's own job-started gate (machines R5.23, runner-pool
// R5.2, DL-79). A new job starts only when all hold: the state is active, a
// slot is free, the measured load is under the budget in force, the job's
// backend is allowed. It reads the newest load sample and never waits for one
// (R6.6). It only holds jobs; it never stops one (R5.21).
//
// The same lock keeps new jobs out and counts the running ones, so the idle
// check of a drain and the hold are one step (R5.32): once Hold has returned
// idle, no job can have started in between.
type Gate struct {
	mu       sync.Mutex
	settings machines.Settings
	have     bool
	state    string // active, draining or drained
	running  map[string]machines.JobRef
	// scope holds the runners a scoped drain keeps new jobs out of (R5.57); nil
	// means no scoped drain.
	scope map[string]bool
	// held names the runners that take no new job while a cache wipe waits.
	held    map[string]bool
	sampler *load.Sampler
	// baseGaps are the runner base images this machine lacks (runners R5.53).
	baseGaps []BaseImage
	// floor is the free-disk floor before a job or VM starts (runners R5.83); nil means none.
	floor *runnercache.Floor

	// obs sees every job that starts and ends (OPS66 R5.62); nil sees none.
	obs JobObserver

	// The machine policy (CRSP75, R5.61 to R5.64): the power source and the clock.
	power power.Reader
	now   func() time.Time
	pwMu  sync.Mutex
	pwAt  time.Time
	pwNow power.State
}

// NewGate returns a gate over a load sampler. Until settings arrive it holds
// every job: an agent that knows no settings must not start work.
func NewGate(s *load.Sampler) *Gate {
	return &Gate{state: machines.StateActive, running: map[string]machines.JobRef{}, sampler: s}
}

// SetDiskFloor sets the free-disk floor (runners R5.83). nil removes it.
func (g *Gate) SetDiskFloor(f *runnercache.Floor) {
	g.mu.Lock()
	g.floor = f
	g.mu.Unlock()
}

// SetSettings applies settings (R5.24, R5.25): running jobs keep going even
// when the new budget is already exceeded.
func (g *Gate) SetSettings(s machines.Settings, state string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.settings, g.have = s, true
	// The controller owns the machine state; the agent follows it.
	switch state {
	case machines.StateActive, machines.StateDraining, machines.StateDrained:
		g.state = state
	}
}

// Settings returns the settings in force.
func (g *Gate) Settings() (machines.Settings, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.settings, g.have
}

// JobObserver sees the jobs of the gate. Both calls must be quick and never call back into the gate. cut
// says the job did not end by itself: the agent stopped, or the runner went away.
type JobObserver interface {
	JobStarted(key string, job machines.JobRef, backend string)
	JobFinished(key string, job machines.JobRef, cut bool)
}

// SetObserver sets the observer of started and finished jobs; nil removes it.
func (g *Gate) SetObserver(o JobObserver) {
	g.mu.Lock()
	g.obs = o
	g.mu.Unlock()
}

// Decision is the gate's answer.
type Decision struct {
	Go      bool
	Reasons []string
}

// Start asks to start a job on a backend. When it answers Go the job counts as
// running until Finish.
func (g *Gate) Start(key string, job machines.JobRef, backend string) Decision {
	d, started, obs := g.start(key, job, backend)
	if d.Go && obs != nil {
		obs.JobStarted(key, started, backend)
	}
	return d
}

func (g *Gate) start(key string, job machines.JobRef, backend string) (Decision, machines.JobRef, JobObserver) {
	ps := g.currentPower()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.have {
		return Decision{Reasons: []string{"settings not received yet"}}, job, nil
	}
	var why []string
	if g.state != machines.StateActive {
		why = append(why, "machine_"+g.state)
	}
	if g.scope[strings.ToLower(job.RunnerName)] {
		why = append(why, "runner_draining")
	}
	if job.RunnerName != "" && g.held[job.RunnerName] {
		why = append(why, "cache_wipe")
	}
	if g.state == machines.StateActive {
		if acc := g.acceptanceLocked(ps, g.clock()); !acc.Accepting && acc.Reason != machines.PauseReasonBudget {
			why = append(why, acc.Reason)
		}
	}
	if len(g.running) >= g.settings.JobSlots {
		why = append(why, "free_slot")
	}
	allowed := false
	for _, b := range g.settings.AllowedBackends {
		if b == backend {
			allowed = true
		}
	}
	if !allowed {
		why = append(why, "backend_not_allowed")
	}
	rep, ok := g.sampler.Latest()
	if good, reasons := load.Decide(rep, ok, g.settings.BudgetMemoryPct, g.settings.BudgetCPUPct); !good {
		why = append(why, reasons...)
	}
	if len(why) > 0 {
		return Decision{Reasons: why}, job, nil
	}
	if job.StartedAt.IsZero() {
		job.StartedAt = time.Now().UTC()
	}
	g.running[key] = job
	return Decision{Go: true}, job, g.obs
}

// Finish records that a job ended.
func (g *Gate) Finish(key string) { g.finish(key, false) }

// FinishCut records that a job ended without ending by itself: the agent stops or the runner is gone.
func (g *Gate) FinishCut(key string) { g.finish(key, true) }

func (g *Gate) finish(key string, cut bool) {
	g.mu.Lock()
	job, was := g.running[key]
	delete(g.running, key)
	obs := g.obs
	g.mu.Unlock()
	if was && obs != nil {
		obs.JobFinished(key, job, cut)
	}
}

// RunningKeys lists the keys of the running jobs.
func (g *Gate) RunningKeys() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, 0, len(g.running))
	for k := range g.running {
		out = append(out, k)
	}
	return out
}

// Running lists the running jobs.
func (g *Gate) Running() []machines.JobRef {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]machines.JobRef, 0, len(g.running))
	for _, j := range g.running {
		out = append(out, j)
	}
	return out
}

// SlotsUsed is how many jobs run.
func (g *Gate) SlotsUsed() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.running)
}

// Hold keeps new jobs out and, in the same step, reports whether the machine
// is idle and which jobs it still waits for (R5.30, R5.32). Call it again and
// again until idle is true; each call is a fresh check.
func (g *Gate) Hold() (idle bool, waitingFor []machines.JobRef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == machines.StateActive {
		g.state = machines.StateDraining
	}
	for _, j := range g.running {
		waitingFor = append(waitingFor, j)
	}
	return len(g.running) == 0, waitingFor
}

// HoldRunners is Hold for a runner set: it keeps new jobs out of those runners
// only and reports idle when no job runs on one of them, whatever the other
// runners of the machine do (R5.57). The idle check and the hold are one step,
// as in Hold (R5.32). No runner names is Hold for the whole machine.
func (g *Gate) HoldRunners(runners []string) (idle bool, waitingFor []machines.JobRef) {
	if len(runners) == 0 {
		return g.Hold()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.scope == nil {
		g.scope = map[string]bool{}
	}
	for _, r := range runners {
		g.scope[strings.ToLower(r)] = true
	}
	for _, j := range g.running {
		if g.scope[strings.ToLower(j.RunnerName)] {
			waitingFor = append(waitingFor, j)
		}
	}
	return len(waitingFor) == 0, waitingFor
}

// Release lets new jobs start again (resume, R5.34).
func (g *Gate) Release() {
	g.mu.Lock()
	g.scope = nil
	g.state = machines.StateActive
	g.mu.Unlock()
}

// State is active, draining or drained.
func (g *Gate) State() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}

// MarkDrained records that the drain finished: the machine stays held.
func (g *Gate) MarkDrained() {
	g.mu.Lock()
	g.state = machines.StateDrained
	g.mu.Unlock()
}

// HoldRunner keeps new jobs of one runner out and, in the same step, reports
// whether that runner is idle and which jobs it still waits for (caches R5.32).
// Call it again until idle is true; each call is a fresh check.
func (g *Gate) HoldRunner(name string) (idle bool, waitingFor []machines.JobRef) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held == nil {
		g.held = map[string]bool{}
	}
	g.held[name] = true
	for _, j := range g.running {
		if j.RunnerName == name {
			waitingFor = append(waitingFor, j)
		}
	}
	return len(waitingFor) == 0, waitingFor
}

// ReleaseRunner lets the runner take jobs again.
func (g *Gate) ReleaseRunner(name string) {
	g.mu.Lock()
	delete(g.held, name)
	g.mu.Unlock()
}
