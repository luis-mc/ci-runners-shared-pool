package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/mirror"
)

// CacheHubService is what the hub routes need (caches R5.73, R5.77).
// *mirror.Service implements it.
type CacheHubService interface {
	Hub(ctx context.Context) (mirror.Hub, error)
	Get(ctx context.Context) (mirror.Settings, error)
	Put(ctx context.Context, actor audit.Actor, rec audit.Recorder, v mirror.Settings) (mirror.Settings, error)
}

var _ CacheHubService = (*mirror.Service)(nil)

func toGenHubSettings(v mirror.Settings) gen.CacheHubSettings {
	r, w := v.GitRefreshMin, v.WarmthHours
	return gen.CacheHubSettings{ActionsCache: v.ActionsCache, GitMirror: v.GitMirror, RegistryMirror: v.RegistryMirror,
		PackageProxy: v.PackageProxy, BudgetGib: v.BudgetGiB, GitRefreshMinutes: &r, WarmthHours: &w}
}

func toGenHub(h mirror.Hub) gen.CacheHub {
	out := gen.CacheHub{Settings: toGenHubSettings(h.Settings), BudgetBytes: h.BudgetBytes, UsedBytes: h.UsedBytes,
		Hits: h.Hits, Misses: h.Misses, ServedBytes: h.Served, WarmMachines: h.WarmMachines,
		Cards: make([]gen.CacheHubCard, 0, len(h.Cards)), Repos: make([]gen.CacheHubRepo, 0, len(h.Repos))}
	for _, c := range h.Cards {
		g := gen.CacheHubCard{Kind: gen.CacheHubCardKind(c.Kind), On: c.On, SizeBytes: c.Size, EntryCount: c.Entries,
			Hits: c.Hits, Misses: c.Misses, ServedBytes: c.Served, Status: gen.CacheHubCardStatus(c.Status), WipeId: c.WipeID}
		if !c.LastUsed.IsZero() {
			t := c.LastUsed
			g.LastUsedAt = &t
		}
		if c.Note != "" {
			n := c.Note
			g.Note = &n
		}
		out.Cards = append(out.Cards, g)
	}
	for _, r := range h.Repos {
		out.Repos = append(out.Repos, gen.CacheHubRepo{Repo: r.Repo, Hits: r.Hits, Misses: r.Misses, SizeBytes: r.Size, Warmth: r.Warmth})
	}
	return out
}

// GetCacheHub implements GET /api/v1/caches/hub (caches R5.77).
func (s *server) GetCacheHub(ctx context.Context, _ gen.GetCacheHubRequestObject) (gen.GetCacheHubResponseObject, error) {
	if s.opts.CacheHub == nil {
		return gen.GetCacheHubdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	h, err := s.opts.CacheHub.Hub(ctx)
	if err != nil {
		s.opts.Logger.Error("cache hub", "err", err)
		return gen.GetCacheHubdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetCacheHub200JSONResponse(toGenHub(h)), nil
}

// PutCacheSettings implements PUT /api/v1/caches/settings (caches R5.66,
// R5.73): validated, stored, audited as cache.settings; a smaller budget
// applies at once.
func (s *server) PutCacheSettings(ctx context.Context, req gen.PutCacheSettingsRequestObject) (gen.PutCacheSettingsResponseObject, error) {
	if s.opts.CacheHub == nil {
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "a settings body is needed")}, nil
	}
	b := *req.Body
	if b.BudgetGib < 1 {
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest,
			Body: errEnvelope(CodeBadRequest, "the cache budget must be 1 to 4096 GiB")}, nil
	}
	// Numbers left out keep what is stored.
	cur, err := s.opts.CacheHub.Get(ctx)
	if err != nil {
		s.opts.Logger.Error("cache settings", "err", err)
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	v := mirror.Settings{ActionsCache: b.ActionsCache, GitMirror: b.GitMirror, RegistryMirror: b.RegistryMirror,
		PackageProxy: b.PackageProxy, BudgetGiB: b.BudgetGib, GitRefreshMin: cur.GitRefreshMin, WarmthHours: cur.WarmthHours}
	if b.GitRefreshMinutes != nil {
		v.GitRefreshMin = *b.GitRefreshMinutes
	}
	if b.WarmthHours != nil {
		v.WarmthHours = *b.WarmthHours
	}
	var rec audit.Recorder
	if s.opts.Audit != nil {
		rec = s.opts.Audit
	}
	if err := v.Validate(); err != nil {
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, err.Error())}, nil
	}
	out, err := s.opts.CacheHub.Put(ctx, audit.ActorFrom(ctx), rec, v)
	if err != nil {
		s.opts.Logger.Error("cache settings", "err", err)
		return gen.PutCacheSettingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.PutCacheSettings200JSONResponse(toGenHubSettings(out)), nil
}
