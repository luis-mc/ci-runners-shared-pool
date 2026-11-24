package api

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
)

type fakeRunners struct {
	recs      map[string]runners.Record
	refuse    string
	removed   bool
	started   []string
	cleared   []string
	lastActor string
	lastAdd   runners.AddRequest
	views     map[string]runners.View // overrides of the view fields of a record
	stray     []runners.StrayRegistration
}

func (f *fakeRunners) ListView(_ context.Context, fl runners.Filter) ([]runners.View, error) {
	var out []runners.View
	for _, r := range f.recs {
		if fl.Managed != nil && *fl.Managed != r.Managed {
			continue
		}
		out = append(out, f.view(r))
	}
	return out, nil
}

// view is the record with the machine's name, as the service gives it. A
// stored failure carries its reason.
func (f *fakeRunners) view(r runners.Record) runners.View {
	v := runners.View{Record: r, State: r.State, Reason: r.Reason, MachineName: "GameRoomPC"}
	if o, ok := f.views[r.ID]; ok {
		v.Serving, v.Registrations = o.Serving, o.Registrations
	}
	return v
}
func (f *fakeRunners) GetView(_ context.Context, id string) (runners.View, error) {
	if r, ok := f.recs[id]; ok {
		return f.view(r), nil
	}
	return runners.View{}, runners.ErrNotFound
}
func (f *fakeRunners) UpdateSettings(_ context.Context, id string, keep *bool, actor string) (runners.View, error) {
	r, ok := f.recs[id]
	if !ok {
		return runners.View{}, runners.ErrNotFound
	}
	if r.Kind != "persistent" {
		return runners.View{}, &runners.RefusalError{Reason: "runner " + r.Name + " is ephemeral"}
	}
	f.lastActor = actor
	if keep != nil {
		r.KeepDependencies = *keep
	}
	f.recs[id] = r
	return f.view(r), nil
}
func (f *fakeRunners) RequestClearCache(_ context.Context, id, actor string) (runners.View, error) {
	r, ok := f.recs[id]
	if !ok {
		return runners.View{}, runners.ErrNotFound
	}
	if r.Kind != "persistent" {
		return runners.View{}, &runners.RefusalError{Reason: "runner " + r.Name + " is ephemeral"}
	}
	f.lastActor = actor
	f.cleared = append(f.cleared, id)
	return f.view(r), nil
}
func (f *fakeRunners) PlanAdd(_ context.Context, req runners.AddRequest, actor string) (runners.Plan, error) {
	f.lastActor, f.lastAdd = actor, req
	if f.refuse != "" {
		return runners.Plan{}, &runners.RefusalError{Reason: f.refuse}
	}
	return runners.Plan{ID: "p1", Area: "runners", State: "planned", CreatedBy: actor, CreatedAt: time.Unix(0, 0).UTC(),
		Steps: []runners.Step{{ID: "s1", Action: "add_runner", Text: "Add " + req.OS, State: "planned"}}}, nil
}
func (f *fakeRunners) PlanReplacement(context.Context, string, string) (runners.Plan, error) {
	return runners.Plan{ID: "p2", Area: "runners", State: "planned", Steps: []runners.Step{
		{ID: "s2", Action: "stop_old_service", NeedsYes: true, WaitsFor: "s1", State: "planned", Text: "x"}}}, nil
}
func (f *fakeRunners) GetPlan(context.Context, string) (runners.Plan, error) {
	return runners.Plan{}, runners.ErrNotFound
}
func (f *fakeRunners) StopPlan(_ context.Context, id, _ string) (runners.Plan, error) {
	if id != "p-applying" {
		return runners.Plan{ID: id}, &runners.RefusalError{Reason: "plan " + id + " is done, so there is nothing to stop"}
	}
	return runners.Plan{ID: id, Area: "runners", State: "applying"}, nil
}
func (f *fakeRunners) StartApply(_ context.Context, id string, approved []string, _ string) (runners.Plan, error) {
	f.started = approved
	return runners.Plan{ID: id, Area: "runners", State: "applying"}, nil
}
func (f *fakeRunners) BeginRemove(_ context.Context, id string, confirm bool, _ string) (runners.Record, func(context.Context) error, error) {
	r, ok := f.recs[id]
	if !ok {
		return r, nil, runners.ErrNotFound
	}
	if !confirm {
		return r, nil, &runners.NeedsConfirmation{Items: []runners.ConfirmItem{{Kind: "service", Text: "The service x"}, {Kind: "job", Text: "Waits for job demo/ci#1"}}}
	}
	r.State = runners.Draining
	return r, func(context.Context) error { f.removed = true; return nil }, nil
}
func (f *fakeRunners) DeleteOfflineRegistration(_ context.Context, id string, confirm bool, actor string) (runners.Record, error) {
	r, ok := f.recs[id]
	if !ok {
		return r, runners.ErrNotFound
	}
	if !confirm {
		return r, &runners.NeedsConfirmation{Items: []runners.ConfirmItem{{Kind: "registration", Text: "The registration of offline runner " + r.Name + " on GitHub"}}}
	}
	f.removed = true
	f.lastActor = actor
	return r, nil
}
func (f *fakeRunners) StartRemove(ctx context.Context, finish func(context.Context) error) {
	_ = finish(ctx)
}

