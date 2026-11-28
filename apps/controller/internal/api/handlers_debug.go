package api

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/debugaccess"
)

// DebugService is what the debug access routes need (docs/features/debug-access,
// CRSP331). *debugaccess.Service implements it. The service checks the role
// again on every change, whatever x-auth let through (R5.2, web-console R6.5).
type DebugService interface {
	Pool(ctx context.Context) debugaccess.PoolSettings
	OpenCount(ctx context.Context) (int, error)
	SetPool(ctx context.Context, p auth.Principal, next debugaccess.PoolSettings, confirm bool) (int, error)
	Repo(ctx context.Context, repoID int64) (debugaccess.RepoSettings, error)
	RepoInfo(ctx context.Context, repoID int64) (debugaccess.Repo, error)
	SetRepo(ctx context.Context, p auth.Principal, repoID int64, next debugaccess.RepoSettings) (debugaccess.RepoSettings, error)
	JobCard(ctx context.Context, p auth.Principal, jobID int64) (debugaccess.JobView, error)
	Open(ctx context.Context, p auth.Principal, req debugaccess.OpenRequest) (debugaccess.Session, error)
	Session(ctx context.Context, id string) (debugaccess.Session, error)
	Sessions(ctx context.Context, limit int) ([]debugaccess.Session, error)
	Extend(ctx context.Context, p auth.Principal, id string) (debugaccess.Session, error)
	End(ctx context.Context, p auth.Principal, id string) (debugaccess.Session, error)
	Transcript(ctx context.Context, p auth.Principal, id string) (debugaccess.Transcript, bool, error)
}

// The words of the repository setting (R5.14), shown next to the switch.
const (
	debugAllows = "An admin can open a terminal inside a running job of this repository, for a limited time, with a reason. " +
		"It can see this job's files and its running processes' output, not its secrets. Every session is in the audit log."
	debugNever = "Never the job's secrets, the machine's own shell, other jobs, or a public repository or pull request. " +
		"A person with write access to the repository can already read masked secrets by writing a workflow; debug access does not widen that."
	debugTranscriptNote = "Cleaned as far as possible. Redaction cannot promise to catch everything."
)

func (s *server) debugOff() bool { return s.opts.Debug == nil }

// debugFailure maps a service error to a status and the error envelope. A
// refusal keeps its words, so the API and the console say the same thing.
func (s *server) debugFailure(err error) (int, gen.ErrorEnvelope) {
	var r *debugaccess.Refusal
	if !errors.As(err, &r) {
		s.opts.Logger.Error("debug access", "err", err)
		return http.StatusInternalServerError, errEnvelope(CodeInternal, "internal error")
	}
	switch r.Code {
	case debugaccess.CodeNotFound:
		return http.StatusNotFound, errEnvelope(CodeNotFound, r.Text)
	case debugaccess.CodeNeedsConfirm:
		return http.StatusConflict, errEnvelope(CodeNeedsConfirmation, r.Text)
	case debugaccess.CodeBadSetting, debugaccess.CodeReason:
		return http.StatusBadRequest, errEnvelope(CodeBadRequest, r.Text)
	case debugaccess.CodeEnded, debugaccess.CodeExtended, debugaccess.CodePastLimit, debugaccess.CodeJobCap, debugaccess.CodeUserCap:
		return http.StatusConflict, errEnvelope(CodeConflict, r.Text)
	}
	return http.StatusForbidden, errEnvelope(CodeForbidden, r.Text)
}

