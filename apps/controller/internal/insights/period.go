package insights

import "time"

// Mode is how often the engine re-analyses (owner decisions D18, D19). The default is after every finished CI
// run; the owner can choose a slower one in Settings when the cost warning (KindCost) recommends it.
type Mode string

// Modes.
const (
	// ModeEveryRun analyses after each CI run (a job reaching done), and on the 15 minute timer as a safety net.
	ModeEveryRun Mode = "every_run"
	// ModeEveryNthRun analyses after every Nth finished CI run, and once an hour as a safety net.
	ModeEveryNthRun Mode = "every_nth_run"
	// ModeHourly analyses once an hour and never because of a finished run.
	ModeHourly Mode = "hourly"
)

// Update period limits: N is 2 to 50 (1 is every run).
const (
	MinEveryN     = 2
	MaxEveryN     = 50
	DefaultEveryN = 5
	// HourlyInterval is the timer of the slower modes.
	HourlyInterval = time.Hour
)

// Period is the update period setting.
type Period struct {
	Mode Mode
	N    int // ModeEveryNthRun only
}

// Valid says whether m is a known mode.
func (m Mode) Valid() bool { return m == ModeEveryRun || m == ModeEveryNthRun || m == ModeHourly }

// Norm returns p with an unknown mode as every run and N inside its range.
func (p Period) Norm() Period {
	if !p.Mode.Valid() {
		p.Mode = ModeEveryRun
	}
	if p.N < MinEveryN {
		p.N = MinEveryN
	}
	if p.N > MaxEveryN {
		p.N = MaxEveryN
	}
	return p
}

// Due says whether a pass should start now. finished is the number of CI runs that finished since the last
// pass, since is the time since it (zero when none ran yet). fromTimer is true for the timer tick, false for a
// finished run.
func (p Period) Due(finished int, since time.Duration, fromTimer, first bool) bool {
	p = p.Norm()
	if first {
		return true
	}
	switch p.Mode {
	case ModeEveryNthRun:
		if fromTimer {
			return since >= HourlyInterval
		}
		return finished >= p.N
	case ModeHourly:
		return fromTimer && since >= HourlyInterval
	default:
		if fromTimer {
			return since >= Interval
		}
		return finished >= 1
	}
}
