//go:build !linux && !windows

package cpucap

import "context"

type osApplier struct{}

func newOS() Applier { return osApplier{} }

// Apply implements Applier: macOS has no CPU cap yet (R5.64). At 100 there is
// nothing to cap, and it says so.
func (osApplier) Apply(_ context.Context, pct int) Result {
	if pct >= 100 {
		return Result{State: NotSupported, Detail: "CPU cap is not supported on macOS yet; the runners use all the CPU"}
	}
	return Result{State: NotSupported, Detail: "CPU cap is not supported on macOS yet"}
}
