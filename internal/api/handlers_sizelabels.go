package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/sizelabel"
)

// SizeLabelMachines lists the room of each machine for the labels screen
// (scheduling R5.67a). Nil in Options answers 501 like every unbuilt area.
type SizeLabelMachines interface {
	SizeLabelMachines(ctx context.Context) ([]sizelabel.Machine, error)
}

// ListSizeLabels implements GET /api/v1/size-labels.
func (s *server) ListSizeLabels(ctx context.Context, _ gen.ListSizeLabelsRequestObject) (gen.ListSizeLabelsResponseObject, error) {
	if s.opts.SizeLabels == nil {
		return gen.ListSizeLabelsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	out := gen.SizeLabelList{Labels: []gen.SizeLabelRow{}, Machines: []gen.SizeLabelMachine{}, Example: sizelabel.Example}
	for _, u := range sizelabel.Understands() {
		out.Labels = append(out.Labels, gen.SizeLabelRow{Key: u.Key, Meaning: u.Meaning, Example: u.Example, Min: u.Min, Max: u.Max})
	}
	ms, err := s.opts.SizeLabels.SizeLabelMachines(ctx)
	if err != nil {
		return gen.ListSizeLabelsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeInternal, Message: "internal error"}}}, nil
	}
	for _, m := range ms {
		out.Machines = append(out.Machines, gen.SizeLabelMachine{Name: m.Name, Os: m.OS, Cpus: float32(m.CPUs), MemoryGib: float32(m.MemoryGiB)})
	}
	return gen.ListSizeLabels200JSONResponse(out), nil
}
