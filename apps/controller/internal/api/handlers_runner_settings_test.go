package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// Runners R5.90: PATCH settings changes keep_dependencies of a persistent runner.
func TestUpdateRunnerSettings(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, http.MethodPatch, "/api/v1/runners/r1/settings", `{"keep_dependencies":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		KeepDependencies *bool `json:"keep_dependencies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.KeepDependencies == nil || *out.KeepDependencies {
		t.Fatalf("body %s", rec.Body)
	}
	if f.lastActor != "luis-mc" || f.recs["r1"].KeepDependencies {
		t.Errorf("actor %q record %+v", f.lastActor, f.recs["r1"])
	}
	if rec := do(mux, http.MethodPatch, "/api/v1/runners/nope/settings", `{"keep_dependencies":true}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown runner: %d", rec.Code)
	}
}

// An ephemeral runner has no settings: refused with its reason.
func TestUpdateRunnerSettingsRefusesEphemeral(t *testing.T) {
	f := sample()
	e := f.recs["r1"]
	e.Kind = "ephemeral"
	f.recs["r1"] = e
	rec := do(runnersMux(f), http.MethodPatch, "/api/v1/runners/r1/settings", `{"keep_dependencies":false}`)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// POST clear-cache answers 202 with the runner and asks the agent to clear.
func TestClearRunnerCache(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, http.MethodPost, "/api/v1/runners/r1/clear-cache", "")
	if rec.Code != http.StatusAccepted || len(f.cleared) != 1 || f.cleared[0] != "r1" {
		t.Fatalf("status %d cleared %v: %s", rec.Code, f.cleared, rec.Body)
	}
	e := f.recs["r1"]
	e.Kind = "ephemeral"
	f.recs["r1"] = e
	if rec := do(mux, http.MethodPost, "/api/v1/runners/r1/clear-cache", ""); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("ephemeral: %d", rec.Code)
	}
}

// keep_dependencies of a plan reaches the service.
func TestPlanRunnersPassesKeepDependencies(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	if rec := do(mux, http.MethodPost, "/api/v1/runner-plans", `{"os":"linux","kind":"persistent","machine_id":"m1","count":1,"keep_dependencies":false}`); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if f.lastAdd.KeepDependencies == nil || *f.lastAdd.KeepDependencies {
		t.Fatalf("keep = %v", f.lastAdd.KeepDependencies)
	}
	do(mux, http.MethodPost, "/api/v1/runner-plans", `{"os":"linux","kind":"persistent","machine_id":"m1","count":1}`)
	if f.lastAdd.KeepDependencies != nil {
		t.Errorf("default sent %v", *f.lastAdd.KeepDependencies)
	}
}

// CRSP382, runners R5.153: POST reregister answers 200 with the runner and asks
// the service to register it again; a refusal is a 4xx.
func TestReregisterRunner(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, http.MethodPost, "/api/v1/runners/r1/reregister", "")
	if rec.Code != http.StatusOK || len(f.reregistered) != 1 || f.reregistered[0] != "r1" {
		t.Fatalf("status %d registered again %v: %s", rec.Code, f.reregistered, rec.Body)
	}
	e := f.recs["r1"]
	e.Kind = "ephemeral"
	f.recs["r1"] = e
	if rec := do(mux, http.MethodPost, "/api/v1/runners/r1/reregister", ""); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("ephemeral: %d", rec.Code)
	}
}

// CRSP382: can_reregister reaches the console only when the view says so.
func TestRunnerCanReregisterIsInTheAnswer(t *testing.T) {
	r := toGenRunner(runners.View{Record: runners.Record{ID: "r1", Kind: "persistent"}, State: runners.NotRegistered, CanReregister: true})
	if r.CanReregister == nil || !*r.CanReregister {
		t.Fatal("can_reregister is true")
	}
	if toGenRunner(runners.View{Record: runners.Record{ID: "r1"}, State: runners.Idle}).CanReregister != nil {
		t.Fatal("absent when there is nothing to offer")
	}
}

// CRSP387, runners R5.157: POST start answers 200 with the runner and asks the
// service to start it; a refusal is a 4xx; can_start shows only when offered.
func TestStartRunner(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, http.MethodPost, "/api/v1/runners/r1/start", "")
	if rec.Code != http.StatusOK || len(f.runnerStarted) != 1 || f.runnerStarted[0] != "r1" {
		t.Fatalf("status %d started %v: %s", rec.Code, f.runnerStarted, rec.Body)
	}
	e := f.recs["r1"]
	e.Kind = "ephemeral"
	f.recs["r1"] = e
	if rec := do(mux, http.MethodPost, "/api/v1/runners/r1/start", ""); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("ephemeral: %d", rec.Code)
	}
	r := toGenRunner(runners.View{Record: runners.Record{ID: "r1", Kind: "persistent"}, State: runners.Stopped, CanStart: true})
	if r.CanStart == nil || !*r.CanStart {
		t.Fatal("can_start is true")
	}
	if toGenRunner(runners.View{Record: runners.Record{ID: "r1"}, State: runners.Idle}).CanStart != nil {
		t.Fatal("absent when there is nothing to offer")
	}
}
