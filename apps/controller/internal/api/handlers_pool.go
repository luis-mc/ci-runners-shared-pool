package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// Handlers of the Budgets settings page (CRSP37): the pool defaults for new
// machines (machines R5.22) and the queue settings (scheduling R5.8 to
// R5.10, observability R5.5). Admin-only writes belong to the auth layer
// (x-auth: admin); the actor it puts in the context is the one audited.

func toGenPool(p settings.PoolSettings) gen.PoolSettings {
	return gen.PoolSettings{
		BudgetMemoryPct: p.BudgetMemoryPct, BudgetCpuPct: p.BudgetCPUPct,
		SizeSlotsFromMachine: p.SizeSlotsFromMachine, DefaultJobSlots: p.DefaultJobSlots,
		DrainTimeoutMinutes: p.DrainTimeoutMinutes,
	}
}

func toGenQueue(q settings.QueueSettings) gen.QueueSettings {
	order := make([]gen.QueueSettingsOrder, 0, len(q.Order))
	for _, k := range q.Order {
		order = append(order, gen.QueueSettingsOrder(k))
	}
	return gen.QueueSettings{Order: order, FairShare: q.FairShare, NoRunnerWaitMinutes: q.NoRunnerWaitMinutes}
}

// GetPoolSettings implements GET /api/v1/settings/pool.
func (s *server) GetPoolSettings(ctx context.Context, _ gen.GetPoolSettingsRequestObject) (gen.GetPoolSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.GetPoolSettingsResponseObject, error) {
		return gen.GetPoolSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	p, err := s.opts.Settings.GetPool(ctx)
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.GetPoolSettings200JSONResponse(toGenPool(p)), nil
}

// UpdatePoolSettings implements PUT /api/v1/settings/pool.
func (s *server) UpdatePoolSettings(ctx context.Context, req gen.UpdatePoolSettingsRequestObject) (gen.UpdatePoolSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.UpdatePoolSettingsResponseObject, error) {
		return gen.UpdatePoolSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	p, err := s.opts.Settings.UpdatePool(ctx, audit.ActorFrom(ctx), settings.PoolSettings{
		BudgetMemoryPct: b.BudgetMemoryPct, BudgetCPUPct: b.BudgetCpuPct,
		SizeSlotsFromMachine: b.SizeSlotsFromMachine, DefaultJobSlots: b.DefaultJobSlots,
		DrainTimeoutMinutes: b.DrainTimeoutMinutes,
	})
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.UpdatePoolSettings200JSONResponse(toGenPool(p)), nil
}

// GetQueueSettings implements GET /api/v1/settings/queue.
func (s *server) GetQueueSettings(ctx context.Context, _ gen.GetQueueSettingsRequestObject) (gen.GetQueueSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.GetQueueSettingsResponseObject, error) {
		return gen.GetQueueSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	q, err := s.opts.Settings.GetQueue(ctx)
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.GetQueueSettings200JSONResponse(toGenQueue(q)), nil
}

// UpdateQueueSettings implements PUT /api/v1/settings/queue.
func (s *server) UpdateQueueSettings(ctx context.Context, req gen.UpdateQueueSettingsRequestObject) (gen.UpdateQueueSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.UpdateQueueSettingsResponseObject, error) {
		return gen.UpdateQueueSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Settings == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	order := make([]string, 0, len(req.Body.Order))
	for _, k := range req.Body.Order {
		order = append(order, string(k))
	}
	q, err := s.opts.Settings.UpdateQueue(ctx, audit.ActorFrom(ctx), settings.QueueSettings{
		Order: order, FairShare: req.Body.FairShare, NoRunnerWaitMinutes: req.Body.NoRunnerWaitMinutes,
	})
	if err != nil {
		return fail(s.opsError(err))
	}
	return gen.UpdateQueueSettings200JSONResponse(toGenQueue(q)), nil
}