func genDebugSession(d debugaccess.Session) gen.DebugSession {
	out := gen.DebugSession{Id: d.ID, JobId: d.JobID, Repository: d.Repository, User: d.UserLogin, Reason: d.Reason, Source: d.Source,
		State: gen.DebugSessionStateOpen, OpenedAt: d.OpenedAt, EndsAt: d.EndsAt, Extended: d.Extended, Recorded: d.Recorded,
		MachineId: optStr(d.MachineID), RunnerName: optStr(d.RunnerName), Backend: optStr(d.Backend), Isolation: optStr(d.Isolation), Os: optStr(d.OS)}
	if !d.EndedAt.IsZero() {
		out.State = gen.DebugSessionStateEnded
		at := d.EndedAt
		out.EndedAt = &at
		why := gen.DebugSessionEndReason(d.EndReason)
		out.EndReason = &why
		out.EndedBy = optStr(d.EndedBy)
	}
	return out
}

func genDebugRepo(id int64, r debugaccess.RepoSettings, info debugaccess.Repo) gen.DebugRepoAccess {
	out := gen.DebugRepoAccess{RepositoryId: id, Enabled: r.Enabled, Who: gen.DebugRepoAccessWho(r.Who), LimitMin: r.LimitMin,
		IdleMin: r.IdleMin, HoldMin: r.HoldMin, PersistentOk: r.PersistentOK, Allows: debugAllows, Never: debugNever,
		UpdatedBy: optStr(r.UpdatedBy)}
	if !r.UpdatedAt.IsZero() {
		at := r.UpdatedAt
		out.UpdatedAt = &at
	}
	if info.Visibility != "private" && info.Visibility != "internal" {
		out.DisabledReason = optStr(debugaccess.TextPublic)
	}
	return out
}

func (s *server) genDebugPool(ctx context.Context, ended *int) (gen.DebugPoolSettings, error) {
	p := s.opts.Debug.Pool(ctx)
	n, err := s.opts.Debug.OpenCount(ctx)
	if err != nil {
		return gen.DebugPoolSettings{}, err
	}
	out := gen.DebugPoolSettings{Enabled: p.Enabled, Record: p.Record, OpenSessions: n, Ended: ended, UpdatedBy: optStr(p.UpdatedBy)}
	if !p.UpdatedAt.IsZero() {
		at := p.UpdatedAt
		out.UpdatedAt = &at
	}
	return out, nil
}

