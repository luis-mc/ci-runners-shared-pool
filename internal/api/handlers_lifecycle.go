package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update/lifecycle"
)

// LifecycleService is the update state machine behind the lifecycle routes. *update.Service implements it.
type LifecycleService interface {
	Status(ctx context.Context) update.LifecycleStatus
	Check(ctx context.Context, actor string) (update.LifecycleStatus, error)
	Install(ctx context.Context, actor string, remote bool, version string) (update.LifecycleStatus, error)
	SetPolicy(ctx context.Context, policy string, w *lifecycle.Window) (update.LifecycleStatus, error)
	History(ctx context.Context) ([]update.HistoryRow, error)
}

var _ LifecycleService = (*update.Service)(nil)

// FleetMachine is one machine of the fleet list.
type FleetMachine struct {
	ID, Name, Version, State string
	// Local says this is the machine that serves the request.
	Local bool
}

// FleetService lists the machines with their update state and asks one to update.
type FleetService interface {
	Fleet(ctx context.Context) ([]FleetMachine, error)
	AskUpdate(machineID, version string) error
}

// RemoveService removes crsp from a machine. machineID is empty for this machine.
type RemoveService interface {
	Remove(ctx context.Context, actor audit.Actor, machineID string, deleteData bool) error
}

func toGenLifecycle(st update.LifecycleStatus) gen.LifecycleStatus {
	out := gen.LifecycleStatus{
		State: gen.LifecycleStatusState(st.State), Current: st.Current, Required: st.Required,
		Policy: gen.LifecycleStatusPolicy(st.Policy), NextCheckAt: st.NextCheckAt, WaitingForJobs: st.WaitingForJobs,
	}
	if st.Available != "" {
		v := st.Available
		out.Available = &v
	}
	if w := st.Window; w != nil {
		out.Window = &struct {
			End   string `json:"end"`
			Start string `json:"start"`
		}{End: w.End, Start: w.Start}
	}
	if f := st.Failures; f != nil {
		out.Failures = &struct {
			Count   int        `json:"count"`
			RetryAt *time.Time `json:"retry_at"`
			Version string     `json:"version"`
		}{Count: f.Count, RetryAt: f.RetryAt, Version: f.Version}
	}
	return out
}

func (s *server) lifecycleError(err error) opsFailure {
	switch {
	case errors.Is(err, update.ErrNotPaired), errors.Is(err, update.ErrNoOffer):
		return envelope(http.StatusConflict, CodeConflict, err.Error())
	}
	return envelope(http.StatusBadRequest, CodeBadRequest, err.Error())
}

