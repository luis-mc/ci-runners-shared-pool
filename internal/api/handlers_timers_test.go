package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/timers"
)

type timerStub struct {
	set     timers.Settings
	jobErr  error
	deadine time.Time
}

func (t *timerStub) Get(context.Context) timers.Settings { return t.set }
func (t *timerStub) Check(_ context.Context, in timers.Input) (timers.Settings, []timers.Problem) {
	return timers.Check(t.set, in)
}
func (t *timerStub) Save(_ context.Context, in timers.Input, _ string) (timers.Settings, []timers.Problem, error) {
	next, p := timers.Check(t.set, in)
	if len(p) == 0 {
		t.set = next
	}
	return next, p, nil
}
func (t *timerStub) view() timers.JobView {
	return timers.JobView{Key: timers.JobKey{Repo: "o/r", RunID: 9, Attempt: 1, JobID: 5}, State: timers.StateGrace,
		Limit: 30 * time.Minute, Deadline: t.deadine, Text: "Job timer ran out at 30:00; stopping in 30 s", CanStopNow: true, CanExtend: true}
}
func (t *timerStub) Job(_ context.Context, id int64) (timers.JobView, bool) { return t.view(), id == 5 }
func (t *timerStub) Run(_ context.Context, id int64, _ int) (timers.RunView, bool) {
	return timers.RunView{Key: timers.RunKey{Repo: "o/r", RunID: id, Attempt: 1}, State: timers.StateGrace, Text: "Run timer ran out at 45:00; cancelling in 30 s", CanCancelNow: true}, id == 9
}
func (t *timerStub) StopNow(context.Context, int64, string) (timers.JobView, error) {
	return t.view(), t.jobErr
}
func (t *timerStub) Extend(context.Context, int64, string) (timers.JobView, error) {
	return t.view(), t.jobErr
}
func (t *timerStub) CancelNow(context.Context, int64, int, string) (timers.RunView, error) {
	return timers.RunView{}, t.jobErr
}
func (t *timerStub) ExtendRun(context.Context, int64, int, string) (timers.RunView, error) {
	return timers.RunView{}, t.jobErr
}

func timerMux(t *testing.T, role auth.Role, svc TimerService) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Timers: svc, Authn: asRole(role)})
	return mux
}

// scheduling R5.104: the settings, the check while typing and the save agree.
func TestTimerSettingsRoutes(t *testing.T) {
	stub := &timerStub{set: timers.Defaults()}
	mux := timerMux(t, auth.RoleOwner, stub)
	rec := do(mux, http.MethodGet, "/api/v1/timer-settings", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"job_minutes":30`) || !strings.Contains(rec.Body.String(), `"grace_seconds":30`) {
		t.Fatalf("get %d %s", rec.Code, rec.Body)
	}
	rec = do(mux, http.MethodPost, "/api/v1/timer-settings/check", `{"job_minutes":50}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), timers.MsgJobRun) {
		t.Fatalf("check %d %s", rec.Code, rec.Body)
	}
	rec = do(mux, http.MethodPut, "/api/v1/timer-settings", `{"job_minutes":0}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), timers.MsgWhole) || !strings.Contains(rec.Body.String(), `"job_minutes"`) {
		t.Fatalf("bad save %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodPut, "/api/v1/timer-settings", `{"job_minutes":20,"run_minutes":40}`); rec.Code != 200 || stub.set.JobMin != 20 {
		t.Fatalf("save %d %s", rec.Code, rec.Body)
	}
	if rec = do(timerMux(t, auth.RoleViewer, stub), http.MethodPut, "/api/v1/timer-settings", `{"job_minutes":10}`); rec.Code != 403 {
		t.Fatalf("viewer write %d", rec.Code)
	}
}

// R5.100, R5.101: the job timer exposes the deadline; the actions map their refusals.
func TestTimerActionRoutes(t *testing.T) {
	stub := &timerStub{set: timers.Defaults(), deadine: time.Date(2026, 11, 22, 10, 30, 30, 0, time.UTC)}
	mux := timerMux(t, auth.RoleOwner, stub)
	rec := do(mux, http.MethodGet, "/api/v1/jobs/5/timer", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"deadline":"2026-11-22T10:30:30Z"`) || !strings.Contains(rec.Body.String(), "stopping in 30 s") {
		t.Fatalf("job %d %s", rec.Code, rec.Body)
	}
	if rec = do(mux, http.MethodGet, "/api/v1/jobs/6/timer", ""); rec.Code != 404 {
		t.Fatalf("unknown job %d", rec.Code)
	}
	if rec = do(mux, http.MethodPost, "/api/v1/jobs/5/timer/stop-now", ""); rec.Code != 200 {
		t.Fatalf("stop now %d %s", rec.Code, rec.Body)
	}
	stub.jobErr = timers.ErrExtendedOnce
	if rec = do(mux, http.MethodPost, "/api/v1/jobs/5/timer/extend", ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "extended once already") {
		t.Fatalf("extend twice %d %s", rec.Code, rec.Body)
	}
	stub.jobErr = timers.ErrCovered
	if rec = do(mux, http.MethodPost, "/api/v1/jobs/5/timer/stop-now", ""); rec.Code != 409 {
		t.Fatalf("covered %d", rec.Code)
	}
	stub.jobErr = timers.ErrNotInGrace
	if rec = do(mux, http.MethodPost, "/api/v1/runs/9/timer/cancel-now?attempt=1", ""); rec.Code != 409 {
		t.Fatalf("cancel early %d", rec.Code)
	}
	if rec = do(mux, http.MethodGet, "/api/v1/runs/9/timer", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "cancelling in 30 s") {
		t.Fatalf("run %d %s", rec.Code, rec.Body)
	}
	if rec = do(timerMux(t, auth.RoleViewer, stub), http.MethodPost, "/api/v1/runs/9/timer/extend", ""); rec.Code != 403 {
		t.Fatalf("viewer action %d", rec.Code)
	}
}

func TestTimerRoutesUnbuilt(t *testing.T) {
	if rec := do(timerMux(t, auth.RoleOwner, nil), http.MethodGet, "/api/v1/timer-settings", ""); rec.Code != 501 {
		t.Fatalf("unbuilt %d", rec.Code)
	}
}
