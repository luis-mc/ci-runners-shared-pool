package runners

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

type stubBackend struct {
	calls  []string
	derErr error
}

func (s *stubBackend) Name() backend.Name { return backend.Persistent }
func (s *stubBackend) Detect(context.Context) (backend.Support, error) {
	return backend.Support{}, nil
}
func (s *stubBackend) PrepareImage(context.Context, backend.ImageSpec) (backend.ImageRef, error) {
	return backend.ImageRef{}, nil
}
func (s *stubBackend) Start(_ context.Context, r backend.RunnerSpec, _ backend.StartConfig) (backend.Handle, error) {
	s.calls = append(s.calls, "start "+r.Name)
	return backend.Handle{Ref: "svc"}, nil
}
func (s *stubBackend) StreamLogs(context.Context, backend.Handle, chan<- backend.LogLine) error {
	return nil
}
func (s *stubBackend) Stop(_ context.Context, h backend.Handle) error {
	s.calls = append(s.calls, "stop "+h.Ref)
	return nil
}
func (s *stubBackend) CleanUp(_ context.Context, h backend.Handle) error {
	s.calls = append(s.calls, "cleanup "+h.Ref)
	return nil
}
func (s *stubBackend) ReportUsage(context.Context, backend.Handle) (backend.Usage, error) {
	return backend.Usage{}, nil
}
func (s *stubBackend) Deregister(_ context.Context, h backend.Handle, tok string) error {
	s.calls = append(s.calls, "deregister "+h.Ref+" "+tok)
	return s.derErr
}

type oneReg struct{ b backend.Backend }

func (o oneReg) All() []backend.Backend { return []backend.Backend{o.b} }
func (o oneReg) Get(n backend.Name) (backend.Backend, bool) {
	return o.b, n == backend.Persistent
}