// GetLifecycleStatus implements GET /api/v1/update/status.
func (s *server) GetLifecycleStatus(ctx context.Context, _ gen.GetLifecycleStatusRequestObject) (gen.GetLifecycleStatusResponseObject, error) {
	if s.opts.Lifecycle == nil {
		f := unavailable()
		return gen.GetLifecycleStatusdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.GetLifecycleStatus200JSONResponse(toGenLifecycle(s.opts.Lifecycle.Status(ctx))), nil
}

// SetUpdatePolicy implements POST /api/v1/update/policy.
func (s *server) SetUpdatePolicy(ctx context.Context, req gen.SetUpdatePolicyRequestObject) (gen.SetUpdatePolicyResponseObject, error) {
	if s.opts.Lifecycle == nil {
		f := unavailable()
		return gen.SetUpdatePolicydefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	var w *lifecycle.Window
	if req.Body.Window != nil {
		w = &lifecycle.Window{Start: req.Body.Window.Start, End: req.Body.Window.End}
	}
	st, err := s.opts.Lifecycle.SetPolicy(ctx, string(req.Body.Policy), w)
	if err != nil {
		f := s.lifecycleError(err)
		return gen.SetUpdatePolicydefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.SetUpdatePolicy200JSONResponse(toGenLifecycle(st)), nil
}

// remoteRequest says the request did not come from this machine's own loopback address.
func remoteRequest(ctx context.Context) bool {
	r := requestFrom(ctx)
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

// InstallUpdate implements POST /api/v1/update/install. With machine_id it asks that machine to update.
func (s *server) InstallUpdate(ctx context.Context, req gen.InstallUpdateRequestObject) (gen.InstallUpdateResponseObject, error) {
	if s.opts.Lifecycle == nil {
		f := unavailable()
		return gen.InstallUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	version, machine := "", ""
	if req.Body.Version != nil {
		version = *req.Body.Version
	}
	if req.Body.MachineId != nil {
		machine = *req.Body.MachineId
	}
	actor := audit.ActorFrom(ctx)
	if machine != "" && s.opts.Fleet != nil {
		fleet, err := s.opts.Fleet.Fleet(ctx)
		if err != nil {
			f := s.lifecycleError(err)
			return gen.InstallUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
		}
		local := true
		for _, m := range fleet {
			if m.ID == machine {
				local = m.Local
			}
		}
		if !local {
			return s.installElsewhere(ctx, actor, machine, version)
		}
	}
	st, err := s.opts.Lifecycle.Install(ctx, actor.Login, machine != "" || remoteRequest(ctx), version)
	if err != nil {
		f := s.lifecycleError(err)
		return gen.InstallUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.InstallUpdate202JSONResponse(toGenLifecycle(st)), nil
}

// installElsewhere asks another machine's agent to update; the row names the owner as actor and remote true.
func (s *server) installElsewhere(ctx context.Context, actor audit.Actor, machine, version string) (gen.InstallUpdateResponseObject, error) {
	result, res := "ok", audit.Target{Type: "machine", ID: machine}
	err := s.opts.Fleet.AskUpdate(machine, version)
	if err != nil {
		result = "failed"
	}
	if s.opts.Audit != nil {
		_ = s.opts.Audit.Record(ctx, actor, "update.install", res, map[string]any{"version": version, "result": result, "remote": true})
	}
	if err != nil {
		f := s.lifecycleError(err)
		return gen.InstallUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.InstallUpdate202JSONResponse(toGenLifecycle(s.opts.Lifecycle.Status(ctx))), nil
}

// ListUpdateHistory implements GET /api/v1/update/history.
func (s *server) ListUpdateHistory(ctx context.Context, _ gen.ListUpdateHistoryRequestObject) (gen.ListUpdateHistoryResponseObject, error) {
	if s.opts.Lifecycle == nil {
		f := unavailable()
		return gen.ListUpdateHistorydefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	rows, err := s.opts.Lifecycle.History(ctx)
	if err != nil {
		f := s.lifecycleError(err)
		return gen.ListUpdateHistorydefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	out := make(gen.ListUpdateHistory200JSONResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, gen.UpdateHistoryRow{At: r.At, Actor: r.Actor, Action: gen.UpdateHistoryRowAction(r.Action), Version: r.Version, Result: r.Result})
	}
	return out, nil
}

// ListUpdateFleet implements GET /api/v1/update/fleet.
func (s *server) ListUpdateFleet(ctx context.Context, _ gen.ListUpdateFleetRequestObject) (gen.ListUpdateFleetResponseObject, error) {
	if s.opts.Fleet == nil {
		f := unavailable()
		return gen.ListUpdateFleetdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	ms, err := s.opts.Fleet.Fleet(ctx)
	if err != nil {
		f := s.lifecycleError(err)
		return gen.ListUpdateFleetdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	out := make(gen.ListUpdateFleet200JSONResponse, 0, len(ms))
	for _, m := range ms {
		out = append(out, gen.UpdateFleetMachine{MachineId: m.ID, Name: m.Name, Version: m.Version, State: m.State})
	}
	return out, nil
}

// RemoveCrsp implements POST /api/v1/remove. delete_data has no default: a missing value is refused.
func (s *server) RemoveCrsp(ctx context.Context, req gen.RemoveCrspRequestObject) (gen.RemoveCrspResponseObject, error) {
	if s.opts.Remove == nil {
		f := unavailable()
		return gen.RemoveCrspdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if req.Body == nil || req.Body.DeleteData == nil {
		f := envelope(http.StatusBadRequest, CodeBadRequest, "Choose Keep my data or Delete my data too first.")
		return gen.RemoveCrspdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	machine := ""
	if req.Body.MachineId != nil {
		machine = *req.Body.MachineId
	}
	if err := s.opts.Remove.Remove(ctx, audit.ActorFrom(ctx), machine, *req.Body.DeleteData); err != nil {
		f := s.lifecycleError(err)
		return gen.RemoveCrspdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.RemoveCrsp202Response{}, nil
}
