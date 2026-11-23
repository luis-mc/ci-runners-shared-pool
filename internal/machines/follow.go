package machines

import (
	"context"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
)

// AgentUpdate is what an agent last said about following the controller's
// version (operations R5.39 to R5.41).
type AgentUpdate struct {
	Version string
	State   string // idle, waiting_for_jobs, downloading, installing, restarting, postponed, failed, unsupported
	Error   string
	Waiting []JobRef
	At      time.Time
}

// Active says the agent is in the middle of an update, so asking again would only repeat.
func (a AgentUpdate) Active() bool {
	switch a.State {
	case "waiting_for_jobs", "downloading", "installing", "restarting":
		return true
	}
	return false
}

// AgentInfo is a connected agent-only machine and the version it runs.
type AgentInfo struct {
	ID      string
	Name    string
	Version string
	Update  AgentUpdate
}

// Agents lists the machines that run an agent and are connected now, the
// controller's own machine apart: its update is the controller's.
func (s *Service) Agents(ctx context.Context) ([]AgentInfo, error) {
	ms, err := s.o.Repo.ListMachines(ctx)
	if err != nil {
		return nil, err
	}
	var out []AgentInfo
	for _, m := range ms {
		if m.IsController || !m.RemovedAt.IsZero() || !s.sessions.Connected(m.ID) {
			continue
		}
		s.mu.Lock()
		u := s.updates[m.ID]
		s.mu.Unlock()
		out = append(out, AgentInfo{ID: m.ID, Name: m.Name, Version: m.AgentVersion, Update: u})
	}
	return out, nil
}

// AskUpdate asks a connected agent to update itself to version.
func (s *Service) AskUpdate(machineID, version string) error {
	s.noteAsked(machineID, version)
	return s.sessions.send(machineID, &agentv1.SessionResponse{Message: &agentv1.SessionResponse_UpdateAgent{
		UpdateAgent: &agentv1.UpdateAgent{CommandId: newID("cmd_"), Version: version}}})
}

// handleUpdateReport keeps the agent's last report and audits the endings.
func (s *Service) handleUpdateReport(ctx context.Context, id string, r *agentv1.UpdateReport) {
	u := AgentUpdate{Version: r.GetVersion(), State: r.GetState(), Error: r.GetError(), At: s.now()}
	for _, j := range r.GetWaitingFor() {
		u.Waiting = append(u.Waiting, jobFromProto(j))
	}
	s.mu.Lock()
	prev := s.updates[id]
	s.updates[id] = u
	s.mu.Unlock()
	if prev.State == u.State && prev.Version == u.Version {
		return
	}
	if u.State == "failed" || u.State == "unsupported" {
		s.failRestart(ctx, id, u.Error)
	}
	switch u.State {
	case "installing", "failed", "postponed", "unsupported":
		_ = s.o.Repo.Audit(ctx, "agent", "machine.agent_update."+u.State, id, u.Version+" "+u.Error)
	}
	s.notifyMachine(id)
}