// R5.19 and R5.10: deregister first and check it; clean up only after a good
// removal, and never when KeepFiles is set.
func TestLocalAgentRemoveOrder(t *testing.T) {
	sb := &stubBackend{}
	a := NewLocalAgent(oneReg{sb})
	ctx := context.Background()
	if err := a.RemoveRunner(ctx, "m", RemoveRequest{ServiceName: "svc", RemoveToken: "T"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(sb.calls, "|") != "deregister svc T|cleanup svc" {
		t.Errorf("%v", sb.calls)
	}
	sb.calls = nil
	if err := a.RemoveRunner(ctx, "m", RemoveRequest{ServiceName: "svc", RemoveToken: "T", KeepFiles: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(sb.calls, "|") != "deregister svc T" {
		t.Errorf("%v", sb.calls)
	}
	sb.calls, sb.derErr = nil, errors.New("exit 1")
	if err := a.RemoveRunner(ctx, "m", RemoveRequest{ServiceName: "svc", RemoveToken: "T"}); err == nil {
		t.Fatal("want error")
	}
	if strings.Join(sb.calls, "|") != "deregister svc T" {
		t.Errorf("no cleanup after a failed removal: %v", sb.calls)
	}
	if _, err := a.AddRunner(ctx, "m", backend.RunnerSpec{Name: "n"}, backend.StartConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := a.StopService(ctx, "m", "svc"); err != nil {
		t.Fatal(err)
	}
}

func TestStartApplyAndRemoveRunInBackground(t *testing.T) {
	r := newRig(machine("linux", "x64", true))
	var queued []func()
	r.svc.Background = func(f func()) { queued = append(queued, f) }
	ctx := context.Background()
	p, _ := r.svc.PlanAdd(ctx, AddRequest{OS: "linux", Kind: "persistent", MachineID: "m1", Count: 1, Scope: Scope{Kind: "repository", RepositoryID: 1}}, "u")
	got, err := r.svc.StartApply(ctx, p.ID, nil, "u")
	if err != nil || got.State != PlanApplying || len(r.ag.added) != 0 {
		t.Fatalf("%+v %v added=%d", got, err, len(r.ag.added))
	}
	queued[0]()
	if len(r.ag.added) != 1 {
		t.Error("the apply runs in the background")
	}
	if _, err := r.svc.StartApply(ctx, p.ID, nil, "u"); err == nil {
		t.Error("a done plan cannot be applied again")
	}
	if _, err := r.svc.StartApply(ctx, "nope", nil, "u"); !errors.Is(err, ErrNotFound) {
		t.Error(err)
	}
	if _, err := r.svc.StartApply(ctx, p.ID, []string{"zz"}, "u"); err == nil {
		t.Error("unknown step")
	}
	all, _ := r.store.ListRunners(ctx, Filter{})
	_, finish, _ := r.svc.BeginRemove(ctx, all[0].ID, true, "u")
	queued = nil
	r.svc.StartRemove(ctx, finish)
	queued[0]()
	if len(r.ag.removed) != 1 {
		t.Error("the removal runs in the background")
	}
}

type dockerStub struct {
	stubBackend
	layers []string
}

func (d *dockerStub) Name() backend.Name { return backend.Docker }
func (d *dockerStub) PrepareImage(_ context.Context, s backend.ImageSpec) (backend.ImageRef, error) {
	d.calls = append(d.calls, "prepare")
	d.layers = s.Layers
	return backend.ImageRef{ID: "crsp-pool:k"}, nil
}

type twoReg struct{ p, d backend.Backend }

func (t twoReg) All() []backend.Backend { return []backend.Backend{t.d, t.p} }
func (t twoReg) Get(n backend.Name) (backend.Backend, bool) {
	switch n {
	case backend.Persistent:
		return t.p, true
	case backend.Docker:
		return t.d, true
	}
	return nil, false
}

// R5.27, R5.31: a JIT config goes to the container backend, a registration
// token to the persistent one; a container handle is cleaned up by the container
// backend and an OS service by the persistent one.
func TestLocalAgentRoutesByKind(t *testing.T) {
	ps, ds := &stubBackend{}, &dockerStub{}
	a := NewLocalAgent(twoReg{ps, ds})
	ctx := context.Background()
	if _, err := a.AddRunner(ctx, "m", backend.RunnerSpec{Name: "crsp-job-1", OS: "linux"}, backend.StartConfig{JITConfig: "J"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddRunner(ctx, "m", backend.RunnerSpec{Name: "box-1", OS: "linux"}, backend.StartConfig{Registration: "R"}); err != nil {
		t.Fatal(err)
	}
	if err := a.CleanUp(ctx, "m", "crsp-pool-x:crsp-job-1"); err != nil {
		t.Fatal(err)
	}
	if err := a.CleanUp(ctx, "m", "actions.runner.x.box-1.service"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ds.calls, ","); got != "prepare,start crsp-job-1,cleanup crsp-pool-x:crsp-job-1" {
		t.Errorf("container backend calls: %s", got)
	}
	if got := strings.Join(ps.calls, ","); got != "start box-1,cleanup actions.runner.x.box-1.service" {
		t.Errorf("persistent backend calls: %s", got)
	}
	// A registry without the container backend refuses a JIT config.
	if _, err := NewLocalAgent(oneReg{ps}).AddRunner(ctx, "m", backend.RunnerSpec{Name: "x"}, backend.StartConfig{JITConfig: "J"}); err == nil {
		t.Error("no container backend")
	}
}

// CRSP68: the image is built with the layers the repository's pool turned on.
func TestLocalAgentBuildsTheImageWithThePoolLayers(t *testing.T) {
	ds := &dockerStub{}
	spec := backend.RunnerSpec{Name: "crsp-job-1", OS: "linux", Pool: backend.PoolSetup{Layers: []string{"browsers", "kvm"}}}
	if _, err := NewLocalAgent(twoReg{&stubBackend{}, ds}).AddRunner(context.Background(), "m", spec, backend.StartConfig{JITConfig: "J"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ds.layers, ",") != "browsers,kvm" {
		t.Errorf("layers = %v", ds.layers)
	}
}
