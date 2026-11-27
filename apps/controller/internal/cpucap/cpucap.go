// Package cpucap limits the CPU the runner processes of this machine may use
// (machines R5.64). The cap is a share of the whole machine: 50 means half of
// all cores. It is separate from the load budget of the admission gate.
//
// Windows puts the runner service process tree in a Job Object with CPU rate
// control. Linux writes cgroup v2 cpu.max for the runner services' cgroups.
// macOS is not supported yet and says so.
package cpucap

import "context"

// Result states.
const (
	Applied      = "applied"
	NotSupported = "not_supported"
	Failed       = "failed"
)

// Result is what an Apply did. Detail is a sentence fit for the console.
type Result struct {
	State  string
	Detail string
	// Targets is how many runner services the cap was set on.
	Targets int
}

// Applier sets the cap on the runner processes of this machine. It is safe to
// call again with the same or another percent: each call sets the cap on every
// runner service that exists now.
type Applier interface {
	Apply(ctx context.Context, pct int) Result
}

// ServicePrefix starts the name of every runner service of crsp and of the
// Actions runner's own tooling (runners R5.5).
const ServicePrefix = "actions.runner."

// Valid says whether pct is one of 25, 50, 75 or 100.
func Valid(pct int) bool {
	switch pct {
	case 25, 50, 75, 100:
		return true
	}
	return false
}

// New returns the Applier of this build's OS.
func New() Applier { return newOS() }

// Static is an Applier that answers one Result; tests and unsupported OSes.
type Static struct {
	R    Result
	Seen *[]int
}

// Apply implements Applier.
func (s Static) Apply(_ context.Context, pct int) Result {
	if s.Seen != nil {
		*s.Seen = append(*s.Seen, pct)
	}
	return s.R
}