// remoteHost is the request's remote address without its port, for the
// opened audit row (R5.24).
func remoteHost(ctx context.Context) string {
	r := requestFrom(ctx)
	if r == nil {
		return ""
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

// GetDebugPoolSettings implements GET /api/v1/debug-access.
func (s *server) GetDebugPoolSettings(ctx context.Context, _ gen.GetDebugPoolSettingsRequestObject) (gen.GetDebugPoolSettingsResponseObject, error) {
	if s.debugOff() {
		return gen.GetDebugPoolSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	out, err := s.genDebugPool(ctx, nil)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.GetDebugPoolSettingsdefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	return gen.GetDebugPoolSettings200JSONResponse(out), nil
}

// PutDebugPoolSettings implements PUT /api/v1/debug-access (the kill switch, R5.26).
func (s *server) PutDebugPoolSettings(ctx context.Context, r gen.PutDebugPoolSettingsRequestObject) (gen.PutDebugPoolSettingsResponseObject, error) {
	if s.debugOff() {
		return gen.PutDebugPoolSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return gen.PutDebugPoolSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "Say whether debug sessions are allowed.")}, nil
	}
	confirm := r.Body.Confirm != nil && *r.Body.Confirm
	n, err := s.opts.Debug.SetPool(ctx, s.principal(ctx), debugaccess.PoolSettings{Enabled: r.Body.Enabled, Record: r.Body.Record}, confirm)
	if err == nil {
		var out gen.DebugPoolSettings
		if out, err = s.genDebugPool(ctx, &n); err == nil {
			return gen.PutDebugPoolSettings200JSONResponse(out), nil
		}
	}
	st, body := s.debugFailure(err)
	return gen.PutDebugPoolSettingsdefaultJSONResponse{StatusCode: st, Body: body}, nil
}

// GetRepoDebugAccess implements GET /api/v1/repositories/{repositoryId}/debug-access.
func (s *server) GetRepoDebugAccess(ctx context.Context, r gen.GetRepoDebugAccessRequestObject) (gen.GetRepoDebugAccessResponseObject, error) {
	if s.debugOff() {
		return gen.GetRepoDebugAccessdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	set, err := s.opts.Debug.Repo(ctx, r.RepositoryId)
	if err == nil {
		var info debugaccess.Repo
		if info, err = s.opts.Debug.RepoInfo(ctx, r.RepositoryId); err == nil {
			if !info.Known {
				return gen.GetRepoDebugAccessdefaultJSONResponse{StatusCode: http.StatusNotFound, Body: errEnvelope(CodeNotFound, debugaccess.TextRepoUnknown)}, nil
			}
			return gen.GetRepoDebugAccess200JSONResponse(genDebugRepo(r.RepositoryId, set, info)), nil
		}
	}
	st, body := s.debugFailure(err)
	return gen.GetRepoDebugAccessdefaultJSONResponse{StatusCode: st, Body: body}, nil
}

// SetRepoDebugAccess implements PUT /api/v1/repositories/{repositoryId}/debug-access.
func (s *server) SetRepoDebugAccess(ctx context.Context, r gen.SetRepoDebugAccessRequestObject) (gen.SetRepoDebugAccessResponseObject, error) {
	if s.debugOff() {
		return gen.SetRepoDebugAccessdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return gen.SetRepoDebugAccessdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "Send the setting.")}, nil
	}
	b := r.Body
	next := debugaccess.RepoSettings{Enabled: b.Enabled, Who: string(b.Who), LimitMin: b.LimitMin, IdleMin: b.IdleMin, HoldMin: b.HoldMin,
		PersistentOK: b.PersistentOk}
	set, err := s.opts.Debug.SetRepo(ctx, s.principal(ctx), r.RepositoryId, next)
	if err == nil {
		var info debugaccess.Repo
		if info, err = s.opts.Debug.RepoInfo(ctx, r.RepositoryId); err == nil {
			return gen.SetRepoDebugAccess200JSONResponse(genDebugRepo(r.RepositoryId, set, info)), nil
		}
	}
	st, body := s.debugFailure(err)
	return gen.SetRepoDebugAccessdefaultJSONResponse{StatusCode: st, Body: body}, nil
}

