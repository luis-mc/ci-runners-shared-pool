package machinehealth

import (
	"context"
	"fmt"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// Windows of the job stats.
const (
	Window24h = "24h"
	Window7d  = "7d"
)

// WindowDuration returns the length of a window name.
func WindowDuration(w string) (time.Duration, error) {
	switch w {
	case Window24h:
		return 24 * time.Hour, nil
	case Window7d:
		return 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("the window must be %s or %s", Window24h, Window7d)
}

// Rates are the counts as fractions of the jobs of the window; nil when no job ran.
type Rates struct {
	Succeeded, Failed, Cancelled, TimedOut, OverMemory, Stopped *float64
}

// Stats are the job stats of one machine or runner.
type Stats struct {
	store.JobStats
	Rates Rates
	// BusyPct is the share of the window's capacity that jobs ran, 0 to 100; nil when the capacity is not known.
	BusyPct *float64
}

// makeStats adds the rates and the busy percent; capacity is how many jobs the subject runs at once (the machine's slots, 1 for a runner).
func makeStats(js store.JobStats, window time.Duration, capacity int) Stats {
	st := Stats{JobStats: js}
	if total := js.Counts.Total(); total > 0 {
		f := func(n int64) *float64 { v := float64(n) / float64(total); return &v }
		st.Rates = Rates{f(js.Counts.Succeeded), f(js.Counts.Failed), f(js.Counts.Cancelled), f(js.Counts.TimedOut), f(js.Counts.OverMemory), f(js.Counts.Stopped)}
	}
	if capacity > 0 {
		p := 100 * js.BusySeconds / (window.Seconds() * float64(capacity))
		if p > 100 {
			p = 100
		}
		st.BusyPct = &p
	}
	return st
}

// RunnerStats are the stats of one runner of a machine.
type RunnerStats struct {
	RunnerID, RunnerName string
	Stats                Stats
}

// MachineStats are the stats of a machine and of the runners that ran jobs on it.
type MachineStats struct {
	Window   string
	From, To time.Time
	Machine  Stats
	Runners  []RunnerStats
}

// MachineJobStats reads the stats of a machine and of each of its runners over a window.
func (s *Service) MachineJobStats(ctx context.Context, machineID, window string) (MachineStats, error) {
	d, err := WindowDuration(window)
	if err != nil {
		return MachineStats{}, err
	}
	to := s.now()
	from := to.Add(-d)
	slots := 1
	if s.Slots != nil {
		if n := s.Slots(ctx, machineID); n > 0 {
			slots = n
		}
	}
	m, err := s.Store.JobStats(ctx, store.JobStatsScope{MachineID: machineID}, from, to)
	if err != nil {
		return MachineStats{}, err
	}
	out := MachineStats{Window: window, From: from, To: to, Machine: makeStats(m, d, slots)}
	refs, err := s.Store.MachineRunnersWithJobs(ctx, machineID, from, to)
	if err != nil {
		return MachineStats{}, err
	}
	for _, r := range refs {
		js, err := s.Store.JobStats(ctx, store.JobStatsScope{RunnerID: r.ID}, from, to)
		if err != nil {
			return MachineStats{}, err
		}
		out.Runners = append(out.Runners, RunnerStats{RunnerID: r.ID, RunnerName: r.Name, Stats: makeStats(js, d, 1)})
	}
	return out, nil
}

// RunnerJobStats reads the stats of one runner over a window.
func (s *Service) RunnerJobStats(ctx context.Context, runnerID, window string) (RunnerStats, time.Time, time.Time, error) {
	d, err := WindowDuration(window)
	if err != nil {
		return RunnerStats{}, time.Time{}, time.Time{}, err
	}
	to := s.now()
	from := to.Add(-d)
	js, err := s.Store.JobStats(ctx, store.JobStatsScope{RunnerID: runnerID}, from, to)
	if err != nil {
		return RunnerStats{}, from, to, err
	}
	return RunnerStats{RunnerID: runnerID, Stats: makeStats(js, d, 1)}, from, to, nil
}
