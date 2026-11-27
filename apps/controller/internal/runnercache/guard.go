package runnercache

import "fmt"

// Disk is the free space of a runner's volume.
type Disk struct{ Free, Total int64 }

// FreePct is the free share in percent; 0 when the volume size is unknown.
func (d Disk) FreePct() float64 {
	if d.Total <= 0 {
		return 0
	}
	return float64(d.Free) * 100 / float64(d.Total)
}

// Guard decides whether new jobs on a machine are held for lack of disk
// space. Held jobs wait; they are not failed. The guard holds under minPct
// free and resumes at minPct plus ResumeMarginPct, so it does not flap.
type Guard struct {
	minPct int
	held   bool
	// OnChange is called once when the guard starts or stops holding; the
	// alert goes through it. It may be nil.
	OnChange func(held bool, reason string)
}

// NewGuard makes a guard with a threshold; 0 takes the default.
func NewGuard(minPct int) *Guard {
	if minPct <= 0 {
		minPct = DefaultMinFreePct
	}
	return &Guard{minPct: minPct}
}

// Held says whether new jobs are held now.
func (g *Guard) Held() bool { return g.held }

// Check takes a disk reading and returns whether new jobs are held, with the
// reason when they are. A volume of unknown size never holds a job (the
// doctor reports the missing figure).
func (g *Guard) Check(d Disk) (bool, string) {
	if d.Total <= 0 {
		return g.held, ""
	}
	pct := d.FreePct()
	switch {
	case !g.held && pct < float64(g.minPct):
		g.held = true
		reason := fmt.Sprintf("disk %.1f%% free < %d%%", pct, g.minPct)
		if g.OnChange != nil {
			g.OnChange(true, reason)
		}
		return true, reason
	case g.held && pct >= float64(g.minPct+ResumeMarginPct):
		g.held = false
		if g.OnChange != nil {
			g.OnChange(false, fmt.Sprintf("disk %.1f%% free, jobs go again", pct))
		}
		return false, ""
	case g.held:
		return true, fmt.Sprintf("disk %.1f%% free < %d%%", pct, g.minPct+ResumeMarginPct)
	}
	return false, ""
}
