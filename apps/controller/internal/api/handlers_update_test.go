package api

import (
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

type fakeUpdate struct {
	applied   []update.Kind
	confirmed []bool
	jobs      []update.Job
	stage     *update.Stage
}

func (f *fakeUpdate) Status(context.Context) update.Status {
	return update.Status{Current: "1.4.0", Latest: "1.5.0", Available: true, State: update.StateIdle, AutoUpdate: true, RunningJobs: f.jobs, DrainTimeoutMinutes: 60, Stage: f.stage}
}
func (f *fakeUpdate) Check(ctx context.Context) (update.Status, error) { return f.Status(ctx), nil }
func (f *fakeUpdate) Apply(ctx context.Context, k update.Kind, confirmed bool, _ string) (update.Status, error) {
	if !confirmed {
		return update.Status{}, &update.NeedsConfirmation{Kind: k, Jobs: f.jobs}
	}
	f.applied, f.confirmed = append(f.applied, k), append(f.confirmed, confirmed)
	st := f.Status(ctx)
	st.State = update.StateWaitingForJobs
	return st, nil
}

func updateMux(f UpdateService) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "1.4.0", DB: okPinger{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Update: f, Authn: asRole(auth.RoleOwner)})
	return mux
}

func doReq(mux *http.ServeMux, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return rec
}

func TestUpdateStatusRoute(t *testing.T) {
	rec := doReq(updateMux(&fakeUpdate{jobs: []update.Job{{Repository: "a/b", Workflow: "ci", RunID: 9, Job: "test"}}}), "GET", "/api/v1/update")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["current_version"] != "1.4.0" || got["latest_version"] != "1.5.0" || got["update_available"] != true || got["auto_update"] != true {
		t.Fatalf("%v", got)
	}
	if jobs, _ := got["running_jobs"].([]any); len(jobs) != 1 {
		t.Fatalf("%v", got["running_jobs"])
	}
}

// Tray R5.16: restart and update refuse without confirmed=true and answer with the running jobs.
func TestRestartAndUpdateRefusedWithoutConfirmed(t *testing.T) {
	f := &fakeUpdate{jobs: []update.Job{{Repository: "a/b", Workflow: "ci", RunID: 9, Job: "test"}}}
	mux := updateMux(f)
	for _, target := range []string{"/api/v1/update/apply", "/api/v1/service/restart", "/api/v1/update/apply?confirmed=false"} {
		rec := doReq(mux, "POST", target)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: %d %s", target, rec.Code, rec.Body)
		}
		assertEnvelope(t, rec, CodeNeedsConfirmation)
		if !strings.Contains(rec.Body.String(), "a/b ci, run 9, job test") {
			t.Fatalf("%s: the running job must be named: %s", target, rec.Body)
		}
	}
	if len(f.applied) != 0 {
		t.Fatal("a refused request changed something")
	}
}

func TestRestartAndUpdateWithConfirmed(t *testing.T) {
	f := &fakeUpdate{}
	mux := updateMux(f)
	if rec := doReq(mux, "POST", "/api/v1/update/apply?confirmed=true"); rec.Code != 202 || !strings.Contains(rec.Body.String(), "waiting_for_jobs") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := doReq(mux, "POST", "/api/v1/service/restart?confirmed=true"); rec.Code != 202 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(f.applied) != 2 || f.applied[0] != update.KindUpdate || f.applied[1] != update.KindRestart {
		t.Fatalf("%v", f.applied)
	}
}

func TestUpdateRoutesUnbuiltWithoutService(t *testing.T) {
	mux := updateMux(nil)
	for _, c := range [][2]string{{"GET", "/api/v1/update"}, {"POST", "/api/v1/update/check"}, {"POST", "/api/v1/update/apply?confirmed=true"}} {
		if rec := doReq(mux, c[0], c[1]); rec.Code != http.StatusNotImplemented {
			t.Fatalf("%v: %d", c, rec.Code)
		}
	}
}

func TestSettingsAutoUpdateDefaultOnAndToggle(t *testing.T) {
	e := newOpsEnv(t)
	if rec := e.do("GET", "/api/v1/settings", ""); !strings.Contains(rec.Body.String(), `"auto_update":true`) {
		t.Fatalf("default must be on: %s", rec.Body)
	}
	if rec := e.do("PATCH", "/api/v1/settings", `{"auto_update":false}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"auto_update":false`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/v1/settings", ""); !strings.Contains(rec.Body.String(), `"auto_update":false`) {
		t.Fatalf("not saved: %s", rec.Body)
	}
}

// TRAY4: the live stage rides on the status; a stage without a number shows none.
func TestUpdateStatusCarriesTheStage(t *testing.T) {
	read := func(st *update.Stage) map[string]any {
		rec := doReq(updateMux(&fakeUpdate{stage: st}), "GET", "/api/v1/update")
		var got map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got
	}
	if _, has := read(nil)["stage"]; has {
		t.Fatal("no stage file, no stage")
	}
	dl, _ := read(&update.Stage{Name: update.StageDownloading, Percent: 40, Version: "1.5.0"})["stage"].(map[string]any)
	if dl["name"] != "downloading" || dl["percent"] != float64(40) || dl["version"] != "1.5.0" {
		t.Fatalf("%v", dl)
	}
	wait, _ := read(&update.Stage{Name: update.StageWaitingForJobs, Percent: -1})["stage"].(map[string]any)
	if _, has := wait["percent"]; has {
		t.Fatalf("waiting has no percent: %v", wait)
	}
	bad, _ := read(&update.Stage{Name: update.StageFailed, Percent: -1, Error: "boom", LogPath: "C:/x/msi.log"})["stage"].(map[string]any)
	if bad["error"] != "boom" || bad["log_path"] != "C:/x/msi.log" {
		t.Fatalf("%v", bad)
	}
}
