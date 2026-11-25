package api

import (
	"context"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/depsetup"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
)

// Dependency setup routes (dependency-setup spec, CRSP248). The role checks
// (admin to check, start, stop, undo and restart; any signed-in user to read)
// are the x-auth of the contract, enforced before these run.

// DepsService is what the dependency routes need (*depsetup.Service).
type DepsService interface {
	Check(ctx context.Context, machineID, runnerOS, runnerKind string) (depsetup.Check, error)
	Start(ctx context.Context, r depsetup.StartRequest) (depsetup.Job, error)
	Get(ctx context.Context, machineID, jobID string) (depsetup.Job, error)
	List(ctx context.Context, machineID string) ([]depsetup.Job, error)
	Stop(ctx context.Context, actor, machineID, jobID string) (depsetup.Job, error)
	Undo(ctx context.Context, actor, machineID, jobID string, confirmed bool) (depsetup.Job, error)
	RestartHost(ctx context.Context, actor, machineID, jobID string, confirmed bool) (depsetup.Job, error)
}

var _ DepsService = (*depsetup.Service)(nil)

func checkToAPI(c depsetup.Check) gen.DependencyCheck {
	out := gen.DependencyCheck{MachineId: c.MachineID, Ready: c.Ready, Os: strPtr(c.OS), Arch: strPtr(c.Arch), Admin: c.Admin,
		DiskFreeBytes: c.DiskFreeBytes, VirtualizationOff: c.VirtualizationOff, Items: []gen.DependencyItem{}, Choices: []gen.DependencyChoice{},
		BlockedReason: strPtr(c.BlockedReason)}
	if c.StartOnly {
		out.StartOnly = machinesPtr(true)
	}
	for _, it := range c.Items {
		out.Items = append(out.Items, gen.DependencyItem{Id: gen.DependencyItemId(it.ID), Name: it.Name, State: gen.DependencyItemState(it.State),
			Version: strPtr(it.Version), Why: it.Why, Detail: strPtr(it.Detail)})
	}
	for _, ch := range c.Choices {
		g := gen.DependencyChoice{Id: ch.ID, Title: ch.Title, ItemIds: ch.ItemIDs, Recommended: ch.Recommended,
			RequiresLicenceTick: ch.RequiresLicenceTick, ManualSteps: ch.ManualSteps, Facts: []gen.DependencyFacts{}}
		for _, f := range ch.Facts {
			g.Facts = append(g.Facts, gen.DependencyFacts{Name: f.Name, Version: f.Version, Source: f.Source, DownloadBytes: f.DownloadBytes,
				DiskBytes: f.DiskBytes, TypicalMinutes: f.TypicalMinutes, NeedsAdmin: f.NeedsAdmin, RestartLikely: f.RestartLikely,
				LicenceNote: strPtr(f.LicenceNote), Undo: f.Undo})
		}
		out.Choices = append(out.Choices, g)
	}
	return out
}

func i64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func eventToAPI(e depsetup.Event) gen.DependencyEvent {
	g := gen.DependencyEvent{JobId: e.JobID, Phase: e.Phase, PhaseIndex: e.PhaseIndex, PhaseCount: e.PhaseCount, PhaseWeight: float32(e.PhaseWeight),
		Reason: strPtr(e.Reason), Bytes: i64Ptr(e.Bytes), Total: i64Ptr(e.Total), Rate: i64Ptr(e.Rate), EtaSec: i64Ptr(e.EtaSec),
		Command: strPtr(e.Command), Stream: gen.DependencyEventStream(e.Stream), Line: strPtr(e.Line), Ts: e.Ts}
	// pct is nullable in the contract but generated as a number; unknown reads 0 with a reason.
	if e.Pct != nil {
		g.Pct = float32(*e.Pct)
	}
	g.Machine.Name, g.Machine.Role, g.Machine.Os = e.MachineName, e.MachineRole, e.MachineOS
	return g
}

func jobToDependencyAPI(j depsetup.Job) gen.DependencyJob {
	out := gen.DependencyJob{Id: j.ID, MachineId: j.MachineID, State: gen.DependencyJobState(j.State), ChoiceId: j.ChoiceID,
		ItemIds: append([]string{}, j.ItemIDs...), RunnerOs: j.RunnerOS, RunnerKind: j.RunnerKind, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
		Actor: strPtr(j.Actor), Error: strPtr(j.Error), EngineVersion: strPtr(j.EngineVersion), Added: []gen.DependencyAdded{}, Log: []gen.DependencyEvent{}}
	for _, a := range j.Added {
		out.Added = append(out.Added, gen.DependencyAdded{ItemId: a.ItemID, Kind: a.Kind, Ref: a.Ref, Version: a.Version})
	}
	for _, e := range j.Log {
		out.Log = append(out.Log, eventToAPI(e))
	}
	if j.Last != nil {
		e := eventToAPI(*j.Last)
		out.Last = &e
	}
	if len(j.ManualSteps) > 0 {
		ms := append([]string{}, j.ManualSteps...)
		out.ManualSteps = &ms
	}
	return out
}

