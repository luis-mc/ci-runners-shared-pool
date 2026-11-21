package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// UpdateService is what the update and restart routes need (operations R5.11
// to R5.18, tray R5.15 to R5.18). *update.Manager implements it.
type UpdateService interface {
	Status(ctx context.Context) update.Status
	Check(ctx context.Context) (update.Status, error)
	Apply(ctx context.Context, kind update.Kind, confirmed bool, actor string) (update.Status, error)
}

var _ UpdateService = (*update.Manager)(nil)

func toGenUpdate(st update.Status) gen.UpdateStatus {
	out := gen.UpdateStatus{
		CurrentVersion: st.Current, UpdateAvailable: st.Available, State: gen.UpdateStatusState(st.State),
		AutoUpdate: st.AutoUpdate, RunningJobs: make([]gen.UpdateJob, 0, len(st.RunningJobs)),
	}
	if st.Latest != "" {
		v := st.Latest
		out.LatestVersion = &v
	}
	if !st.CheckedAt.IsZero() {
		t := st.CheckedAt
		out.CheckedAt = &t
	}
	if st.Error != "" {
		e := st.Error
		out.Error = &e
	}
	if st.ReleaseURL != "" {
		u := st.ReleaseURL
		out.ReleaseUrl = &u
	}
	if st.DrainTimeoutMinutes > 0 {
		d := st.DrainTimeoutMinutes
		out.DrainTimeoutMinutes = &d
	}
	if !st.ServiceStarted.IsZero() {
		t := st.ServiceStarted
		out.ServiceStartedAt = &t
	}
	if !st.ServerTime.IsZero() {
		t := st.ServerTime
		out.ServerTime = &t
	}
	if st.ErrorAttempt != "" {
		e := st.ErrorAttempt
		out.ErrorAttempt = &e
	}
	if a := st.Attempt; a != nil {
		out.Attempt = &gen.UpdateAttempt{Id: a.ID, Kind: gen.UpdateAttemptKind(a.Kind), StartedAt: a.StartedAt}
	}
	if sg := st.Stage; sg != nil {
		g := gen.UpdateStage{Name: gen.UpdateStageName(sg.Name), At: sg.At}
		if sg.Percent >= 0 && (sg.Name == update.StageDownloading || sg.Name == update.StageInstalling) {
			p := sg.Percent
			g.Percent = &p
		}
		if sg.Version != "" {
			v := sg.Version
			g.Version = &v
		}
		if sg.Error != "" {
			e := sg.Error
			g.Error = &e
		}
		if sg.LogPath != "" {
			l := sg.LogPath
			g.LogPath = &l
		}
		if sg.WaitingFor != "" {
			w := sg.WaitingFor
			g.WaitingFor = &w
		}
		if sg.FailedAt != "" {
			f := gen.UpdateStageFailedAt(sg.FailedAt)
			g.FailedAt = &f
		}
		out.Stage = &g
	}
	for _, j := range st.RunningJobs {
		g := gen.UpdateJob{Repository: j.Repository, RunId: j.RunID, Job: j.Job}
		if j.Workflow != "" {
			w := j.Workflow
			g.Workflow = &w
		}
		out.RunningJobs = append(out.RunningJobs, g)
	}
	return out
}

// updateError maps an update error to the error envelope.
func (s *server) updateError(err error) opsFailure {
	var nc *update.NeedsConfirmation
	var vf *update.Failure
	switch {
	case errors.As(err, &nc):
		details := make([]gen.ErrorDetail, 0, len(nc.Jobs))
		for _, j := range nc.Jobs {
			details = append(details, gen.ErrorDetail{Kind: "job", Text: fmt.Sprintf("%s %s, run %d, job %s", j.Repository, j.Workflow, j.RunID, j.Job)})
		}
		msg := "Nothing has changed yet. Say yes to drain this machine and go on."
		if len(nc.Jobs) > 0 {
			msg = fmt.Sprintf("Nothing has changed yet. %d running job(s) finish first; no job is stopped.", len(nc.Jobs))
		}
		return envelope(http.StatusConflict, CodeNeedsConfirmation, msg, details...)
	case errors.Is(err, update.ErrNoUpdate), errors.Is(err, update.ErrBusy), errors.Is(err, update.ErrInProgress):
		return envelope(http.StatusConflict, CodeConflict, err.Error())
	case errors.As(err, &vf):
		return envelope(http.StatusConflict, CodeConflict, "The update was refused: "+vf.Error()+". The running build stays.")
	}
	s.opts.Logger.Warn("update request failed", "err", err)
	return envelope(http.StatusBadGateway, CodeGitHubUnavailable, "The update could not go on: "+err.Error())
}

// GetUpdateStatus implements GET /api/v1/update.
func (s *server) GetUpdateStatus(ctx context.Context, _ gen.GetUpdateStatusRequestObject) (gen.GetUpdateStatusResponseObject, error) {
	if s.opts.Update == nil {
		f := unavailable()
		return gen.GetUpdateStatusdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.GetUpdateStatus200JSONResponse(toGenUpdate(s.opts.Update.Status(ctx))), nil
}

// CheckForUpdate implements POST /api/v1/update/check.
func (s *server) CheckForUpdate(ctx context.Context, _ gen.CheckForUpdateRequestObject) (gen.CheckForUpdateResponseObject, error) {
	if s.opts.Update == nil {
		f := unavailable()
		return gen.CheckForUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	st, err := s.opts.Update.Check(ctx)
	if err != nil {
		f := s.updateError(err)
		return gen.CheckForUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.CheckForUpdate200JSONResponse(toGenUpdate(st)), nil
}

func (s *server) applyKind(ctx context.Context, kind update.Kind, confirmed *bool) (update.Status, *opsFailure) {
	if s.opts.Update == nil {
		f := unavailable()
		return update.Status{}, &f
	}
	st, err := s.opts.Update.Apply(ctx, kind, confirmed != nil && *confirmed, audit.ActorFrom(ctx).Login)
	if err != nil {
		f := s.updateError(err)
		return update.Status{}, &f
	}
	return st, nil
}

// ApplyUpdate implements POST /api/v1/update/apply.
func (s *server) ApplyUpdate(ctx context.Context, req gen.ApplyUpdateRequestObject) (gen.ApplyUpdateResponseObject, error) {
	st, f := s.applyKind(ctx, update.KindUpdate, req.Params.Confirmed)
	if f != nil {
		return gen.ApplyUpdatedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.ApplyUpdate202JSONResponse(toGenUpdate(st)), nil
}

// RestartService implements POST /api/v1/service/restart.
func (s *server) RestartService(ctx context.Context, req gen.RestartServiceRequestObject) (gen.RestartServiceResponseObject, error) {
	st, f := s.applyKind(ctx, update.KindRestart, req.Params.Confirmed)
	if f != nil {
		return gen.RestartServicedefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	return gen.RestartService202JSONResponse(toGenUpdate(st)), nil
}
