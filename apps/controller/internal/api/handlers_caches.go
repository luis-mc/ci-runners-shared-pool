package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
)

// CachesService is what the caches routes need (web-console R5.25, caches
// R5.36 to R5.39). *caches.Service implements it.
type CachesService interface {
	List(ctx context.Context, machineID, kind string) ([]caches.Cache, error)
	BeginWipe(ctx context.Context, actor audit.Actor, id string, confirm bool) (caches.Cache, error)
}

var _ CachesService = (*caches.Service)(nil)

func toGenCache(c caches.Cache) gen.Cache {
	out := gen.Cache{Id: c.ID, Kind: gen.CacheKind(c.Kind), MachineId: c.MachineID, SizeBytes: c.Size, State: gen.CacheState(c.State)}
	if c.RunnerName != "" {
		n := c.RunnerName
		out.RunnerName = &n
	}
	if c.Cap > 0 {
		v := c.Cap
		out.CapBytes = &v
	}
	n := c.Entries
	out.EntryCount = &n
	if !c.LastWipedAt.IsZero() {
		t := c.LastWipedAt
		out.LastWipedAt = &t
	}
	if len(c.WaitingFor) > 0 {
		w := append([]string{}, c.WaitingFor...)
		out.WaitingFor = &w
	}
	if c.Error != "" {
		e := c.Error
		out.Error = &e
	}
	return out
}

// cacheFail maps a service error to the error envelope.
func (s *server) cacheFail(err error) (int, gen.ErrorEnvelope) {
	var nc *caches.NeedsConfirmation
	var re *caches.RefusalError
	switch {
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Items))
		for _, it := range nc.Items {
			details = append(details, gen.ErrorDetail{Kind: it.Kind, Text: it.Text})
		}
		return http.StatusConflict, gen.ErrorEnvelope{Error: gen.ErrorBody{
			Code: CodeNeedsConfirmation, Message: "this wipe needs a yes; it names the cache and the jobs it waits for", Details: &details}}
	case errors.As(err, &re):
		return http.StatusConflict, errEnvelope(CodeConflict, re.Reason)
	case errors.Is(err, caches.ErrNotFound):
		return http.StatusNotFound, errEnvelope(CodeNotFound, "no such cache")
	}
	s.opts.Logger.Error("caches", "err", err)
	return http.StatusInternalServerError, errEnvelope(CodeInternal, "internal error")
}

// ListCaches implements GET /api/v1/caches: what each machine's agent measured.
func (s *server) ListCaches(ctx context.Context, req gen.ListCachesRequestObject) (gen.ListCachesResponseObject, error) {
	if s.opts.Caches == nil {
		return gen.ListCachesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	var machineID, kind string
	if req.Params.MachineId != nil {
		machineID = *req.Params.MachineId
	}
	if req.Params.Kind != nil {
		kind = string(*req.Params.Kind)
	}
	list, err := s.opts.Caches.List(ctx, machineID, kind)
	if err != nil {
		code, body := s.cacheFail(err)
		return gen.ListCachesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.Cache, 0, len(list))
	for _, c := range list {
		items = append(items, toGenCache(c))
	}
	return gen.ListCaches200JSONResponse{Items: items}, nil
}

// WipeCache implements POST /api/v1/caches/{cacheId}/wipe. Without confirm=true
// it answers 409 needs_confirmation naming the cache, its size and the jobs the
// drain waits for. With it the agent drains the runner and deletes (R5.30 to R5.32).
func (s *server) WipeCache(ctx context.Context, req gen.WipeCacheRequestObject) (gen.WipeCacheResponseObject, error) {
	if s.opts.Caches == nil {
		return gen.WipeCachedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	confirm := req.Params.Confirm != nil && *req.Params.Confirm
	c, err := s.opts.Caches.BeginWipe(ctx, audit.ActorFrom(ctx), req.CacheId, confirm)
	if err != nil {
		code, body := s.cacheFail(err)
		return gen.WipeCachedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.WipeCache202JSONResponse(toGenCache(c)), nil
}
