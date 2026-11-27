package api

import (
	"context"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/history"
)

// SavingsService is what the savings routes need (observability R5.15 to R5.19). history.Service implements Report.
type SavingsService interface {
	Report(ctx context.Context, name string, from, to time.Time) (history.Report, error)
}

// SavingsRates reads and changes the rate table (R5.16, R5.17). history.Rates implements it.
type SavingsRates interface {
	Get(ctx context.Context) (history.Table, error)
	Set(ctx context.Context, actor audit.Actor, t history.Table) (history.Table, error)
	Reset(ctx context.Context, actor audit.Actor) (history.Table, error)
}

func toGenRow(r history.Row) gen.SavingsRow {
	return gen.SavingsRow{Key: r.Key, Jobs: r.Jobs, Minutes: r.Minutes, Money: r.Money, PublicMinutes: r.PublicMinutes}
}

func toGenRows(rs []history.Row) []gen.SavingsRow {
	out := make([]gen.SavingsRow, 0, len(rs))
	for _, r := range rs {
		out = append(out, toGenRow(r))
	}
	return out
}

func toGenRates(t history.Table) gen.SavingsRates {
	out := gen.SavingsRates{SourceUrl: t.SourceURL, CopiedOn: t.CopiedOn, Changed: t.Changed,
		Rates: make([]gen.SavingsRate, 0, len(t.Rates)), MachineSizes: map[string]string{}}
	for _, r := range t.Rates {
		out.Rates = append(out.Rates, gen.SavingsRate{Os: gen.SavingsRateOs(r.OS), Size: r.Size, PerMinute: r.PerMinute})
	}
	for k, v := range t.MachineSizes {
		out.MachineSizes[k] = v
	}
	return out
}

// GetSavingsReport implements GET /api/v1/savings.
func (s *server) GetSavingsReport(ctx context.Context, req gen.GetSavingsReportRequestObject) (gen.GetSavingsReportResponseObject, error) {
	fail := func(status int, code, msg string) (gen.GetSavingsReportResponseObject, error) {
		return gen.GetSavingsReportdefaultJSONResponse{StatusCode: status, Body: errEnvelope(code, msg)}, nil
	}
	if s.opts.Savings == nil {
		return gen.GetSavingsReportdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	name := ""
	if req.Params.Range != nil {
		name = string(*req.Params.Range)
	}
	var from, to time.Time
	if req.Params.From != nil {
		from = req.Params.From.Time
	}
	if req.Params.To != nil {
		to = req.Params.To.Time
	}
	rep, err := s.opts.Savings.Report(ctx, name, from, to)
	if err != nil {
		return fail(http.StatusBadRequest, CodeBadRequest, err.Error())
	}
	return gen.GetSavingsReport200JSONResponse(gen.SavingsReport{
		Range: gen.SavingsReportRange(rep.Range), From: rep.From, To: rep.To, Total: toGenRow(rep.Total),
		ByRepository: toGenRows(rep.ByRepository), ByOs: toGenRows(rep.ByOS), ByMachine: toGenRows(rep.ByMachine),
		NotPriced: rep.NotPriced, Note: rep.Note, SourceUrl: rep.SourceURL, CopiedOn: rep.CopiedOn, RatesChanged: rep.Changed,
	}), nil
}

// GetSavingsRates implements GET /api/v1/savings/rates.
func (s *server) GetSavingsRates(ctx context.Context, _ gen.GetSavingsRatesRequestObject) (gen.GetSavingsRatesResponseObject, error) {
	if s.opts.SavingsRates == nil {
		return gen.GetSavingsRatesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	t, err := s.opts.SavingsRates.Get(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetSavingsRates200JSONResponse(toGenRates(t)), nil
}

// UpdateSavingsRates implements PUT /api/v1/savings/rates.
func (s *server) UpdateSavingsRates(ctx context.Context, req gen.UpdateSavingsRatesRequestObject) (gen.UpdateSavingsRatesResponseObject, error) {
	if s.opts.SavingsRates == nil {
		return gen.UpdateSavingsRatesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if req.Body == nil {
		return gen.UpdateSavingsRatesdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "the request is not valid")}, nil
	}
	in := history.Table{MachineSizes: req.Body.MachineSizes}
	for _, r := range req.Body.Rates {
		in.Rates = append(in.Rates, history.Rate{OS: string(r.Os), Size: r.Size, PerMinute: r.PerMinute})
	}
	// Validate first so a bad table is a 400 with the reason, not a server error.
	if _, err := in.Validate(); err != nil {
		return gen.UpdateSavingsRatesdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, err.Error())}, nil
	}
	t, err := s.opts.SavingsRates.Set(ctx, audit.ActorFrom(ctx), in)
	if err != nil {
		return nil, err
	}
	return gen.UpdateSavingsRates200JSONResponse(toGenRates(t)), nil
}

// ResetSavingsRates implements DELETE /api/v1/savings/rates.
func (s *server) ResetSavingsRates(ctx context.Context, _ gen.ResetSavingsRatesRequestObject) (gen.ResetSavingsRatesResponseObject, error) {
	if s.opts.SavingsRates == nil {
		return gen.ResetSavingsRatesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	t, err := s.opts.SavingsRates.Reset(ctx, audit.ActorFrom(ctx))
	if err != nil {
		return nil, err
	}
	return gen.ResetSavingsRates200JSONResponse(toGenRates(t)), nil
}
