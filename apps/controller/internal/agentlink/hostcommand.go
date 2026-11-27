package agentlink

import (
	"context"
	"errors"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// ErrAgentCannotRun is a machine whose agent does not say `host_command` in its
// machine report: an older agent. Nothing is sent to it (cli R5.29).
var ErrAgentCannotRun = errors.New("the agent of this machine cannot run host commands yet")

var _ machines.HostRunner = (*Link)(nil)

// RunHostCommand sends argv to the machine's agent and waits for its answer.
// The agent runs it without a shell and only on a held, idle machine. A command
// that ran and failed (non-zero exit, timeout, a refusal by the agent) is not
// an error here: the outcome says Ran false or an exit code, and holds the
// output and the agent's text. An error means no answer came.
func (l *Link) RunHostCommand(ctx context.Context, machineID string, argv []string, timeout time.Duration) (machines.HostOutcome, error) {
	rep, ok := l.Reports()[machineID]
	if !ok || !rep.GetHostCommand() {
		return machines.HostOutcome{}, ErrAgentCannotRun
	}
	r, err := l.callWait(ctx, machineID, timeout+time.Minute, func(id string) *agentv1.SessionResponse {
		return &agentv1.SessionResponse{Message: &agentv1.SessionResponse_RunHostCommand{RunHostCommand: &agentv1.RunHostCommand{
			CommandId: id, Argv: argv, TimeoutSeconds: int32(timeout / time.Second), //nolint:gosec // bounded by machines.Service
		}}}
	})
	if r == nil {
		return machines.HostOutcome{}, err
	}
	out := machines.HostOutcome{OK: err == nil, ExitCode: int(r.GetExitCode()), Output: r.GetOutput()}
	if err != nil {
		out.Message = err.Error()
	}
	return out, nil
}
