package machines

import (
	"slices"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// RunnerService is one runner service of a machine as its agent left it after
// it re-asserted the logon right of the service account (runners R5.89).
type RunnerService struct {
	Runner, Service, Account string
	Running                  bool
	// LogonKnown is false when the agent could not read the rights of the
	// account; LogonHeld says whether the account holds the right to log on
	// as a service.
	LogonKnown, LogonHeld bool
	// Regranted is true when the last pass gave the right back.
	Regranted bool
	// Problem is why the service does not run, after the agent's one retry.
	Problem string
}

// Failed says the service does not run and the agent could not start it: the
// console shows a Problem and the tray a notification until it runs.
func (r RunnerService) Failed() bool { return !r.Running && r.Problem != "" }

// HandleRunnerServices replaces the runner services of a machine with what its
// agent just reported. A report that leaves a service out, or says it runs,
// clears its problem. A change reaches the console through the usual
// notification.
func (s *Service) HandleRunnerServices(machineID string, rs []*agentv1.RunnerServiceReport) {
	next := make([]RunnerService, 0, len(rs))
	for _, r := range rs {
		next = append(next, RunnerService{
			Runner: r.GetRunnerName(), Service: r.GetServiceName(), Account: r.GetAccount(), Running: r.GetRunning(),
			LogonKnown: r.GetLogonKnown(), LogonHeld: r.GetLogonHeld(), Regranted: r.GetRegranted(), Problem: r.GetProblem(),
		})
	}
	s.mu.Lock()
	old := s.rsvcs[machineID]
	if len(next) == 0 {
		delete(s.rsvcs, machineID)
	} else {
		s.rsvcs[machineID] = next
	}
	s.mu.Unlock()
	if !slices.Equal(old, next) {
		s.notifyMachine(machineID)
	}
}

// RunnerServices returns the newest runner service report of a machine.
func (s *Service) RunnerServices(machineID string) []RunnerService {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.rsvcs[machineID])
}

// RunnerProblems are the services of the view that failed to start.
func (v View) RunnerProblems() []RunnerService {
	var out []RunnerService
	for _, r := range v.RunnerServices {
		if r.Failed() {
			out = append(out, r)
		}
	}
	return out
}
