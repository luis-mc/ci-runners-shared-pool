package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/poolname"
)

type poolStub struct{ job poolname.Job }

func (p *poolStub) Info(context.Context) poolname.Info {
	return poolname.Info{Name: "crsp-home", Source: poolname.SourceDefault}
}
func (p *poolStub) SetFirst(_ context.Context, n, _ string) (poolname.Info, error) {
	if err := poolname.Validate(n); err != nil {
		return poolname.Info{}, err
	}
	return poolname.Info{Name: n, Source: poolname.SourceStored}, nil
}
func (p *poolStub) Plan(_ context.Context, n, _ string) (poolname.Job, error) {
	p.job = poolname.Job{ID: "pr1", Old: "crsp-home", New: n, State: poolname.JobPlanned, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Steps: []poolname.Step{{ID: "finish", Kind: poolname.KindFinish, Text: "Finish", State: poolname.StepPending}}}
	return p.job, nil
}
func (p *poolStub) Get(context.Context, string) (poolname.Job, error) { return p.job, nil }
func (p *poolStub) Apply(context.Context, string, string) (poolname.Job, error) {
	p.job.State = poolname.JobApplying
	return p.job, nil
}
func (p *poolStub) Stop(context.Context, string, string) (poolname.Job, error) {
	return p.job, poolname.ErrNotApplying
}

// runners R5.141 to R5.144: name, plan, run, stop; an admin writes, a viewer reads.
func TestPoolNameRoutes(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux, Options{Version: "t", DB: fakeDB{}, PoolName: &poolStub{}, Authn: asRole(auth.RoleOwner)})
	if rec := do(mux, http.MethodGet, "/api/v1/pool/name", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"crsp-home"`) {
		t.Fatalf("get %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPut, "/api/v1/pool/name", `{"name":"Bad Name"}`); rec.Code != 400 {
		t.Fatalf("a bad name is refused: %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPut, "/api/v1/pool/name", `{"name":"crsp-lab"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "crsp-lab") {
		t.Fatalf("put %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPost, "/api/v1/pool/renames", `{"name":"crsp-lab"}`); rec.Code != 201 || !strings.Contains(rec.Body.String(), `"state":"planned"`) {
		t.Fatalf("plan %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodPost, "/api/v1/pool/renames/pr1/apply", ""); rec.Code != 202 || !strings.Contains(rec.Body.String(), `"applying"`) {
		t.Fatalf("apply %d %s", rec.Code, rec.Body)
	}
	if rec := do(mux, http.MethodGet, "/api/v1/pool/renames/pr1", ""); rec.Code != 200 {
		t.Fatalf("get rename %d", rec.Code)
	}
	if rec := do(mux, http.MethodPost, "/api/v1/pool/renames/pr1/stop", ""); rec.Code != 409 {
		t.Fatalf("stop of a rename that is not running is a conflict: %d %s", rec.Code, rec.Body)
	}
	viewer := http.NewServeMux()
	Register(viewer, Options{Version: "t", DB: fakeDB{}, PoolName: &poolStub{}, Authn: asRole(auth.RoleViewer)})
	if rec := do(viewer, http.MethodPost, "/api/v1/pool/renames", `{"name":"crsp-lab"}`); rec.Code != 403 {
		t.Errorf("a viewer planned: %d", rec.Code)
	}
	if rec := do(viewer, http.MethodGet, "/api/v1/pool/name", ""); rec.Code != 200 {
		t.Errorf("a viewer reads: %d", rec.Code)
	}
	off := http.NewServeMux()
	Register(off, Options{Version: "t", DB: fakeDB{}, Authn: asRole(auth.RoleOwner)})
	if rec := do(off, http.MethodGet, "/api/v1/pool/name", ""); rec.Code != 501 {
		t.Errorf("not wired: %d", rec.Code)
	}
}
