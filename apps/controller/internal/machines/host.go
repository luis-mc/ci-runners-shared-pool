package machines

import (
	"context"
	"fmt"
	"time"
)

// Host maintenance (cli R5.29, HND24): one command on a drained machine, sent
// over the agent link. It never touches a job: the machine must be drained
// (new jobs held, running jobs finished) before anything is sent, and the agent
// checks the same again.

// HostOutcome is what a host command did.
type HostOutcome struct {
	// OK is true when the command exited with code 0.
	OK       bool
	ExitCode int
	// Output is the last bytes of the combined output, redacted by the agent.
	Output string
	// Message is the agent's text when OK is false.
	Message string
}

// HostRunner sends a command to a machine's agent (agentlink.Link). An error
// means no answer came; a command that ran and failed is an outcome.
type HostRunner interface {
	RunHostCommand(ctx context.Context, machineID string, argv []string, timeout time.Duration) (HostOutcome, error)
}

// DefaultHostTimeout and MaxHostTimeout bound one host command.
const (
	DefaultHostTimeout = 30 * time.Minute
	MaxHostTimeout     = 24 * time.Hour
)

// SetHostRunner sets the sender of host commands; call it before the API serves.
func (s *Service) SetHostRunner(h HostRunner) { s.host = h }

// RunHostCommand runs argv on a drained machine. It refuses a machine that
// still takes jobs or still waits for them (ErrConflict), so the caller drains
// first and the command never meets a running job.
func (s *Service) RunHostCommand(ctx context.Context, actor, id string, argv []string, timeout time.Duration) (HostOutcome, error) {
	if len(argv) == 0 || argv[0] == "" {
		return HostOutcome{}, fmt.Errorf("%w: no command to run", ErrInvalid)
	}
	if timeout == 0 {
		timeout = DefaultHostTimeout
	}
	if timeout < time.Second || timeout > MaxHostTimeout {
		return HostOutcome{}, fmt.Errorf("%w: the command timeout must be between 1s and 24h", ErrInvalid)
	}
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return HostOutcome{}, err
	}
	if !m.RemovedAt.IsZero() {
		return HostOutcome{}, ErrNotFound
	}
	if s.host == nil {
		return HostOutcome{}, ErrNotSupported
	}
	if m.State != StateDrained {
		return HostOutcome{}, fmt.Errorf("%w: drain the machine first; a command runs only when no job can start or run", ErrConflict)
	}
	if _, open, err := s.o.Repo.OpenChange(ctx, id); err != nil {
		return HostOutcome{}, err
	} else if open {
		return HostOutcome{}, fmt.Errorf("%w: a change is still waiting on this machine", ErrConflict)
	}
	// Only the program name is audited: arguments may carry values.
	_ = s.o.Repo.Audit(ctx, actor, "machine.host_command", id, argv[0])
	out, err := s.host.RunHostCommand(ctx, id, argv, timeout)
	if err != nil && ctx.Err() == nil {
		return HostOutcome{}, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return out, err
}
