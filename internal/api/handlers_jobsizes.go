package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
)

// JobSizeService is what the job size routes need (scheduling R5.79 to R5.87).
// *jobsize.Service implements it.
type JobSizeService interface {
	Views(ctx context.Context, repository string) ([]jobsize.View, string, error)
	SetOverride(ctx context.Context, repository, pattern string, class jobsize.Class) error
	CostTable(ctx context.Context) jobsize.CostTable
}

func jobSizeFail(msg string) (int, gen.ErrorEnvelope) {
	return http.StatusBadRequest, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeBadRequest, Message: msg}}
}

func genJobSizeRow(v jobsize.View) gen.JobSizeRow {
	row := gen.JobSizeRow{
		Repository: v.Repository, Job: v.Job, Class: gen.JobSizeClass(v.Size.Class), Source: gen.JobSizeSource(v.Size.Source),
		Weight:  gen.JobSizeUnits{Cpu: v.Size.Weight.CPU, Memory: v.Size.Weight.Memory},
		Reasons: []string{}, Services: []string{}, OverrideConsole: v.Console, OverrideFile: v.File,
		CopyLine: jobsize.CopyLine(v.Job, v.Size.Class),
	}
	if sc := v.Scan; sc != nil {
		conf := gen.JobSizeRowConfidence(sc.Confidence)
		tests, est, at := sc.Tests, sc.EstSec, sc.ScannedAt
		row.Confidence, row.Tests, row.EstSeconds, row.ScannedAt = &conf, &tests, &est, &at
		row.Workflow = &sc.Workflow
		row.Reasons = append(row.Reasons, sc.Reasons...)
		row.Services = append(row.Services, sc.Services...)
		row.ResetNote = sc.ResetNote
	}
	return row
}

// ListJobSizes implements GET /api/v1/job-sizes.
func (s *server) ListJobSizes(ctx context.Context, req gen.ListJobSizesRequestObject) (gen.ListJobSizesResponseObject, error) {
	if s.opts.JobSizes == nil {
		return gen.ListJobSizesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	repo := strings.TrimSpace(deref(req.Params.Repository))
	if !strings.Contains(repo, "/") {
		code, body := jobSizeFail("The repository must be owner/name.")
		return gen.ListJobSizesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	views, ferr, err := s.opts.JobSizes.Views(ctx, repo)
	if err != nil {
		return gen.ListJobSizesdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "internal error"}}}, nil
	}
	out := gen.JobSizeList{Repository: repo, Items: []gen.JobSizeRow{}, CostTable: []gen.JobSizeCostRow{}, FileError: ferr}
	for _, v := range views {
		out.Items = append(out.Items, genJobSizeRow(v))
	}
	for _, r := range s.opts.JobSizes.CostTable(ctx).Rows() {
		row := gen.JobSizeCostRow{Kind: gen.JobSizeCostRowKind(r.Kind), Key: r.Key, Class: gen.JobSizeClass(r.Class), Cpu: float32(r.CPU),
			Gib: float32(r.GiB), Assumption: r.Assumption}
		if r.GiBMin > 0 {
			v := float32(r.GiBMin)
			row.GibMin = &v
		}
		if r.SecPerTest > 0 {
			v := float32(r.SecPerTest)
			row.SecPerTest = &v
		}
		if r.Note != "" {
			n := r.Note
			row.Note = &n
		}
		out.CostTable = append(out.CostTable, row)
	}
	return gen.ListJobSizes200JSONResponse(out), nil
}

// PutJobSizeOverride implements PUT /api/v1/job-sizes.
func (s *server) PutJobSizeOverride(ctx context.Context, req gen.PutJobSizeOverrideRequestObject) (gen.PutJobSizeOverrideResponseObject, error) {
	if s.opts.JobSizes == nil {
		return gen.PutJobSizeOverridedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil || !strings.Contains(req.Body.Repository, "/") {
		code, body := jobSizeFail("The repository must be owner/name.")
		return gen.PutJobSizeOverridedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	b := req.Body
	if err := s.opts.JobSizes.SetOverride(ctx, b.Repository, b.Pattern, jobsize.Class(b.Class)); err != nil {
		code, body := jobSizeFail(err.Error())
		return gen.PutJobSizeOverridedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	pattern := strings.TrimSpace(b.Pattern)
	return gen.PutJobSizeOverride200JSONResponse(gen.JobSizeOverride{Repository: b.Repository, Pattern: pattern, Class: b.Class,
		CopyLine: jobsize.CopyLine(pattern, jobsize.Class(b.Class))}), nil
}

// DeleteJobSizeOverride implements DELETE /api/v1/job-sizes.
func (s *server) DeleteJobSizeOverride(ctx context.Context, req gen.DeleteJobSizeOverrideRequestObject) (gen.DeleteJobSizeOverrideResponseObject, error) {
	if s.opts.JobSizes == nil {
		return gen.DeleteJobSizeOverridedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	repo := strings.TrimSpace(deref(req.Params.Repository))
	if !strings.Contains(repo, "/") {
		code, body := jobSizeFail("The repository must be owner/name.")
		return gen.DeleteJobSizeOverridedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if err := s.opts.JobSizes.SetOverride(ctx, repo, deref(req.Params.Pattern), ""); err != nil {
		code, body := jobSizeFail(err.Error())
		return gen.DeleteJobSizeOverridedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.DeleteJobSizeOverride204Response{}, nil
}