func runnersMux(svc RunnersService) *http.ServeMux {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runners: svc, Authn: asRole(auth.RoleOwner)})
	return mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(ContextWithActor(context.Background(), "luis-mc"), method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func sample() *fakeRunners {
	return &fakeRunners{recs: map[string]runners.Record{
		"r1": {ID: "r1", Name: "box-1", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent", Backend: "persistent",
			Isolation: "persistent", Labels: []string{"self-hosted", "linux", "x64"}, Scope: runners.Scope{Kind: "repository", RepositoryID: 9},
			State: runners.Idle, Managed: true, RunnerVersion: "2.330.0"},
		"r2": {ID: "r2", Name: "hand", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent", Backend: "persistent",
			Isolation: "persistent", Scope: runners.Scope{Kind: "repository"}, State: runners.Offline, Managed: false},
	}}
}

// R5.7: the response names the isolation and marks a persistent runner as
// keeping state; R5.5: a runner CRSP did not make shows as not managed.
func TestIsolationInResponse(t *testing.T) {
	rec := do(runnersMux(sample()), http.MethodGet, "/api/v1/runners", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Items []struct {
			ID         string
			Isolation  string
			KeepsState bool `json:"keeps_state"`
			Managed    bool
			Labels     []string
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 {
		t.Fatalf("%s", rec.Body)
	}
	for _, it := range out.Items {
		if it.Isolation != "persistent" || !it.KeepsState || it.Labels == nil {
			t.Errorf("%+v", it)
		}
	}
	rec = do(runnersMux(sample()), http.MethodGet, "/api/v1/runners?managed=false", "")
	if !strings.Contains(rec.Body.String(), `"managed":false`) || strings.Contains(rec.Body.String(), `"id":"r1"`) {
		t.Errorf("managed filter: %s", rec.Body)
	}
	if rec = do(runnersMux(sample()), http.MethodGet, "/api/v1/runners/r1", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"keeps_state":true`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec = do(runnersMux(sample()), http.MethodGet, "/api/v1/runners/nope", ""); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
}

// A failed runner carries its whole reason; any other state has none.
func TestFailedReasonInResponse(t *testing.T) {
	st := sample()
	r := st.recs["r1"]
	r.State, r.FailedReason = runners.Failed, "register service: the account has no right to log on as a service"
	st.recs["r1"] = r
	rec := do(runnersMux(st), http.MethodGet, "/api/v1/runners/r1", "")
	if !strings.Contains(rec.Body.String(), `"failed_reason":"register service: the account has no right to log on as a service"`) {
		t.Errorf("%s", rec.Body)
	}
	rec = do(runnersMux(sample()), http.MethodGet, "/api/v1/runners/r1", "")
	if strings.Contains(rec.Body.String(), "failed_reason") {
		t.Errorf("an idle runner has no reason: %s", rec.Body)
	}
}

// R5.1: a refused plan answers with the reason.
func TestPlanRefusalCarriesReason(t *testing.T) {
	f := sample()
	f.refuse = "machine mac-1: no allowed backend for Windows"
	body := `{"os":"windows","kind":"persistent","machine_id":"m1","count":1,"scope":{"kind":"repository","repository_id":9}}`
	rec := do(runnersMux(f), http.MethodPost, "/api/v1/runner-plans", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeConflict)
	if !strings.Contains(rec.Body.String(), "no allowed backend for Windows") {
		t.Errorf("%s", rec.Body)
	}
	f.refuse = ""
	rec = do(runnersMux(f), http.MethodPost, "/api/v1/runner-plans", body)
	if rec.Code != http.StatusCreated || f.lastActor != "luis-mc" || !strings.Contains(rec.Body.String(), `"add_runner"`) {
		t.Errorf("%d %s actor=%s", rec.Code, rec.Body, f.lastActor)
	}
}

// R5.4: without confirm, 409 needs_confirmation names what goes and the job.
func TestDeleteNeedsConfirmation(t *testing.T) {
	f := sample()
	rec := do(runnersMux(f), http.MethodDelete, "/api/v1/runners/r1", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeNeedsConfirmation)
	if !strings.Contains(rec.Body.String(), "demo/ci#1") || f.removed {
		t.Errorf("%s removed=%v", rec.Body, f.removed)
	}
	rec = do(runnersMux(f), http.MethodDelete, "/api/v1/runners/r1?confirm=true", "")
	if rec.Code != http.StatusAccepted || !f.removed || !strings.Contains(rec.Body.String(), `"state":"draining"`) {
		t.Errorf("%d %s removed=%v", rec.Code, rec.Body, f.removed)
	}
}

func TestApplyAndReplace(t *testing.T) {
	f := sample()
	rec := do(runnersMux(f), http.MethodPost, "/api/v1/runner-plans/p1/apply", `{"approved_steps":["s2","s3"]}`)
	if rec.Code != http.StatusAccepted || strings.Join(f.started, ",") != "s2,s3" {
		t.Errorf("%d %s %v", rec.Code, rec.Body, f.started)
	}
	rec = do(runnersMux(f), http.MethodPost, "/api/v1/runners/r2/replace", "")
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"needs_yes":true`) || !strings.Contains(rec.Body.String(), `"waits_for":"s1"`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec = do(runnersMux(f), http.MethodGet, "/api/v1/runner-plans/zzz", ""); rec.Code != 404 {
		t.Errorf("%d", rec.Code)
	}
}

// Without a service the routes still answer with the not-implemented envelope.
func TestRunnersNotWiredAnswers501(t *testing.T) {
	rec := do(runnersMux(nil), http.MethodGet, "/api/v1/runners", "")
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("%d", rec.Code)
	}
	assertEnvelope(t, rec, CodeNotImplemented)
}

// Stop (web-console 11): 202 with the plan while applying, 409 when it is not.
func TestStopRunnerPlan(t *testing.T) {
	mux := runnersMux(sample())
	if rec := do(mux, "POST", "/api/v1/runner-plans/p-applying/stop", ""); rec.Code != 202 || !strings.Contains(rec.Body.String(), `"applying"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec := do(mux, "POST", "/api/v1/runner-plans/p-done/stop", "")
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "nothing to stop") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// R5.33: deleting an offline registration asks first and names the runner; a yes deletes it.
func TestDeleteRunnerRegistrationNeedsAYesThatNamesTheRunner(t *testing.T) {
	f := sample()
	rec := do(runnersMux(f), http.MethodDelete, "/api/v1/runners/r1/registration", "")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), CodeNeedsConfirmation) || !strings.Contains(rec.Body.String(), "offline runner") || f.removed {
		t.Fatalf("%d %s removed=%v", rec.Code, rec.Body, f.removed)
	}
	rec = do(runnersMux(f), http.MethodDelete, "/api/v1/runners/r1/registration?confirm=true", "")
	if rec.Code != http.StatusOK || !f.removed {
		t.Fatalf("%d %s removed=%v", rec.Code, rec.Body, f.removed)
	}
	if rec = do(runnersMux(f), http.MethodDelete, "/api/v1/runners/nope/registration?confirm=true", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// UX-1, UX-19: a runner carries its machine's name, and a failed runner its reason.
func TestRunnerCarriesMachineNameAndReason(t *testing.T) {
	f := sample()
	r := f.recs["r1"]
	r.State, r.Reason = runners.Failed, "the service did not start"
	f.recs["r1"] = r
	rec := do(runnersMux(f), http.MethodGet, "/api/v1/runners/r1", "")
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		MachineID   string `json:"machine_id"`
		MachineName string `json:"machine_name"`
		State       string
		Reason      string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.MachineName != "GameRoomPC" || out.MachineID != "m1" || out.State != "failed" || out.Reason != "the service did not start" {
		t.Errorf("%+v", out)
	}
	if rec = do(runnersMux(sample()), http.MethodGet, "/api/v1/runners/r1", ""); strings.Contains(rec.Body.String(), `"reason"`) {
		t.Errorf("no reason when there is nothing to say: %s", rec.Body)
	}
}

func (f *fakeRunners) RemoveStrayRegistration(_ context.Context, in runners.StrayRegistration, prefix string, confirm bool, _ string) error {
	if !confirm {
		return &runners.NeedsConfirmation{Items: []runners.ConfirmItem{{Kind: "registration", Text: "The registration of runner " + in.Name}}}
	}
	if prefix != "crsp-" {
		return &runners.RefusalError{Reason: "prefix"}
	}
	f.stray = append(f.stray, in)
	return nil
}

func (f *fakeRunners) RemoveRecordWithoutRegistration(ctx context.Context, id string, confirm bool, actor string) (runners.Record, error) {
	return f.DeleteOfflineRegistration(ctx, id, confirm, actor)
}

// Doctor R5.50: the Remove button of a half-registered runner asks first, then
// passes what the finding carried to the service.
func TestRemoveRegistrationFixes(t *testing.T) {
	f := sample()
	body := `{"action":"remove_github_runner","args":{"github_runner_id":555,"name":"crsp-stray","installation_id":7,"owner":"o","repo":"r"}}`
	rec := do(runnersMux(f), http.MethodPost, "/api/v1/doctor/registration-fixes", body)
	if rec.Code != http.StatusConflict {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeNeedsConfirmation)
	rec = do(runnersMux(f), http.MethodPost, "/api/v1/doctor/registration-fixes?confirm=true", body)
	if rec.Code != http.StatusOK || len(f.stray) != 1 || f.stray[0].GitHubID != 555 || f.stray[0].Scope.Repo != "r" || f.stray[0].InstallationID != 7 {
		t.Fatalf("%d %s %+v", rec.Code, rec.Body, f.stray)
	}
	rec = do(runnersMux(f), http.MethodPost, "/api/v1/doctor/registration-fixes?confirm=true", `{"action":"remove_runner_record","args":{"runner_id":"r1"}}`)
	if rec.Code != http.StatusOK || !f.removed {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

// LOOK66: a runner with a declared size class carries it; one without leaves the field out.
func TestRunnerSizeClassInResponse(t *testing.T) {
	with := toGenRunner(runners.View{Record: runners.Record{ID: "r1", SizeClass: runners.SizeLarge}})
	if with.SizeClass == nil || *with.SizeClass != "large" {
		t.Fatalf("size class = %v", with.SizeClass)
	}
	if without := toGenRunner(runners.View{Record: runners.Record{ID: "r2"}}); without.SizeClass != nil {
		t.Fatalf("size class = %v", *without.SizeClass)
	}
}
