package machines

import (
	"strings"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// Engine is the container engine of a machine as its agent last reported it
// (machines R5.18, runners R5.47). The zero value is "nothing reported".
type Engine struct {
	Reported bool
	Kind     string // docker-desktop, docker-wsl2-windows-cli, docker-wsl2-wsl, docker-native or none
	Usable   bool
	Version  string
	Reason   string
	// ContainerOS is the OS the engine's containers run: linux or windows.
	// Empty when the engine does not answer or an older agent did not say.
	ContainerOS string
}

// RunsLinuxContainers is true when the engine answers and runs Linux
// containers, which is what a Linux runner needs on a Windows machine.
func (e Engine) RunsLinuxContainers() bool {
	return e.Reported && e.Usable && e.ContainerOS == "linux"
}

// HandleMachineReport records the engine of a MachineReport. The rest of the
// report (KVM, images) stays with the agent link and the doctor. A change
// reaches the console through the usual notification.
func (s *Service) HandleMachineReport(machineID string, r *agentv1.MachineReport) {
	s.HandleDockerDisk(machineID, r.GetDockerDisk())
	s.HandleRunnerServices(machineID, r.GetRunnerServices())
	e := r.GetEngine()
	if e == nil {
		return
	}
	next := Engine{Reported: true, Kind: e.GetKind(), Usable: e.GetUsable(), Version: e.GetVersion(), Reason: e.GetReason(),
		ContainerOS: strings.ToLower(strings.TrimSpace(e.GetContainerOs()))}
	if next.ContainerOS != "linux" && next.ContainerOS != "windows" {
		next.ContainerOS = ""
	}
	s.mu.Lock()
	old, had := s.engines[machineID]
	s.engines[machineID] = next
	s.mu.Unlock()
	if !had || old != next {
		s.notifyMachine(machineID)
	}
}

// Engine returns the newest engine report of a machine.
func (s *Service) Engine(machineID string) Engine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engines[machineID]
}
