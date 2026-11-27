package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/configio"
)

// ConfigAsCode is config as code (operations R5.28 to R5.30): one file with
// the setup, a plan that changes nothing, and an apply of exactly that plan.
// Nil answers 501 like an unbuilt operation.
type ConfigAsCode interface {
	Export(ctx context.Context) ([]byte, error)
	Plan(ctx context.Context, file []byte) (configio.Plan, error)
	Apply(ctx context.Context, actor audit.Actor, planID string, confirm []string) (configio.Result, error)
}

type configFileAnswer struct{ exportFile }

func (a configFileAnswer) VisitExportConfigFileResponse(w http.ResponseWriter) error {
	return a.write(w)
}

func genConfigChanges(cs []configio.Change) []gen.ConfigChange {
	out := make([]gen.ConfigChange, 0, len(cs))
	for _, c := range cs {
		g := gen.ConfigChange{Section: c.Section, Name: c.Name, Kind: gen.ConfigChangeKind(c.Kind)}
		if len(c.Fields) > 0 {
			fs := make([]gen.ConfigFieldChange, 0, len(c.Fields))
			for _, f := range c.Fields {
				fs = append(fs, gen.ConfigFieldChange{Field: f.Field, Old: f.Old, New: f.New})
			}
			g.Fields = &fs
		}
		out = append(out, g)
	}
	return out
}

// configError maps an error of the config engine to the API envelope.
func (s *server) configError(err error) opsFailure {
	var nc *configio.NeedsConfirmation
	switch {
	case errors.Is(err, configio.ErrBadFile):
		return envelope(http.StatusBadRequest, CodeBadRequest, err.Error())
	case errors.Is(err, configio.ErrStale):
		return envelope(http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, configio.ErrUnknownPlan):
		return envelope(http.StatusNotFound, CodeNotFound, "no such plan; plans last until the controller restarts, so make a new one")
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Removals))
		for _, r := range nc.Removals {
			details = append(details, gen.ErrorDetail{Kind: "removes", Text: r})
		}
		return envelope(http.StatusConflict, CodeNeedsConfirmation, nc.Error(), details...)
	}
	return s.opsError(err)
}

// ExportConfigFile implements GET /api/v1/config/export (admin).
func (s *server) ExportConfigFile(ctx context.Context, _ gen.ExportConfigFileRequestObject) (gen.ExportConfigFileResponseObject, error) {
	if s.opts.ConfigAsCode == nil {
		f := unavailable()
		return gen.ExportConfigFiledefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	body, err := s.opts.ConfigAsCode.Export(ctx)
	if err != nil {
		f := s.configError(err)
		return gen.ExportConfigFiledefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Audit != nil {
		_ = s.opts.Audit.Record(ctx, audit.ActorFrom(ctx), "config.export", audit.Target{Type: "config"},
			map[string]any{"summary": "exported the setup file without secrets"})
	}
	return configFileAnswer{exportFile{name: "crsp-setup-" + s.now().Format(fileStamp) + ".yaml", contentType: "application/yaml", body: body}}, nil
}

// PlanConfig implements POST /api/v1/config/plans (admin).
func (s *server) PlanConfig(ctx context.Context, req gen.PlanConfigRequestObject) (gen.PlanConfigResponseObject, error) {
	fail := func(f opsFailure) (gen.PlanConfigResponseObject, error) {
		return gen.PlanConfigdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.ConfigAsCode == nil {
		return fail(unavailable())
	}
	if req.Body == nil {
		return fail(envelope(http.StatusBadRequest, CodeBadRequest, "the request is not valid"))
	}
	p, err := s.opts.ConfigAsCode.Plan(ctx, []byte(req.Body.File))
	if err != nil {
		return fail(s.configError(err))
	}
	out := gen.PlanConfig201JSONResponse{Changes: genConfigChanges(p.Changes), CreatedAt: p.CreatedAt}
	if p.ID != "" {
		out.Id = &p.ID
	}
	if len(p.Problems) > 0 {
		out.Problems = &p.Problems
	}
	if len(p.Removals) > 0 {
		out.Removals = &p.Removals
	}
	return out, nil
}

// ApplyConfigPlan implements POST /api/v1/config/plans/{configPlanId}/apply (owner).
func (s *server) ApplyConfigPlan(ctx context.Context, req gen.ApplyConfigPlanRequestObject) (gen.ApplyConfigPlanResponseObject, error) {
	fail := func(f opsFailure) (gen.ApplyConfigPlanResponseObject, error) {
		return gen.ApplyConfigPlandefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.ConfigAsCode == nil {
		return fail(unavailable())
	}
	var confirm []string
	if req.Body != nil && req.Body.ConfirmRemovals != nil {
		confirm = *req.Body.ConfirmRemovals
	}
	res, err := s.opts.ConfigAsCode.Apply(ctx, audit.ActorFrom(ctx), req.ConfigPlanId, confirm)
	if err != nil {
		return fail(s.configError(err))
	}
	return gen.ApplyConfigPlan200JSONResponse{PlanId: res.PlanID, Applied: genConfigChanges(res.Applied)}, nil
}
