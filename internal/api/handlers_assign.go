package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/assign"
)

// AssignLeases is where the API reads the leases of the assign controller
// (runners R5.110 to R5.116).
type AssignLeases interface {
	List(ctx context.Context, states ...assign.State) ([]assign.Lease, error)
}

// ListAssignLeases implements GET /api/v1/assign/leases.
func (s *server) ListAssignLeases(ctx context.Context, req gen.ListAssignLeasesRequestObject) (gen.ListAssignLeasesResponseObject, error) {
	if s.opts.Assign == nil {
		return gen.ListAssignLeasesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notWired()}, nil
	}
	var states []assign.State
	if req.Params.State != nil {
		states = append(states, assign.State(*req.Params.State))
	}
	ls, err := s.opts.Assign.List(ctx, states...)
	if err != nil {
		s.opts.Logger.Error("list assign leases", "err", err)
		return gen.ListAssignLeasesdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errBody(CodeInternal, "internal error")}, nil
	}
	out := gen.AssignLeaseList{Leases: []gen.AssignLease{}}
	for _, l := range ls {
		a := gen.AssignLease{JobId: l.JobID, RunId: &l.RunID, Repository: l.Repository, Job: ptr(l.JobName), Labels: &l.Labels,
			JobLabel: ptr(l.JobLabel), State: gen.AssignLeaseState(l.State), MachineId: ptr(l.MachineID), RunnerName: ptr(l.RunnerName),
			Cpus: ptr(l.Req.CPUs), MemoryGib: ptr(l.Req.MemoryGiB), Class: ptr(l.Class), QueuedAt: l.QueuedAt,
			Reason: ptr(l.Reason), Passes: ptr(l.Passes), ReservedSince: tptr(l.ReservedSince)}
		if l.ReasonCode != "" {
			c := gen.AssignReasonCode(l.ReasonCode)
			a.ReasonCode = &c
		}
		out.Leases = append(out.Leases, a)
	}
	return gen.ListAssignLeases200JSONResponse(out), nil
}
