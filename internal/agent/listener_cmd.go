package agent

import (
	"context"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// ListenerRunner is the runner backend's listener control (runners R5.134): the
// runners.LocalAgent has it. A backend without it answers that it cannot.
type ListenerRunner interface {
	ControlListener(ctx context.Context, action, service string, jobAssigned, maintenance bool) (backend.ListenerState, error)
}

var errNoListenerControl = errors.New("this agent cannot start or stop a runner listener")

// listenerControl carries out one ListenerControl command in its own goroutine
// (a clean stop waits for the listener to exit) and answers with a RunnerResult
// whose listener field is what the agent saw afterwards.
func (l *link) listenerControl(ctx context.Context, m *agentv1.ListenerControl) {
	go func() {
		lr, ok := l.cfg.Runners.(ListenerRunner)
		if !ok {
			l.answer(m.GetCommandId(), m.GetRunnerId(), false, "failed", "", errNoListenerControl)
			return
		}
		st, err := lr.ControlListener(ctx, m.GetAction(), m.GetServiceName(), m.GetJobAssigned(), m.GetMaintenance())
		res := &agentv1.RunnerResult{CommandId: m.GetCommandId(), Ok: err == nil, Outcome: outcomeOf(err), RunnerId: m.GetRunnerId(),
			ServiceRef: m.GetServiceName(), Listener: string(st)}
		if err != nil {
			res.Error = l.redactor().Redact(err.Error())
		}
		_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: res}})
	}()
}
