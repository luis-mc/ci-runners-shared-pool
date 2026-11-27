package builds

import "context"

// Gate lets one build run on a machine at a time (RES19 decision D20, R5.68).
// A second build waits for the first; the waiting one asks again whether its
// image is there once it is let in, since the first may have built it.
type Gate struct{ slot chan struct{} }

// NewGate makes a gate that is open.
func NewGate() *Gate { return &Gate{slot: make(chan struct{}, 1)} }

// Acquire waits for the turn. The returned function gives it back; calling it
// twice is safe. A cancelled context ends the wait with its error.
func (g *Gate) Acquire(ctx context.Context) (release func(), err error) {
	select {
	case g.slot <- struct{}{}:
		var done bool
		return func() {
			if !done {
				done = true
				<-g.slot
			}
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Busy says whether a build holds the gate now.
func (g *Gate) Busy() bool { return len(g.slot) > 0 }

// Cores is how many cores a build may use: all of them when no job runs, and
// half (at least one) while jobs run (decision D20). total below 1 is 1.
func Cores(total int, jobsRunning bool) int {
	if total < 1 {
		total = 1
	}
	if !jobsRunning {
		return total
	}
	return max(1, total/2)
}
