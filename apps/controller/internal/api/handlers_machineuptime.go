package api

import (
	"context"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machinehealth"
)

func init() {
	otherAreaAuth["getMachineUptime"] = authUser
}

func uptimeToAPI(u machinehealth.Uptime) gen.MachineUptime {
	out := gen.MachineUptime{MachineId: u.MachineID, Watched: u.Watched, Windows: []gen.UptimeWindow{}, Periods: []gen.UptimePeriod{}}
	if u.Watched {
		out.Since = htptr(u.Since)
	}
	for _, w := range u.Windows {
		aw := gen.UptimeWindow{Window: gen.UptimeWindowWindow(w.Name), CoveredSeconds: int64(w.Covered.Seconds()),
			OnlineSeconds: int64(w.Online.Seconds()), TakingSeconds: int64(w.Taking.Seconds())}
		if w.UptimePct != nil {
			aw.UptimePct = hptr(float32(*w.UptimePct))
		}
		if w.TakingPct != nil {
			aw.TakingPct = hptr(float32(*w.TakingPct))
		}
		out.Windows = append(out.Windows, aw)
	}
	for _, p := range u.Periods {
		out.Periods = append(out.Periods, gen.UptimePeriod{StartedAt: p.Start.UTC(), EndedAt: htptr(p.End), Offline: p.Offline, Reason: p.Reason})
	}
	return out
}

// GetMachineUptime implements GET /api/v1/machines/{machineId}/uptime.
func (s *server) GetMachineUptime(ctx context.Context, r gen.GetMachineUptimeRequestObject) (gen.GetMachineUptimeResponseObject, error) {
	if s.opts.Health == nil || s.machinesOff() {
		return gen.GetMachineUptimedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	if _, err := s.opts.Machines.Get(ctx, r.MachineId); err != nil {
		return fail[gen.GetMachineUptimedefaultJSONResponse](s, err), nil
	}
	u, err := s.opts.Health.Uptime(ctx, r.MachineId)
	if err != nil {
		return gen.GetMachineUptimedefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetMachineUptime200JSONResponse(uptimeToAPI(u)), nil
}