// GetJobDebug implements GET /api/v1/jobs/{jobId}/debug.
func (s *server) GetJobDebug(ctx context.Context, r gen.GetJobDebugRequestObject) (gen.GetJobDebugResponseObject, error) {
	if s.debugOff() {
		return gen.GetJobDebugdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Debug.JobCard(ctx, s.principal(ctx), r.JobId)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.GetJobDebugdefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	out := gen.JobDebug{JobId: r.JobId, Allowed: v.Refusal == nil, Warnings: append([]string{}, v.Warnings...),
		Sees: debugaccess.TextWhatItSees, RepositoryEnabled: v.Repo.Enabled, Sessions: []gen.DebugSession{}}
	if v.Refusal != nil {
		out.WhyNotCode, out.WhyNot = optStr(v.Refusal.Code), optStr(v.Refusal.Text)
	}
	if v.Repo.Enabled {
		lim, idle := v.Repo.LimitMin, v.Repo.IdleMin
		out.LimitMin, out.IdleMin = &lim, &idle
	}
	if v.Open != nil {
		o := genDebugSession(*v.Open)
		out.OpenSession = &o
	}
	for _, d := range v.Sessions {
		out.Sessions = append(out.Sessions, genDebugSession(d))
	}
	return gen.GetJobDebug200JSONResponse(out), nil
}

// OpenDebugSession implements POST /api/v1/jobs/{jobId}/debug-sessions.
func (s *server) OpenDebugSession(ctx context.Context, r gen.OpenDebugSessionRequestObject) (gen.OpenDebugSessionResponseObject, error) {
	if s.debugOff() {
		return gen.OpenDebugSessiondefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	reason := ""
	if r.Body != nil {
		reason = r.Body.Reason
	}
	p := s.principal(ctx)
	d, err := s.opts.Debug.Open(ctx, p, debugaccess.OpenRequest{JobID: r.JobId, Reason: reason, Source: p.Source, Addr: remoteHost(ctx)})
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.OpenDebugSessiondefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	return gen.OpenDebugSession201JSONResponse(genDebugSession(d)), nil
}

// ListDebugSessions implements GET /api/v1/debug-sessions.
func (s *server) ListDebugSessions(ctx context.Context, _ gen.ListDebugSessionsRequestObject) (gen.ListDebugSessionsResponseObject, error) {
	if s.debugOff() {
		return gen.ListDebugSessionsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	rows, err := s.opts.Debug.Sessions(ctx, 200)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.ListDebugSessionsdefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	out := gen.DebugSessionList{Items: make([]gen.DebugSession, 0, len(rows))}
	for _, d := range rows {
		out.Items = append(out.Items, genDebugSession(d))
	}
	return gen.ListDebugSessions200JSONResponse(out), nil
}

// GetDebugSession implements GET /api/v1/debug-sessions/{sessionId}.
func (s *server) GetDebugSession(ctx context.Context, r gen.GetDebugSessionRequestObject) (gen.GetDebugSessionResponseObject, error) {
	if s.debugOff() {
		return gen.GetDebugSessiondefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	d, err := s.opts.Debug.Session(ctx, r.SessionId)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.GetDebugSessiondefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	return gen.GetDebugSession200JSONResponse(genDebugSession(d)), nil
}

// ExtendDebugSession implements POST /api/v1/debug-sessions/{sessionId}/extend.
func (s *server) ExtendDebugSession(ctx context.Context, r gen.ExtendDebugSessionRequestObject) (gen.ExtendDebugSessionResponseObject, error) {
	if s.debugOff() {
		return gen.ExtendDebugSessiondefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	d, err := s.opts.Debug.Extend(ctx, s.principal(ctx), r.SessionId)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.ExtendDebugSessiondefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	return gen.ExtendDebugSession200JSONResponse(genDebugSession(d)), nil
}

// EndDebugSession implements POST /api/v1/debug-sessions/{sessionId}/end.
func (s *server) EndDebugSession(ctx context.Context, r gen.EndDebugSessionRequestObject) (gen.EndDebugSessionResponseObject, error) {
	if s.debugOff() {
		return gen.EndDebugSessiondefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	d, err := s.opts.Debug.End(ctx, s.principal(ctx), r.SessionId)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.EndDebugSessiondefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	return gen.EndDebugSession200JSONResponse(genDebugSession(d)), nil
}

// GetDebugTranscript implements GET /api/v1/debug-sessions/{sessionId}/transcript.
func (s *server) GetDebugTranscript(ctx context.Context, r gen.GetDebugTranscriptRequestObject) (gen.GetDebugTranscriptResponseObject, error) {
	if s.debugOff() {
		return gen.GetDebugTranscriptdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	t, ok, err := s.opts.Debug.Transcript(ctx, s.principal(ctx), r.SessionId)
	if err != nil {
		st, body := s.debugFailure(err)
		return gen.GetDebugTranscriptdefaultJSONResponse{StatusCode: st, Body: body}, nil
	}
	if !ok {
		return gen.GetDebugTranscriptdefaultJSONResponse{StatusCode: http.StatusNotFound, Body: errEnvelope(CodeNotFound, "This session has no transcript.")}, nil
	}
	return gen.GetDebugTranscript200JSONResponse(gen.DebugTranscript{SessionId: t.SessionID, Text: t.Text, Truncated: t.Truncated,
		ExpiresAt: t.ExpiresAt, Note: debugTranscriptNote}), nil
}