// CheckMachineDependencies implements POST /api/v1/machines/{machineId}/dependencies/check.
func (s *server) CheckMachineDependencies(ctx context.Context, req gen.CheckMachineDependenciesRequestObject) (gen.CheckMachineDependenciesResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.CheckMachineDependenciesdefaultJSONResponse](), nil
	}
	if req.Body == nil {
		return fail[gen.CheckMachineDependenciesdefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	c, err := s.opts.Deps.Check(ctx, req.MachineId, string(req.Body.RunnerOs), string(req.Body.RunnerKind))
	if err != nil {
		return fail[gen.CheckMachineDependenciesdefaultJSONResponse](s, err), nil
	}
	return gen.CheckMachineDependencies200JSONResponse(checkToAPI(c)), nil
}

// ListMachineDependencyJobs implements GET /api/v1/machines/{machineId}/dependencies/jobs.
func (s *server) ListMachineDependencyJobs(ctx context.Context, req gen.ListMachineDependencyJobsRequestObject) (gen.ListMachineDependencyJobsResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.ListMachineDependencyJobsdefaultJSONResponse](), nil
	}
	js, err := s.opts.Deps.List(ctx, req.MachineId)
	if err != nil {
		return fail[gen.ListMachineDependencyJobsdefaultJSONResponse](s, err), nil
	}
	out := make(gen.ListMachineDependencyJobs200JSONResponse, 0, len(js))
	for _, j := range js {
		out = append(out, jobToDependencyAPI(j))
	}
	return out, nil
}

// StartMachineDependencyJob implements POST /api/v1/machines/{machineId}/dependencies/jobs.
func (s *server) StartMachineDependencyJob(ctx context.Context, req gen.StartMachineDependencyJobRequestObject) (gen.StartMachineDependencyJobResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.StartMachineDependencyJobdefaultJSONResponse](), nil
	}
	if req.Body == nil {
		return fail[gen.StartMachineDependencyJobdefaultJSONResponse](s, machines.ErrInvalid), nil
	}
	b := req.Body
	j, err := s.opts.Deps.Start(ctx, depsetup.StartRequest{MachineID: req.MachineId, Actor: s.actor(ctx), RunnerOS: string(b.RunnerOs),
		RunnerKind: string(b.RunnerKind), ChoiceID: b.ChoiceId, Confirmed: b.Confirmed, AcceptLicence: b.AcceptLicence != nil && *b.AcceptLicence})
	if err != nil {
		return fail[gen.StartMachineDependencyJobdefaultJSONResponse](s, err), nil
	}
	return gen.StartMachineDependencyJob202JSONResponse(jobToDependencyAPI(j)), nil
}

// GetMachineDependencyJob implements GET .../dependencies/jobs/{jobId}.
func (s *server) GetMachineDependencyJob(ctx context.Context, req gen.GetMachineDependencyJobRequestObject) (gen.GetMachineDependencyJobResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.GetMachineDependencyJobdefaultJSONResponse](), nil
	}
	j, err := s.opts.Deps.Get(ctx, req.MachineId, req.JobId)
	if err != nil {
		return fail[gen.GetMachineDependencyJobdefaultJSONResponse](s, err), nil
	}
	return gen.GetMachineDependencyJob200JSONResponse(jobToDependencyAPI(j)), nil
}

// StopMachineDependencyJob implements POST .../dependencies/jobs/{jobId}/stop.
func (s *server) StopMachineDependencyJob(ctx context.Context, req gen.StopMachineDependencyJobRequestObject) (gen.StopMachineDependencyJobResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.StopMachineDependencyJobdefaultJSONResponse](), nil
	}
	j, err := s.opts.Deps.Stop(ctx, s.actor(ctx), req.MachineId, req.JobId)
	if err != nil {
		return fail[gen.StopMachineDependencyJobdefaultJSONResponse](s, err), nil
	}
	return gen.StopMachineDependencyJob200JSONResponse(jobToDependencyAPI(j)), nil
}

// UndoMachineDependencyJob implements POST .../dependencies/jobs/{jobId}/undo.
func (s *server) UndoMachineDependencyJob(ctx context.Context, req gen.UndoMachineDependencyJobRequestObject) (gen.UndoMachineDependencyJobResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.UndoMachineDependencyJobdefaultJSONResponse](), nil
	}
	j, err := s.opts.Deps.Undo(ctx, s.actor(ctx), req.MachineId, req.JobId, req.Params.Confirmed != nil && *req.Params.Confirmed)
	if err != nil {
		return fail[gen.UndoMachineDependencyJobdefaultJSONResponse](s, err), nil
	}
	return gen.UndoMachineDependencyJob202JSONResponse(jobToDependencyAPI(j)), nil
}

// RestartMachineForDependencyJob implements POST .../dependencies/jobs/{jobId}/restart-host.
func (s *server) RestartMachineForDependencyJob(ctx context.Context, req gen.RestartMachineForDependencyJobRequestObject) (gen.RestartMachineForDependencyJobResponseObject, error) {
	if s.opts.Deps == nil {
		return machinesUnavailable[gen.RestartMachineForDependencyJobdefaultJSONResponse](), nil
	}
	j, err := s.opts.Deps.RestartHost(ctx, s.actor(ctx), req.MachineId, req.JobId, req.Params.Confirmed != nil && *req.Params.Confirmed)
	if err != nil {
		return fail[gen.RestartMachineForDependencyJobdefaultJSONResponse](s, err), nil
	}
	return gen.RestartMachineForDependencyJob202JSONResponse(jobToDependencyAPI(j)), nil
}
