package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machinehealth"
)

// HealthService is what the machine health routes need (machines R5.72 to R5.79,
// doctor R5.66). *machinehealth.Service implements it.
type HealthService interface {
	View(ctx context.Context, machineID string) (machinehealth.View, error)
	Uptime(ctx context.Context, machineID string) (machinehealth.Uptime, error)
	MachineJobStats(ctx context.Context, machineID, window string) (machinehealth.MachineStats, error)
	RunnerJobStats(ctx context.Context, runnerID, window string) (machinehealth.RunnerStats, time.Time, time.Time, error)
	Thresholds(ctx context.Context) (machinehealth.Thresholds, error)
	SetThresholds(ctx context.Context, t machinehealth.Thresholds, by string) error
}

func init() {
	// The routes of OPS83; the role of each is the one its x-auth names.
	otherAreaAuth["getMachineHealth"] = authUser
	otherAreaAuth["getMachineJobStats"] = authUser
	otherAreaAuth["getRunnerJobStats"] = authUser
	otherAreaAuth["getHealthThresholds"] = authUser
	otherAreaAuth["putHealthThresholds"] = authAdmin
}

func healthUnavailable() gen.ErrorEnvelope { return notImplemented() }

func hptr[T any](v T) *T { return &v }

func htptr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func reason(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func msTime(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	return htptr(time.UnixMilli(ms))
}

func healthToAPI(v machinehealth.View) gen.MachineHealth {
	out := gen.MachineHealth{MachineId: v.MachineID, Reported: v.Reported, Stale: v.Stale, Volumes: []gen.HealthVolume{}}
	if !v.Reported {
		return out
	}
	r := v.Report
	out.ReceivedAt = htptr(v.ReceivedAt)

	if b := r.GetBoot(); b != nil {
		hb := gen.HealthBoot{UnknownReason: reason(b.GetUnknownReason())}
		if b.GetKnown() {
			hb.UptimeSeconds, hb.LastBootAt, hb.UnexpectedRestart = hptr(b.GetUptimeSeconds()), msTime(b.GetLastBootUnixMs()), hptr(b.GetUnexpectedRestart())
		}
		out.Boot = &hb
	}
	for _, d := range v.Disks {
		hv := gen.HealthVolume{Role: gen.HealthVolumeRole(d.Role), Path: d.Path, UnknownReason: reason(d.UnknownReason), DaysNote: reason(d.DaysNote)}
		if d.Known {
			hv.TotalBytes, hv.FreeBytes, hv.FreePct = hptr(d.Total), hptr(d.Free), hptr(float32(d.FreePct))
			if d.DaysUntilFull != nil {
				hv.DaysUntilFull = hptr(float32(*d.DaysUntilFull))
			}
		}
		out.Volumes = append(out.Volumes, hv)
	}
	if s := r.GetSwap(); s != nil {
		hs := gen.HealthSwap{UnknownReason: reason(s.GetUnknownReason())}
		if s.GetKnown() {
			hs.TotalBytes, hs.UsedBytes = hptr(s.GetTotalBytes()), hptr(s.GetUsedBytes())
		}
		out.Swap = &hs
	}
	if o := r.GetOom(); o != nil {
		ho := gen.HealthOom{UnknownReason: reason(o.GetUnknownReason())}
		if o.GetKnown() {
			ho.TotalKills, ho.Recent, ho.LastKillAt = hptr(o.GetTotalKills()), hptr(v.OOMRecent), htptr(v.OOMLastAt)
			scopes := []gen.HealthOomScope{}
			for _, sc := range o.GetScopes() {
				scopes = append(scopes, gen.HealthOomScope{Scope: gen.HealthOomScopeScope(sc.GetScope()), Name: sc.GetName(), Kills: sc.GetKills()})
			}
			ho.Scopes = &scopes
		}
		out.Oom = &ho
	}
	if c := r.GetConnectivity(); c != nil {
		hc := gen.HealthConnectivity{LastHeartbeatAt: msTime(c.GetLastHeartbeatUnixMs()), Reconnects24h: hptr(int(c.GetReconnects_24H()))}
		if c.GetRttKnown() {
			hc.RttMs = hptr(c.GetRttMs())
		}
		if c.GetSkewKnown() {
			hc.ClockSkewSeconds = hptr(float32(c.GetClockSkewSeconds()))
		}
		if c.GetGithubKnown() {
			hc.Github = &gen.HealthGithub{Reachable: hptr(c.GetGithubReachable()), LatencyMs: hptr(c.GetGithubLatencyMs()),
				CheckedAt: msTime(c.GetGithubCheckedUnixMs()), Error: reason(c.GetGithubError())}
		}
		if c.GetDnsKnown() {
			hc.Dns = &gen.HealthDns{Ok: hptr(c.GetDnsOk()), Error: reason(c.GetDnsError())}
		}
		out.Connectivity = &hc
	}
	if vs := r.GetVersions(); vs != nil {
		out.Versions = &gen.HealthVersions{Agent: reason(vs.GetAgentVersion()), Controller: reason(v.ControllerVersion), AgentOutOfDate: hptr(v.AgentOutOfDate),
			RunnerApp: reason(vs.GetRunnerAppVersion()), LatestRunner: reason(v.LatestRunner), RunnerOutOfDate: hptr(v.RunnerOutOfDate),
			DockerKind: reason(vs.GetDockerKind()), Docker: reason(vs.GetDockerVersion()), Wsl: reason(vs.GetWslVersion())}
	}
	if u := r.GetUpdates(); u != nil {
		hu := gen.HealthUpdates{UnknownReason: reason(u.GetUnknownReason())}
		if u.GetKnown() {
			reasons := append([]string{}, u.GetRestartReasons()...)
			hu.RestartPending, hu.RestartReasons, hu.AutoUpdateState = hptr(u.GetRestartPending()), &reasons, reason(u.GetAutoUpdateState())
			hu.RestartMayHappenNow, hu.RestartMayHitJobs = hptr(v.RestartMayHappenNow), hptr(v.RestartMayHitJobs)
			if u.GetUpdateCountKnown() {
				hu.PendingUpdateCount = hptr(int(u.GetPendingUpdateCount()))
			}
			if u.GetActiveHoursKnown() {
				hu.ActiveHoursStart, hu.ActiveHoursEnd = hptr(int(u.GetActiveHoursStart())), hptr(int(u.GetActiveHoursEnd()))
			}
		}
		out.Updates = &hu
	}
	if p := r.GetPower(); p != nil {
		hp := gen.HealthPower{SourceUnknownReason: reason(p.GetSourceUnknownReason()), SleepUnknownReason: reason(p.GetSleepUnknownReason())}
		if p.GetSourceKnown() {
			hp.OnBattery = hptr(p.GetOnBattery())
		}
		if p.GetSleepKnown() {
			modes := append([]string{}, p.GetSleepModes()...)
			hp.SleepEnabled, hp.SleepModes = hptr(p.GetSleepEnabled()), &modes
		}
		if p.GetWakeKnown() {
			hp.LastWakeAt = msTime(p.GetLastWakeUnixMs())
		}
		out.Power = &hp
	}
	if d := r.GetDockerUse(); d != nil {
		hd := gen.HealthDockerUse{UnknownReason: reason(d.GetUnknownReason())}
		if d.GetKnown() {
			hd.ImagesBytes, hd.BuildCacheBytes, hd.VolumesBytes, hd.ContainersBytes, hd.ReadAt = hptr(d.GetImagesBytes()), hptr(d.GetBuildCacheBytes()),
				hptr(d.GetVolumesBytes()), hptr(d.GetContainersBytes()), msTime(d.GetReadUnixMs())
		}
		out.DockerUse = &hd
	}
	if t := r.GetThermal(); t != nil {
		ht := gen.HealthThermal{UnknownReason: reason(t.GetUnknownReason())}
		if t.GetKnown() {
			ht.MaxTempCelsius, ht.Throttled, ht.ThrottleDetail = hptr(float32(t.GetMaxTempCelsius())), hptr(t.GetThrottled()), reason(t.GetThrottleDetail())
		}
		out.Thermal = &ht
	}
	if l := r.GetLoadAverage(); l != nil {
		hl := gen.HealthLoad{LoadUnknownReason: reason(l.GetLoadUnknownReason()), StealUnknownReason: reason(l.GetStealUnknownReason())}
		if l.GetLoadKnown() {
			hl.Load1, hl.Load5, hl.Load15 = hptr(float32(l.GetLoad1())), hptr(float32(l.GetLoad5())), hptr(float32(l.GetLoad15()))
		}
		if l.GetStealKnown() {
			hl.StealPct = hptr(float32(l.GetStealPct()))
		}
		out.Load = &hl
	}
	return out
}

func statsToAPI(st machinehealth.Stats) gen.JobStatsSet {
	c := st.Counts
	out := gen.JobStatsSet{
		Counts: gen.JobStatsCounts{Succeeded: c.Succeeded, Failed: c.Failed, Cancelled: c.Cancelled, TimedOut: c.TimedOut, OverMemory: c.OverMemory,
			Stopped: c.Stopped, Other: c.Other, Total: c.Total()},
		TopFailing: []gen.JobStatsFailingJob{}, BusySeconds: hptr(float32(st.BusySeconds)),
	}
	f32 := func(p *float64) *float32 {
		if p == nil {
			return nil
		}
		return hptr(float32(*p))
	}
	if st.Counts.Total() > 0 {
		out.Rates = &gen.JobStatsRates{Succeeded: f32(st.Rates.Succeeded), Failed: f32(st.Rates.Failed), Cancelled: f32(st.Rates.Cancelled),
			TimedOut: f32(st.Rates.TimedOut), OverMemory: f32(st.Rates.OverMemory), Stopped: f32(st.Rates.Stopped)}
	}
	if st.AvgRunKnown {
		out.AvgRunSeconds = hptr(float32(st.AvgRunSeconds))
	}
	if st.AvgQueueWaitKnown {
		out.AvgQueueWaitSeconds = hptr(float32(st.AvgQueueWaitSeconds))
	}
	out.BusyPct = f32(st.BusyPct)
	if lf := st.LastFailure; lf != nil {
		out.LastFailure = &gen.JobStatsFailure{JobId: lf.JobID, Repository: lf.Repository, Job: lf.Name, RunId: lf.RunID, Class: gen.JobStatsFailureClass(lf.Class),
			At: lf.At.UTC(), JobUrl: reason(lf.JobURL), RunUrl: reason(lf.RunURL)}
	}
	for _, t := range st.TopFailing {
		out.TopFailing = append(out.TopFailing, gen.JobStatsFailingJob{Repository: t.Repository, Job: t.Name, Failures: t.Failures, LastAt: t.Last.UTC()})
	}
	return out
}

func statsWindow(w *string) string {
	if w == nil || *w == "" {
		return machinehealth.Window24h
	}
	return *w
}

func windowErr() gen.ErrorEnvelope {
	return errEnvelope(CodeBadRequest, "The window must be 24h or 7d.")
}

// GetMachineHealth implements GET /api/v1/machines/{machineId}/health.
func (s *server) GetMachineHealth(ctx context.Context, r gen.GetMachineHealthRequestObject) (gen.GetMachineHealthResponseObject, error) {
	if s.opts.Health == nil || s.machinesOff() {
		return gen.GetMachineHealthdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	if _, err := s.opts.Machines.Get(ctx, r.MachineId); err != nil {
		return fail[gen.GetMachineHealthdefaultJSONResponse](s, err), nil
	}
	v, err := s.opts.Health.View(ctx, r.MachineId)
	if err != nil {
		return gen.GetMachineHealthdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetMachineHealth200JSONResponse(healthToAPI(v)), nil
}

// GetMachineJobStats implements GET /api/v1/machines/{machineId}/job-stats.
func (s *server) GetMachineJobStats(ctx context.Context, r gen.GetMachineJobStatsRequestObject) (gen.GetMachineJobStatsResponseObject, error) {
	if s.opts.Health == nil || s.machinesOff() {
		return gen.GetMachineJobStatsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	var w *string
	if r.Params.Window != nil {
		w = hptr(string(*r.Params.Window))
	}
	if _, err := s.opts.Machines.Get(ctx, r.MachineId); err != nil {
		return fail[gen.GetMachineJobStatsdefaultJSONResponse](s, err), nil
	}
	ms, err := s.opts.Health.MachineJobStats(ctx, r.MachineId, statsWindow(w))
	if err != nil {
		if _, werr := machinehealth.WindowDuration(statsWindow(w)); werr != nil {
			return gen.GetMachineJobStatsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: windowErr()}, nil
		}
		return gen.GetMachineJobStatsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	out := gen.GetMachineJobStats200JSONResponse{Window: gen.MachineJobStatsWindow(ms.Window), From: ms.From.UTC(), To: ms.To.UTC(),
		Machine: statsToAPI(ms.Machine), Runners: []gen.RunnerJobStatsEntry{}}
	for _, r := range ms.Runners {
		out.Runners = append(out.Runners, gen.RunnerJobStatsEntry{RunnerId: r.RunnerID, RunnerName: r.RunnerName, Stats: statsToAPI(r.Stats)})
	}
	return out, nil
}

// GetRunnerJobStats implements GET /api/v1/runners/{runnerId}/job-stats.
func (s *server) GetRunnerJobStats(ctx context.Context, r gen.GetRunnerJobStatsRequestObject) (gen.GetRunnerJobStatsResponseObject, error) {
	if s.opts.Health == nil {
		return gen.GetRunnerJobStatsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	var w *string
	if r.Params.Window != nil {
		w = hptr(string(*r.Params.Window))
	}
	rs, from, to, err := s.opts.Health.RunnerJobStats(ctx, r.RunnerId, statsWindow(w))
	if err != nil {
		if _, werr := machinehealth.WindowDuration(statsWindow(w)); werr != nil {
			return gen.GetRunnerJobStatsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: windowErr()}, nil
		}
		return gen.GetRunnerJobStatsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetRunnerJobStats200JSONResponse{Window: gen.RunnerJobStatsWindow(statsWindow(w)), From: from.UTC(), To: to.UTC(), RunnerId: r.RunnerId, Stats: statsToAPI(rs.Stats)}, nil
}

func thresholdsToAPI(t machinehealth.Thresholds) gen.HealthThresholds {
	return gen.HealthThresholds{DiskDaysUnder: t.DiskDaysUnder, DiskFreePctUnder: t.DiskFreePctUnder, NoHeartbeatSeconds: t.NoHeartbeatSeconds,
		ClockSkewSeconds: t.ClockSkewSeconds, OomWindowMinutes: t.OOMWindowMinutes}
}

// GetHealthThresholds implements GET /api/v1/health-thresholds.
func (s *server) GetHealthThresholds(ctx context.Context, _ gen.GetHealthThresholdsRequestObject) (gen.GetHealthThresholdsResponseObject, error) {
	if s.opts.Health == nil {
		return gen.GetHealthThresholdsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	t, err := s.opts.Health.Thresholds(ctx)
	if err != nil {
		return gen.GetHealthThresholdsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.GetHealthThresholds200JSONResponse(thresholdsToAPI(t)), nil
}

// PutHealthThresholds implements PUT /api/v1/health-thresholds.
func (s *server) PutHealthThresholds(ctx context.Context, r gen.PutHealthThresholdsRequestObject) (gen.PutHealthThresholdsResponseObject, error) {
	if s.opts.Health == nil {
		return gen.PutHealthThresholdsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: healthUnavailable()}, nil
	}
	b := r.Body
	t := machinehealth.Thresholds{DiskDaysUnder: b.DiskDaysUnder, DiskFreePctUnder: b.DiskFreePctUnder, NoHeartbeatSeconds: b.NoHeartbeatSeconds,
		ClockSkewSeconds: b.ClockSkewSeconds, OOMWindowMinutes: b.OomWindowMinutes}
	if err := s.opts.Health.SetThresholds(ctx, t, s.actor(ctx)); err != nil {
		if errors.Is(err, machinehealth.ErrBadThreshold) {
			return gen.PutHealthThresholdsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, err.Error())}, nil
		}
		return gen.PutHealthThresholdsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	return gen.PutHealthThresholds200JSONResponse(thresholdsToAPI(t)), nil
}

var _ HealthService = (*machinehealth.Service)(nil)
