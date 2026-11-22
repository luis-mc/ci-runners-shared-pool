package api

import (
	"context"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/listenergate"
)

// ListenerGateService is what the listener gate routes need (runners R5.134 to
// R5.140). *listenergate.Control implements it.
type ListenerGateService interface {
	View(ctx context.Context) (listenergate.View, error)
	Save(ctx context.Context, enforce bool, by string) (listenergate.Settings, error)
}

func genListenerGate(v listenergate.View) gen.ListenerGate {
	out := gen.ListenerGate{Enforce: v.Enforce, WakeEveryDays: int(v.WakeEvery / (24 * time.Hour)), Runners: []gen.ListenerGateRunner{}}
	for _, r := range v.Runners {
		g := gen.ListenerGateRunner{RunnerName: r.Runner.Name, MachineId: r.Runner.MachineID, Os: r.Runner.OS, Text: r.Text,
			ListenerOn: r.ListenerOn, NextWake: r.NextWake, LastWake: optTime(r.LastWake), QueueText: optStr(r.QueueText)}
		if len(r.JobIDs) > 0 {
			ids := r.JobIDs
			g.JobIds = &ids
		}
		out.Runners = append(out.Runners, g)
	}
	return out
}

func (s *server) gateOff() bool { return s.opts.ListenerGate == nil }

// GetListenerGate implements GET /api/v1/listener-gate.
func (s *server) GetListenerGate(ctx context.Context, _ gen.GetListenerGateRequestObject) (gen.GetListenerGateResponseObject, error) {
	if s.gateOff() {
		return gen.GetListenerGatedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.ListenerGate.View(ctx)
	if err != nil {
		return gen.GetListenerGatedefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: timerEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetListenerGate200JSONResponse(genListenerGate(v)), nil
}

// PutListenerGate implements PUT /api/v1/listener-gate.
func (s *server) PutListenerGate(ctx context.Context, r gen.PutListenerGateRequestObject) (gen.PutListenerGateResponseObject, error) {
	if s.gateOff() {
		return gen.PutListenerGatedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return gen.PutListenerGatedefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: timerEnvelope(CodeBadRequest, "Say whether the gate is on.")}, nil
	}
	if _, err := s.opts.ListenerGate.Save(ctx, r.Body.Enforce, s.actor(ctx)); err != nil {
		return gen.PutListenerGatedefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: timerEnvelope(CodeInternal, "internal error")}, nil
	}
	v, err := s.opts.ListenerGate.View(ctx)
	if err != nil {
		return gen.PutListenerGatedefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: timerEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.PutListenerGate200JSONResponse(genListenerGate(v)), nil
}
