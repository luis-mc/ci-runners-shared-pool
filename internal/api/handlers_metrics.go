package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/metrics"
)

// MetricsService is what the metrics export routes need (observability R5.40 to
// R5.47). *metrics.Service implements it.
type MetricsService interface {
	State(ctx context.Context) (metrics.State, error)
	Update(ctx context.Context, actor audit.Actor, c metrics.Config) (metrics.State, error)
	MakeToken(ctx context.Context) (string, error)
}

var _ MetricsService = (*metrics.Service)(nil)

func toGenMetrics(st metrics.State) gen.MetricsSettings {
	out := gen.MetricsSettings{
		Enabled: st.Config.Enabled, Port: st.Config.Port, Expose: st.Config.Expose, NeedsKey: st.Config.RequireToken,
		PushEnabled: st.Config.PushEnabled, PushEndpoint: st.Config.PushEndpoint, PushSeconds: st.Config.PushSeconds,
		KeyMade: st.TokenSet, Running: st.Running, Pushing: st.Pushing, ScrapeConfig: st.ScrapeConfig,
	}
	if st.Addr != "" {
		a := st.Addr
		out.Address = &a
	}
	if !st.LastPush.IsZero() {
		t := st.LastPush
		out.LastPushAt = &t
	}
	if st.LastPushErr != "" {
		e := st.LastPushErr
		out.LastPushError = &e
	}
	return out
}

func (s *server) metricsFail(err error) opsFailure {
	var inv *metrics.InvalidError
	switch {
	case errors.As(err, &inv):
		details := make([]gen.ErrorDetail, 0, len(inv.Problems))
		for _, p := range inv.Problems {
			details = append(details, gen.ErrorDetail{Kind: "field", Text: p})
		}
		return envelope(http.StatusBadRequest, CodeBadRequest, inv.Error(), details...)
	case errors.Is(err, metrics.ErrNoToken):
		return envelope(http.StatusConflict, CodeConflict, err.Error())
	}
	return s.opsError(err)
}

// GetMetricsSettings implements GET /api/v1/settings/metrics.
func (s *server) GetMetricsSettings(ctx context.Context, _ gen.GetMetricsSettingsRequestObject) (gen.GetMetricsSettingsResponseObject, error) {
	if s.opts.Metrics == nil {
		f := unavailable()
		return gen.GetMetricsSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	st, err := s.opts.Metrics.State(ctx)
	if err != nil {
		f := s.metricsFail(err)
		return gen.GetMetricsSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.GetMetricsSettings200JSONResponse(toGenMetrics(st)), nil
}

// PutMetricsSettings implements PUT /api/v1/settings/metrics.
func (s *server) PutMetricsSettings(ctx context.Context, req gen.PutMetricsSettingsRequestObject) (gen.PutMetricsSettingsResponseObject, error) {
	fail := func(f opsFailure) (gen.PutMetricsSettingsResponseObject, error) {
		return gen.PutMetricsSettingsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Metrics == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	b := req.Body
	st, err := s.opts.Metrics.Update(ctx, publicActor(ctx, s.principal(ctx)), metrics.Config{
		Enabled: b.Enabled, Port: b.Port, Expose: b.Expose, RequireToken: b.NeedsKey,
		PushEnabled: b.PushEnabled, PushEndpoint: b.PushEndpoint, PushSeconds: b.PushSeconds,
	})
	if err != nil {
		// A page that could not start is kept as chosen; say so, with the state.
		return fail(s.metricsFail(err))
	}
	return gen.PutMetricsSettings200JSONResponse(toGenMetrics(st)), nil
}

// MakeMetricsToken implements POST /api/v1/settings/metrics/token.
func (s *server) MakeMetricsToken(ctx context.Context, _ gen.MakeMetricsTokenRequestObject) (gen.MakeMetricsTokenResponseObject, error) {
	fail := func(f opsFailure) (gen.MakeMetricsTokenResponseObject, error) {
		return gen.MakeMetricsTokendefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Metrics == nil {
		return fail(unavailable())
	}
	tok, err := s.opts.Metrics.MakeToken(ctx)
	if err != nil {
		return fail(s.metricsFail(err))
	}
	if s.opts.Audit != nil {
		_ = s.opts.Audit.Record(ctx, publicActor(ctx, s.principal(ctx)), metrics.ActionTokenMade, audit.Target{Type: "settings", ID: "metrics_export"}, nil)
	}
	return gen.MakeMetricsToken200JSONResponse{Key: tok}, nil
}
