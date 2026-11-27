// Package runnerprocs lists and ends the processes a finished job left behind
// on a persistent runner (runners R5.91). It is the one place that ends a
// process on behalf of a runner clean-up: internal/hooks decides which
// processes are leftovers and asks this package; the hook and scheduler
// packages never start, list, signal or kill a process themselves
// (hooks R5.62 guard, scheduling R5.27 guard).
package runnerprocs

// Proc is one process of the machine.
type Proc struct {
	PID, PPID int64
	// Owner is the account of the process: the user id on Linux and macOS, the
	// SID on Windows. It has the form Self returns.
	Owner string
	Name  string
}
