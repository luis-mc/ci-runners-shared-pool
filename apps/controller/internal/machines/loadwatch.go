package machines

import "time"

// LoadClearAfter is how long the load must stay under the budget before a load
// warning clears (scheduling R5.78). It is a few agent samples long (a report
// every 15 s), so one dip between two spikes does not clear the warning.
const LoadClearAfter = 60 * time.Second

// LoadWarning is set while a machine is over its load budget with jobs already
// running (scheduling R5.77). It never stops a job: the hold is the gate's.
type LoadWarning struct {
	// Since is when the warning was raised.
	Since time.Time
}

// OverBudget reports whether a load is over the budget of a machine: memory
// strictly above the memory budget or CPU strictly above the CPU budget. A
// figure that is not known never counts as over (scheduling R5.45).
func OverBudget(l Load, set Settings) bool {
	return (l.MemoryKnown && l.MemoryPct > float64(set.BudgetMemoryPct)) ||
		(l.CPUKnown && l.CPUPct > float64(set.BudgetCPUPct))
}

// watchState is the load watch of one machine.
type watchState struct {
	warn       bool
	since      time.Time
	underSince time.Time
}

// observe takes one load report: over says the load is over budget and running
// how many jobs hold a slot. It reports whether the warning is up.
func (w *watchState) observe(over bool, running int, now time.Time) bool {
	if over {
		w.underSince = time.Time{}
		if running > 0 && !w.warn {
			w.warn, w.since = true, now
		}
		return w.warn
	}
	if w.warn {
		if w.underSince.IsZero() {
			w.underSince = now
		}
		if now.Sub(w.underSince) >= LoadClearAfter {
			*w = watchState{}
		}
	}
	return w.warn
}

// watchLoad feeds one load report to the machine's watch (R5.77, R5.78).
func (s *Service) watchLoad(machineID string, l Load, set Settings, running int, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watches == nil {
		s.watches = map[string]*watchState{}
	}
	w := s.watches[machineID]
	if w == nil {
		w = &watchState{}
		s.watches[machineID] = w
	}
	w.observe(OverBudget(l, set), running, now)
}

// loadWarning returns the warning of a machine, or nil. The caller holds s.mu.
func (s *Service) loadWarningLocked(machineID string) *LoadWarning {
	if w := s.watches[machineID]; w != nil && w.warn {
		return &LoadWarning{Since: w.since}
	}
	return nil
}
