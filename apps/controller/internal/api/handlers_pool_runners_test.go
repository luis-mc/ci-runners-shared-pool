package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// OPS23: runner-plans without a scope add pool runners.
func TestPlanRunnersWithoutScopeAddsPoolRunners(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, "POST", "/api/v1/runner-plans", `{"os":"linux","kind":"persistent","machine_id":"m1","count":2}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if f.lastAdd.Scope.Kind != runners.ScopePool || f.lastAdd.Scope.RepositoryID != 0 {
		t.Errorf("scope = %+v, want pool", f.lastAdd.Scope)
	}
}

func TestPlanRunnersWithPoolScopeAndRepositoryScope(t *testing.T) {
	f := sample()
	mux := runnersMux(f)
	rec := do(mux, "POST", "/api/v1/runner-plans", `{"os":"linux","kind":"persistent","machine_id":"m1","count":1,"scope":{"kind":"pool"}}`)
	if rec.Code != http.StatusCreated || f.lastAdd.Scope.Kind != runners.ScopePool {
		t.Fatalf("pool scope: status %d scope %+v", rec.Code, f.lastAdd.Scope)
	}
	rec = do(mux, "POST", "/api/v1/runner-plans", `{"os":"linux","kind":"persistent","machine_id":"m1","count":1,"scope":{"kind":"repository","repository_id":9}}`)
	if rec.Code != http.StatusCreated || f.lastAdd.Scope.Kind != "repository" || f.lastAdd.Scope.RepositoryID != 9 {
		t.Fatalf("repository scope: status %d scope %+v", rec.Code, f.lastAdd.Scope)
	}
}

func TestRunnerListReturnsServingAndRegistrations(t *testing.T) {
	f := sample()
	f.recs["pool1"] = runners.Record{ID: "pool1", Name: "box-pool-1", MachineID: "m1", OS: "linux", Arch: "x64", Kind: "persistent",
		Backend: "persistent", Isolation: "persistent", Scope: runners.Scope{Kind: runners.ScopePool}, State: runners.Busy, Managed: true}
	f.views = map[string]runners.View{"pool1": {Serving: "luis-mc/web", Registrations: 3}}
	rec := do(runnersMux(f), "GET", "/api/v1/runners", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body struct {
		Items []struct {
			ID            string  `json:"id"`
			Serving       *string `json:"serving"`
			Registrations *int    `json:"registrations"`
			Scope         struct {
				Kind string `json:"kind"`
			} `json:"scope"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, it := range body.Items {
		switch it.ID {
		case "pool1":
			seen = true
			if it.Scope.Kind != "pool" || it.Serving == nil || *it.Serving != "luis-mc/web" || it.Registrations == nil || *it.Registrations != 3 {
				t.Errorf("pool runner = %+v", it)
			}
		default:
			if it.Serving != nil || it.Registrations != nil {
				t.Errorf("runner %s is not a pool runner but shows serving or registrations", it.ID)
			}
		}
	}
	if !seen {
		t.Error("the pool runner is not listed")
	}
}
