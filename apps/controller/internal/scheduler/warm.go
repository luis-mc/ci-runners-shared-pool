package scheduler

import (
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// DefaultWarmMaxAge is how long a warm runner may wait before it is replaced,
// so it never runs from a stale image (scheduling R5.66).
const DefaultWarmMaxAge = 15 * time.Minute

// Warm holds, the reasons a machine keeps no warm runner (R5.66). They are the
// acceptance reasons of R5.68 and the free-disk floor of machines R5.60.
const (
	WarmHoldPaused    = "paused"
	WarmHoldBattery   = "on_battery"
	WarmHoldDiskFloor = "disk_floor"
)

// WarmRunner is one registered, idle runner kept ready on a machine.
type WarmRunner struct {
	Name      string
	StartedAt time.Time
}

// warmWeight is what one warm runner takes of the budget: one slot, as a job
// of an unknown class does (R5.69).
var warmWeight = slots.DefaultWeight

// WarmInput is what the planner reads. The machine has one OS, so the count
// `Settings.WarmRunners` is the count for that OS.
type WarmInput struct {
	Settings slots.Settings
	Held     []slots.Slot
	Warm     []WarmRunner
	Load     Load
	// Hold is "" or one of the WarmHold reasons.
	Hold   string
	Now    time.Time
	MaxAge time.Duration // 0 means DefaultWarmMaxAge
}

// WarmPlan is what to do: release these runners (oldest first), then start
// Start new ones. Replace names the released runners that were too old.
type WarmPlan struct {
	Release []string
	Replace []string
	Start   int
	Why     string
}

// PlanWarm decides the warm pool of one machine. A warm runner counts against
// the machine's slots and budget like a running job (R5.66): it is started only
// when the held weight plus the warm weight plus one more fits in both kinds,
// the load is measured and under budget, and nothing holds the machine. The
// same hold, a lower count, an age past the limit or a budget a real job took
// release it, oldest first.
func PlanWarm(in WarmInput) WarmPlan {
	maxAge := in.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultWarmMaxAge
	}
	warm := append([]WarmRunner(nil), in.Warm...)
	sort.SliceStable(warm, func(i, j int) bool {
		if !warm[i].StartedAt.Equal(warm[j].StartedAt) {
			return warm[i].StartedAt.Before(warm[j].StartedAt)
		}
		return warm[i].Name < warm[j].Name
	})
	var p WarmPlan
	names := func(l []WarmRunner) []string {
		out := make([]string, 0, len(l))
		for _, w := range l {
			out = append(out, w.Name)
		}
		return out
	}
	want := in.Settings.WarmRunners
	switch {
	case in.Hold != "":
		p.Release = names(warm)
		p.Why = "no warm runner: " + holdText(in.Hold)
		return p
	case want <= 0:
		p.Release = names(warm)
		return p
	}
	var keep []WarmRunner
	for _, w := range warm {
		if in.Now.Sub(w.StartedAt) >= maxAge {
			p.Release = append(p.Release, w.Name)
			p.Replace = append(p.Replace, w.Name)
			continue
		}
		keep = append(keep, w)
	}
	capacity, used := in.Settings.Capacity(), in.Settings.HeldWeight(in.Held)
	// Too many, or the budget went to real jobs: release the oldest first.
	for len(keep) > 0 && (len(keep) > want || !fits(capacity, used, len(keep))) {
		p.Release = append(p.Release, keep[0].Name)
		keep = keep[1:]
	}
	room := want - len(keep)
	for room > 0 && !fits(capacity, used, len(keep)+room) {
		room--
	}
	if room <= 0 {
		return p
	}
	measured := in.Load.MemoryKnown || in.Load.CPUKnown
	if !measured {
		p.Why = "no new warm runner: " + LoadNotMeasuredText
		return p
	}
	budget := slots.Budget{MemoryPct: in.Settings.BudgetMemoryPct, CPUPct: in.Settings.BudgetCPUPct}
	if over, text := overBudget(in.Load, budget); over {
		p.Why = "no new warm runner: load over budget (" + text + ")"
		return p
	}
	p.Start = room
	return p
}

// fits is true when the held weight plus n warm runners stays inside the budget
// in both kinds.
func fits(capacity, used slots.Weight, n int) bool {
	return used.CPU+n*warmWeight.CPU <= capacity.CPU && used.Memory+n*warmWeight.Memory <= capacity.Memory
}

func holdText(h string) string {
	switch h {
	case WarmHoldPaused:
		return "this machine is paused"
	case WarmHoldBattery:
		return "this machine is on battery"
	case WarmHoldDiskFloor:
		return "free disk is under the floor"
	}
	return h
}

// WarmToYield names the warm runners (oldest first) a real job of weight need
// takes the budget from: none when the job fits beside all of them, and never
// more than the job needs. Held is the weight of the held ci slots.
func WarmToYield(set slots.Settings, held []slots.Slot, warm []WarmRunner, need slots.Weight) []string {
	sorted := append([]WarmRunner(nil), warm...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].StartedAt.Equal(sorted[j].StartedAt) {
			return sorted[i].StartedAt.Before(sorted[j].StartedAt)
		}
		return sorted[i].Name < sorted[j].Name
	})
	capacity, used := set.Capacity(), set.HeldWeight(held)
	used.CPU += need.CPU
	used.Memory += need.Memory
	var out []string
	for n := len(sorted); n > 0 && !fits(capacity, used, n); n-- {
		out = append(out, sorted[len(sorted)-n].Name)
	}
	return out
}

// WithWarm sets where the gate reads the warm runners of a machine and how it
// asks one to be released (R5.66). A real job never waits for a warm runner:
// when the budget is needed, the gate admits the job and calls release with
// the names, oldest first; the agent drains them. release must not block and
// must be safe to call twice for the same name.
func WithWarm(list func(machineID string) []WarmRunner, release func(machineID string, names []string)) Option {
	return func(e *Engine) { e.warm, e.warmRelease = list, release }
}

// yieldWarm releases the warm runners a job about to take a slot needs the
// budget of. The runner the job itself runs on is the warm runner it uses, so
// it is not counted.
func (e *Engine) yieldWarm(machineID, runnerName string, set slots.Settings, all []slots.Slot, need slots.Weight) {
	if e.warm == nil || e.warmRelease == nil {
		return
	}
	var others []WarmRunner
	for _, w := range e.warm(machineID) {
		if w.Name != runnerName {
			others = append(others, w)
		}
	}
	if names := WarmToYield(set, all, others, need); len(names) > 0 {
		e.log.Info("warm runners released for a job", "machine", machineID, "runners", fmt.Sprint(names))
		e.warmRelease(machineID, names)
	}
}
