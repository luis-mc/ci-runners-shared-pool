package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/replace"
)

type fakeReplace struct {
	plan registry.ReplacePlan
	err  error
	// keepGot is the keep days CreatePlan was asked with.
	keepGot *int
	keepDef int
}

func (f fakeReplace) KeepDaysDefault(context.Context) int { return f.keepDef }

func (f fakeReplace) CreatePlan(_ context.Context, _ string, keep int, _ string) (registry.ReplacePlan, error) {
	if f.keepGot != nil {
		*f.keepGot = keep
	}
	return f.plan, f.err
}
func (f fakeReplace) Plan(context.Context, string) (registry.ReplacePlan, error) {
	return f.plan, f.err
}
func (f fakeReplace) Start(context.Context, string) (registry.ReplacePlan, error) {
	return f.plan, f.err
}
func (f fakeReplace) Stop(context.Context, string) (registry.ReplacePlan, error) {
	return f.plan, f.err
}
func (f fakeReplace) Rollback(context.Context, string, string) (registry.ReplacePlan, error) {
	return f.plan, f.err
}
func (f fakeReplace) Compare(context.Context, string) (replace.Comparison, error) {
	return replace.Comparison{}, f.err
}
func (f fakeReplace) View(_ context.Context, id string) (replace.ImageView, error) {
	return replace.ImageView{Image: registry.Image{ID: id, Kind: registry.KindCustom, Arch: "amd64", State: registry.StateCurrent}}, nil
}

func replaceServer(f *fakeReplace) *server {
	o := Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if f != nil {
		o.RegistryReplace = *f
	}
	return &server{opts: o}
}

func TestReplaceRoutesMapErrors(t *testing.T) {
	ctx := context.Background()
	rb := func(s *server) int {
		r, _ := s.RollbackRegistryReplacePlan(ctx, gen.RollbackRegistryReplacePlanRequestObject{PlanId: "p"})
		return r.(gen.RollbackRegistryReplacePlandefaultJSONResponse).StatusCode
	}
	if got := rb(replaceServer(nil)); got != http.StatusNotImplemented {
		t.Fatalf("unwired = %d", got)
	}
	if got := rb(replaceServer(&fakeReplace{err: errors.Join(replace.ErrConflict)})); got != http.StatusConflict {
		t.Fatalf("expired rollback = %d", got)
	}
	if got := rb(replaceServer(&fakeReplace{err: registry.ErrNotFound})); got != http.StatusNotFound {
		t.Fatalf("missing = %d", got)
	}
	cmp, _ := replaceServer(&fakeReplace{err: registry.ErrNotFound}).CompareRegistryImage(ctx, gen.CompareRegistryImageRequestObject{RegistryImageId: "i"})
	if cmp.(gen.CompareRegistryImagedefaultJSONResponse).StatusCode != http.StatusNotFound {
		t.Fatal("compare of an image that updates nothing must be 404")
	}
}

func TestReplacePlanBody(t *testing.T) {
	ctx := context.Background()
	p := registry.ReplacePlan{ID: "rpl_1", NewImageID: "n", OldImageID: "o", KeepDays: 7, State: registry.PlanDone,
		KeepUntil: time.Now().Add(time.Hour), Repositories: []string{"acme/app"}, StepsDone: 1, StepsTotal: 1, CreatedAt: time.Now()}
	r, err := replaceServer(&fakeReplace{plan: p}).StartRegistryReplacePlan(ctx, gen.StartRegistryReplacePlanRequestObject{PlanId: "rpl_1"})
	if err != nil {
		t.Fatal(err)
	}
	body := gen.RegistryReplacePlan(r.(gen.StartRegistryReplacePlan202JSONResponse))
	if body.Id != "rpl_1" || body.State != gen.RegistryReplacePlanStateDone || body.CanRollback == nil || !*body.CanRollback || body.Machines == nil || body.NewImage.Id != "n" {
		t.Fatalf("body: %+v", body)
	}
}
