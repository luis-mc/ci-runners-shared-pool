package api

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// Dependency setup routes (dependency-setup spec, CRSP248). These are the
// contract stubs; the controller wiring replaces them.

// CheckMachineDependencies implements POST /api/v1/machines/{machineId}/dependencies/check.
func (s *server) CheckMachineDependencies(_ context.Context, _ gen.CheckMachineDependenciesRequestObject) (gen.CheckMachineDependenciesResponseObject, error) {
	return fail[gen.CheckMachineDependenciesdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// ListMachineDependencyJobs implements GET /api/v1/machines/{machineId}/dependencies/jobs.
func (s *server) ListMachineDependencyJobs(_ context.Context, _ gen.ListMachineDependencyJobsRequestObject) (gen.ListMachineDependencyJobsResponseObject, error) {
	return fail[gen.ListMachineDependencyJobsdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// StartMachineDependencyJob implements POST /api/v1/machines/{machineId}/dependencies/jobs.
func (s *server) StartMachineDependencyJob(_ context.Context, _ gen.StartMachineDependencyJobRequestObject) (gen.StartMachineDependencyJobResponseObject, error) {
	return fail[gen.StartMachineDependencyJobdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// GetMachineDependencyJob implements GET .../dependencies/jobs/{jobId}.
func (s *server) GetMachineDependencyJob(_ context.Context, _ gen.GetMachineDependencyJobRequestObject) (gen.GetMachineDependencyJobResponseObject, error) {
	return fail[gen.GetMachineDependencyJobdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// StopMachineDependencyJob implements POST .../dependencies/jobs/{jobId}/stop.
func (s *server) StopMachineDependencyJob(_ context.Context, _ gen.StopMachineDependencyJobRequestObject) (gen.StopMachineDependencyJobResponseObject, error) {
	return fail[gen.StopMachineDependencyJobdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// UndoMachineDependencyJob implements POST .../dependencies/jobs/{jobId}/undo.
func (s *server) UndoMachineDependencyJob(_ context.Context, _ gen.UndoMachineDependencyJobRequestObject) (gen.UndoMachineDependencyJobResponseObject, error) {
	return fail[gen.UndoMachineDependencyJobdefaultJSONResponse](s, machines.ErrNotSupported), nil
}

// RestartMachineForDependencyJob implements POST .../dependencies/jobs/{jobId}/restart-host.
func (s *server) RestartMachineForDependencyJob(_ context.Context, _ gen.RestartMachineForDependencyJobRequestObject) (gen.RestartMachineForDependencyJobResponseObject, error) {
	return fail[gen.RestartMachineForDependencyJobdefaultJSONResponse](s, machines.ErrNotSupported), nil
}
