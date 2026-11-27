package agentlink

import (
	"context"
	"errors"
	"fmt"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// Dependency setup over the agent link (spec dependency-setup, CRSP248). Every
// call is gated on the machine report's `dependency_setup` flag: an older agent
// gets nothing and an error that says to update it first.

// ErrAgentCannotSetup is a machine whose agent does not say `dependency_setup`
// in its machine report: an older agent.
var ErrAgentCannotSetup = errors.New("the agent of this machine is too old for this: update the agent first")

// DepsCheckWait is how long a check waits for the agent's report.
const DepsCheckWait = 2 * time.Minute

// DepsSink takes what the agents send back (depsetup.Service implements it).
type DepsSink interface {
	HandleEvent(ctx context.Context, machineID string, ev *agentv1.DepsEvent)
	OnAgentConnected(machineID string)
}

func (l *Link) depsReady(machineID string) error {
	rep, ok := l.Reports()[machineID]
	if !ok || !rep.GetDependencySetup() {
		return ErrAgentCannotSetup
	}
	return nil
}

// SendCheck asks the agent what the runner choice needs and waits for its report.
func (l *Link) SendCheck(ctx context.Context, machineID, runnerOS, runnerKind string) (*agentv1.DepsReport, error) {
	if err := l.depsReady(machineID); err != nil {
		return nil, err
	}
	id := newCommandID()
	ch := make(chan *agentv1.DepsReport, 1)
	l.mu.Lock()
	if l.pendingDeps == nil {
		l.pendingDeps = map[string]chan *agentv1.DepsReport{}
	}
	l.pendingDeps[id] = ch
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.pendingDeps, id); l.mu.Unlock() }()
	err := l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DepsCheck{DepsCheck: &agentv1.DepsCheck{
		CommandId: id, RunnerOs: runnerOS, RunnerKind: runnerKind}}})
	if err != nil {
		return nil, errors.New("the machine's agent is not connected")
	}
	t := time.NewTimer(DepsCheckWait)
	defer t.Stop()
	select {
	case r := <-ch:
		return r, nil
	case <-t.C:
		return nil, ErrNoAnswer
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// SendInstall sends the consented plan; the agent answers with DepsEvent messages.
func (l *Link) SendInstall(_ context.Context, machineID string, in *agentv1.DepsInstall) error {
	if err := l.depsReady(machineID); err != nil {
		return err
	}
	in.CommandId = newCommandID()
	return l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DepsInstall{DepsInstall: in}})
}

// SendUndo sends the recorded added list to remove.
func (l *Link) SendUndo(_ context.Context, machineID string, in *agentv1.DepsUndo) error {
	if err := l.depsReady(machineID); err != nil {
		return err
	}
	in.CommandId = newCommandID()
	return l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DepsUndo{DepsUndo: in}})
}

// SendRestart asks the agent to restart the machine; the agent refuses while a job runs.
func (l *Link) SendRestart(_ context.Context, machineID, jobID string) error {
	if err := l.depsReady(machineID); err != nil {
		return err
	}
	return l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DepsRestartHost{
		DepsRestartHost: &agentv1.DepsRestartHost{CommandId: newCommandID(), JobId: jobID}}})
}

// SendStop stops a running install. The agent reads a DepsInstall with no items
// and a command id of "stop:<job id>" as a stop (internal/agent/deps_link.go).
func (l *Link) SendStop(_ context.Context, machineID, jobID string) error {
	if err := l.depsReady(machineID); err != nil {
		return err
	}
	if err := l.Sender.Send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_DepsInstall{
		DepsInstall: &agentv1.DepsInstall{CommandId: "stop:" + jobID, JobId: jobID},
	}}); err != nil {
		return fmt.Errorf("the machine's agent is not connected: %w", err)
	}
	return nil
}

// onDeps takes the DepsReport answer and the DepsEvent messages.
func (l *Link) onDeps(machineID string, msg *agentv1.SessionRequest) bool {
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionRequest_DepsReport:
		l.mu.Lock()
		ch := l.pendingDeps[m.DepsReport.GetCommandId()]
		l.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m.DepsReport:
			default:
			}
		}
		return true
	case *agentv1.SessionRequest_DepsEvent:
		if l.Deps != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			l.Deps.HandleEvent(ctx, machineID, m.DepsEvent)
		}
		return true
	}
	return false
}

// afterReport runs when a machine report arrives: an agent that can set up
// dependencies and came back resumes its paused or lost job (DEP7).
func (l *Link) afterReport(machineID string, rep *agentv1.MachineReport) {
	if l.Deps != nil && rep.GetDependencySetup() {
		go l.Deps.OnAgentConnected(machineID)
	}
}
