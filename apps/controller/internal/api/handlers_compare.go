package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/compare"
)

// CompareService is what the speed comparison route needs.
// compare.Service implements it.
type CompareService interface {
	Compare(ctx context.Context, owner, repo string, days int) (compare.Report, error)
}

// GetSpeedComparison implements GET /api/v1/compare (observability R5.34).
// It only reads; the auth layer has checked the user role.
func (s *server) GetSpeedComparison(ctx context.Context, req gen.GetSpeedComparisonRequestObject) (gen.GetSpeedComparisonResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetSpeedComparisonResponseObject, error) {
		return gen.GetSpeedComparisondefaultJSONResponse{StatusCode: status, Body: errEnvelope(code, msg)}, nil
	}
	if s.opts.Compare == nil {
		return gen.GetSpeedComparisondefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	name := ""
	if req.Params.Repository != nil {
		name = *req.Params.Repository
	}
	owner, repo, ok := strings.Cut(name, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return fail(http.StatusBadRequest, CodeBadRequest, "name the repository as owner/repo")
	}
	days := 0
	if req.Params.Days != nil {
		days = *req.Params.Days
	}
	rep, err := s.opts.Compare.Compare(ctx, owner, repo, days)
	if errors.Is(err, compare.ErrNotFound) {
		return fail(http.StatusNotFound, CodeNotFound, "crsp cannot see that repository")
	}
	if err != nil {
		s.opts.Logger.Error("speed comparison", "err", err)
		return fail(http.StatusBadGateway, CodeUnavailable, "GitHub did not answer; try again in a minute")
	}
	// The report's JSON names are the contract's; one round trip keeps them one list.
	raw, err := json.Marshal(rep)
	if err != nil {
		return nil, err
	}
	var out gen.SpeedComparison
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return gen.GetSpeedComparison200JSONResponse(out), nil
}
