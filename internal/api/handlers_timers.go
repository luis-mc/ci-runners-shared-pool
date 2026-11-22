package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/timers"
)

// TimerService is what the timer routes need (scheduling R5.99 to R5.106).
// *timers.Service implements it.
type TimerService interface {
	Get(ctx context.Context) timers.Settings
	Check(ctx context.Context, in timers.Input) (timers.Settings, []timers.Problem)
	Save(ctx context.Context, in timers.Input, by string) (timers.Settings, []timers.Problem, error)
	Job(ctx context.Context, id int64) (timers.JobView, bool)
	Run(ctx context.Context, runID int64, attempt int) (timers.RunView, bool)
	StopNow(ctx context.Context, id int64, actor string) (timers.JobView, error)
	Extend(ctx context.Context, id int64, actor string) (timers.JobView, error)
	CancelNow(ctx context.Context, runID int64, attempt int, actor string) (timers.RunView, error)
	ExtendRun(ctx context.Context, runID int64, attempt int, actor string) (timers.RunView, error)
}

func genTimerSettings(s timers.Settings) gen.TimerSettings {
	return gen.TimerSettings{JobMinutes: s.JobMin, RunMinutes: s.RunMin, HoldMinutes: s.HoldMin, TimeoutMinutes: s.TimeoutMin,
		QueuedMinutes: s.QueuedMin, StartupMinutes: s.StartupMin,
		GraceSeconds: int(timers.Grace / time.Second), ExtendMinutes: int(timers.ExtendBy / time.Minute)}
}

func timerInput(b gen.TimerSettingsInput) timers.Input {
	return timers.Input{JobMin: b.JobMinutes, RunMin: b.RunMinutes, HoldMin: b.HoldMinutes,
		TimeoutMin: b.TimeoutMinutes, QueuedMin: b.QueuedMinutes, StartupMin: b.StartupMinutes}
}

func timerProblems(p []timers.Problem) []gen.TimerSettingsProblem {
	out := make([]gen.TimerSettingsProblem, 0, len(p))
	for _, x := range p {
		out = append(out, gen.TimerSettingsProblem{Field: gen.TimerSettingsProblemField(x.Field), Message: x.Message})
	}
	return out
}

func timerSecs(d time.Duration) *int {
	n := int(d / time.Second)
	return &n
}

func genJobTimer(v timers.JobView) gen.JobTimer {
	attempt, run := v.Key.Attempt, v.Key.RunID
	return gen.JobTimer{JobId: v.Key.JobID, Repository: optStr(v.Key.Repo), RunId: &run, RunAttempt: &attempt,
		State: gen.JobTimerState(v.State), StartedAt: optTime(v.StartedAt), LimitSeconds: timerSecs(v.Limit), ElapsedSeconds: timerSecs(v.Elapsed),
		Deadline: optTime(v.Deadline), Text: v.Text, Note: optStr(v.Note), CanStopNow: v.CanStopNow, CanExtend: v.CanExtend,
		Extended: v.Extended, CoveredByRun: v.CoveredByRun}
}

func genRunTimer(v timers.RunView) gen.RunTimer {
	attempt := v.Key.Attempt
	return gen.RunTimer{RunId: v.Key.RunID, Repository: optStr(v.Key.Repo), RunAttempt: &attempt,
		State: gen.RunTimerState(v.State), StartedAt: optTime(v.StartedAt), LimitSeconds: timerSecs(v.Limit), ElapsedSeconds: timerSecs(v.Elapsed),
		Deadline: optTime(v.Deadline), Text: v.Text, Note: optStr(v.Note), CanCancelNow: v.CanCancelNow, CanExtend: v.CanExtend, Extended: v.Extended}
}

func timerEnvelope(code, msg string) gen.ErrorEnvelope {
	return gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
}

// timerFailure maps an action's error to a status, a code and a sentence in the
// owner's words.
func timerFailure(err error) (int, gen.ErrorEnvelope) {
	switch {
	case errors.Is(err, timers.ErrUnknown):
		return http.StatusNotFound, timerEnvelope(CodeNotFound, "There is no timer for that job or run. It has ended or never started.")
	case errors.Is(err, timers.ErrNotInGrace):
		return http.StatusConflict, timerEnvelope(CodeConflict, "The timer has not run out, so there is nothing to do yet.")
	case errors.Is(err, timers.ErrExtendedOnce):
		return http.StatusConflict, timerEnvelope(CodeConflict, "This timer was extended once already.")
	case errors.Is(err, timers.ErrCovered):
		return http.StatusConflict, timerEnvelope(CodeConflict, "The run timer ran out too. Crsp cancels the run, not this job alone.")
	}
	return http.StatusInternalServerError, timerEnvelope(CodeInternal, "internal error")
}

func (s *server) timersOff() bool { return s.opts.Timers == nil }

