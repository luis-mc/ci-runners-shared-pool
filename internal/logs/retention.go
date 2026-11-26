package logs

import (
	"context"
	"fmt"
	"time"
)

// A Bound is the default, floor and ceiling of one limit (observability R6.8).
type Bound struct{ Default, Floor, Ceiling int64 }

// Check refuses a value outside the floor and ceiling and names the range.
func (b Bound) Check(name string, v int64, unit string) error {
	if v < b.Floor || v > b.Ceiling {
		return fmt.Errorf("%s must be between %d and %d %s", name, b.Floor, b.Ceiling, unit)
	}
	return nil
}

// The limits of R6.8 for logs. Owners change the first two in Settings
// (internal/settings keeps the same range, and a test holds them equal); the
// agent buffer cap is a per-machine value.
var (
	// RetentionDays is how long runner setup and agent logs stay on the controller.
	RetentionDays = Bound{Default: 14, Floor: 1, Ceiling: 90}
	// RetentionMiBPerMachine is the size cap per machine.
	RetentionMiBPerMachine = Bound{Default: 512, Floor: 64, Ceiling: 10 * 1024}
	// AgentBufferMiB is the agent's disk buffer while it cannot reach the controller (R5.23).
	AgentBufferMiB = Bound{Default: 64, Floor: 8, Ceiling: 1024}
)

// Prune deletes log lines past days and, per machine, past mibPerMachine, by
// day. Whichever limit is reached first applies; the oldest data goes first.
// Values outside the floor and ceiling are refused.
func Prune(ctx context.Context, s Store, now time.Time, days, mibPerMachine int) (int64, error) {
	if err := RetentionDays.Check("log retention", int64(days), "days"); err != nil {
		return 0, err
	}
	if err := RetentionMiBPerMachine.Check("log size per machine", int64(mibPerMachine), "MiB"); err != nil {
		return 0, err
	}
	return s.Prune(ctx, now.Add(-time.Duration(days)*24*time.Hour), int64(mibPerMachine)<<20)
}
