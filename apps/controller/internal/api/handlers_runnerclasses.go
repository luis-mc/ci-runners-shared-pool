package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// RunnerClassService is what the class settings routes need (scheduling R5.89
// to R5.93). *runners.ClassService implements it.
type RunnerClassService interface {
	Get(ctx context.Context) runners.ClassView
	Save(ctx context.Context, in runners.ClassInput, by string) (runners.ClassView, error)
}

func genRunnerClasses(v runners.ClassView) gen.RunnerClasses {
	out := gen.RunnerClasses{Preset: v.Preset, Classes: []gen.RunnerClassSize{}, Hints: append([]string{}, v.Hints...),
		Unheld: []string{}, Presets: []gen.RunnerClassesPresets{},
		ReserveAfterPasses: v.ReservePasses, ReserveAfterMinutes: v.ReserveMinutes}
	for _, p := range v.Presets {
		out.Presets = append(out.Presets, gen.RunnerClassesPresets(p))
	}
	for _, s := range v.Sizes.List() {
		out.Classes = append(out.Classes, gen.RunnerClassSize{Class: gen.RunnerClassSizeClass(s.Class), Cpus: s.CPUs, MemoryGib: s.MemoryGiB})
	}
	for _, c := range v.Unheld {
		out.Unheld = append(out.Unheld, string(c))
	}
	return out
}

// GetRunnerClasses implements GET /api/v1/runner-classes.
func (s *server) GetRunnerClasses(ctx context.Context, _ gen.GetRunnerClassesRequestObject) (gen.GetRunnerClassesResponseObject, error) {
	if s.opts.RunnerClasses == nil {
		return gen.GetRunnerClassesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	return gen.GetRunnerClasses200JSONResponse(genRunnerClasses(s.opts.RunnerClasses.Get(ctx))), nil
}

// PutRunnerClasses implements PUT /api/v1/runner-classes.
func (s *server) PutRunnerClasses(ctx context.Context, req gen.PutRunnerClassesRequestObject) (gen.PutRunnerClassesResponseObject, error) {
	if s.opts.RunnerClasses == nil {
		return gen.PutRunnerClassesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	reply := func(code int, c, msg string) (gen.PutRunnerClassesResponseObject, error) {
		return gen.PutRunnerClassesdefaultJSONResponse{StatusCode: code, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: c, Message: msg}}}, nil
	}
	if req.Body == nil {
		return reply(http.StatusBadRequest, CodeBadRequest, runners.MsgWholeNumbers)
	}
	b := req.Body
	in := runners.ClassInput{ReservePasses: b.ReserveAfterPasses, ReserveMinutes: b.ReserveAfterMinutes, ConfirmLowering: b.ConfirmLowering != nil && *b.ConfirmLowering}
	if b.Preset != nil {
		in.Preset = string(*b.Preset)
	}
	if b.Classes != nil {
		sz := runners.TableSizes()
		set := map[runners.SizeClass]bool{}
		for _, c := range *b.Classes {
			spec := runners.SizeSpec{Class: runners.SizeClass(c.Class), CPUs: c.Cpus, MemoryGiB: c.MemoryGib}
			switch spec.Class {
			case runners.SizeSmall:
				sz.Small = spec
			case runners.SizeMedium:
				sz.Medium = spec
			case runners.SizeLarge:
				sz.Large = spec
			case runners.SizeXL:
				sz.XL = spec
			}
			set[spec.Class] = true
		}
		if len(set) != 4 {
			return reply(http.StatusBadRequest, CodeBadRequest, "Give all four classes: small, medium, large and xl.")
		}
		in.Sizes = &sz
	}
	v, err := s.opts.RunnerClasses.Save(ctx, in, "")
	var ct *runners.ErrClassTable
	switch {
	case err == nil:
		return gen.PutRunnerClasses200JSONResponse(genRunnerClasses(v)), nil
	case errors.As(err, &ct):
		return reply(http.StatusBadRequest, CodeBadRequest, ct.Msg)
	case errors.Is(err, runners.ErrConfirmLowering):
		return reply(http.StatusConflict, CodeNeedsConfirmation, "A size that runners use gets smaller. Confirm to save it.")
	}
	return reply(http.StatusInternalServerError, CodeInternal, "internal error")
}