// GetTimerSettings implements GET /api/v1/timer-settings.
func (s *server) GetTimerSettings(ctx context.Context, _ gen.GetTimerSettingsRequestObject) (gen.GetTimerSettingsResponseObject, error) {
	if s.timersOff() {
		return gen.GetTimerSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	return gen.GetTimerSettings200JSONResponse(genTimerSettings(s.opts.Timers.Get(ctx))), nil
}

// PutTimerSettings implements PUT /api/v1/timer-settings.
func (s *server) PutTimerSettings(ctx context.Context, r gen.PutTimerSettingsRequestObject) (gen.PutTimerSettingsResponseObject, error) {
	if s.timersOff() {
		return gen.PutTimerSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return gen.PutTimerSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: timerEnvelope(CodeBadRequest, timers.MsgWhole)}, nil
	}
	next, problems, err := s.opts.Timers.Save(ctx, timerInput(*r.Body), s.actor(ctx))
	switch {
	case err != nil:
		return gen.PutTimerSettingsdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: timerEnvelope(CodeInternal, "internal error")}, nil
	case len(problems) > 0:
		env := timerEnvelope(CodeBadRequest, problems[0].Message)
		details := make([]gen.ErrorDetail, 0, len(problems))
		for _, p := range problems {
			f := p.Field
			details = append(details, gen.ErrorDetail{Kind: "field", Id: &f, Text: p.Message})
		}
		env.Error.Details = &details
		return gen.PutTimerSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: env}, nil
	}
	return gen.PutTimerSettings200JSONResponse(genTimerSettings(next)), nil
}

// CheckTimerSettings implements POST /api/v1/timer-settings/check.
func (s *server) CheckTimerSettings(ctx context.Context, r gen.CheckTimerSettingsRequestObject) (gen.CheckTimerSettingsResponseObject, error) {
	if s.timersOff() {
		return gen.CheckTimerSettingsdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if r.Body == nil {
		return gen.CheckTimerSettingsdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: timerEnvelope(CodeBadRequest, timers.MsgWhole)}, nil
	}
	next, problems := s.opts.Timers.Check(ctx, timerInput(*r.Body))
	return gen.CheckTimerSettings200JSONResponse(gen.TimerSettingsCheck{Ok: len(problems) == 0, Problems: timerProblems(problems), Settings: genTimerSettings(next)}), nil
}

// GetJobTimer implements GET /api/v1/jobs/{jobId}/timer.
func (s *server) GetJobTimer(ctx context.Context, r gen.GetJobTimerRequestObject) (gen.GetJobTimerResponseObject, error) {
	if s.timersOff() {
		return gen.GetJobTimerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, ok := s.opts.Timers.Job(ctx, r.JobId)
	if !ok {
		code, env := timerFailure(timers.ErrUnknown)
		return gen.GetJobTimerdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.GetJobTimer200JSONResponse(genJobTimer(v)), nil
}

// StopJobNow implements POST /api/v1/jobs/{jobId}/timer/stop-now.
func (s *server) StopJobNow(ctx context.Context, r gen.StopJobNowRequestObject) (gen.StopJobNowResponseObject, error) {
	if s.timersOff() {
		return gen.StopJobNowdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Timers.StopNow(ctx, r.JobId, s.actor(ctx))
	if err != nil {
		code, env := timerFailure(err)
		return gen.StopJobNowdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.StopJobNow200JSONResponse(genJobTimer(v)), nil
}

// ExtendJobTimer implements POST /api/v1/jobs/{jobId}/timer/extend.
func (s *server) ExtendJobTimer(ctx context.Context, r gen.ExtendJobTimerRequestObject) (gen.ExtendJobTimerResponseObject, error) {
	if s.timersOff() {
		return gen.ExtendJobTimerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Timers.Extend(ctx, r.JobId, s.actor(ctx))
	if err != nil {
		code, env := timerFailure(err)
		return gen.ExtendJobTimerdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.ExtendJobTimer200JSONResponse(genJobTimer(v)), nil
}

func attemptOf(a *gen.TimerAttempt) int {
	if a == nil {
		return 0
	}
	return int(*a)
}

// GetRunTimer implements GET /api/v1/runs/{runId}/timer.
func (s *server) GetRunTimer(ctx context.Context, r gen.GetRunTimerRequestObject) (gen.GetRunTimerResponseObject, error) {
	if s.timersOff() {
		return gen.GetRunTimerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, ok := s.opts.Timers.Run(ctx, r.RunId, attemptOf(r.Params.Attempt))
	if !ok {
		code, env := timerFailure(timers.ErrUnknown)
		return gen.GetRunTimerdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.GetRunTimer200JSONResponse(genRunTimer(v)), nil
}

// CancelRunNow implements POST /api/v1/runs/{runId}/timer/cancel-now.
func (s *server) CancelRunNow(ctx context.Context, r gen.CancelRunNowRequestObject) (gen.CancelRunNowResponseObject, error) {
	if s.timersOff() {
		return gen.CancelRunNowdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Timers.CancelNow(ctx, r.RunId, attemptOf(r.Params.Attempt), s.actor(ctx))
	if err != nil {
		code, env := timerFailure(err)
		return gen.CancelRunNowdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.CancelRunNow200JSONResponse(genRunTimer(v)), nil
}

// ExtendRunTimer implements POST /api/v1/runs/{runId}/timer/extend.
func (s *server) ExtendRunTimer(ctx context.Context, r gen.ExtendRunTimerRequestObject) (gen.ExtendRunTimerResponseObject, error) {
	if s.timersOff() {
		return gen.ExtendRunTimerdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	v, err := s.opts.Timers.ExtendRun(ctx, r.RunId, attemptOf(r.Params.Attempt), s.actor(ctx))
	if err != nil {
		code, env := timerFailure(err)
		return gen.ExtendRunTimerdefaultJSONResponse{StatusCode: code, Body: env}, nil
	}
	return gen.ExtendRunTimer200JSONResponse(genRunTimer(v)), nil
}
