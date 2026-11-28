package runners

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

type memStore struct {
	mu      sync.Mutex
	runners map[string]Record
	plans   map[string]Plan
	events  []string
	svcErr  error // SetRunnerService fails with it
}

func newMemStore() *memStore {
	return &memStore{runners: map[string]Record{}, plans: map[string]Plan{}}
}

func (m *memStore) InsertRunner(_ context.Context, r Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runners[r.ID] = r
	return nil
}
func (m *memStore) GetRunner(_ context.Context, id string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}
func (m *memStore) ListRunners(_ context.Context, f Filter) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.runners {
		if f.MachineID != "" && r.MachineID != f.MachineID {
			continue
		}
		if f.Managed != nil && r.Managed != *f.Managed {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *memStore) SetRunnerState(_ context.Context, id string, to State, detail string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	m.events = append(m.events, fmt.Sprintf("%s %s->%s %s", id, r.State, to, detail))
	r.State = to
	r.FailedReason = ""
	if to == Failed {
		r.FailedReason = detail
	}
	r.Reason = detail
	m.runners[id] = r
	return nil
}
func (m *memStore) SetRunnerPool(_ context.Context, id, poolRunnerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.PoolRunnerID = poolRunnerID
	m.runners[id] = r
	return nil
}

func (m *memStore) SetRunnerHygiene(_ context.Context, id string, h Hygiene) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.KeepDependencies, r.DependencyCacheBytes, r.UncleanReason = h.KeepDependencies, h.DependencyCacheBytes, h.UncleanReason
	m.runners[id] = r
	return nil
}

func (m *memStore) SetRunnerService(_ context.Context, id, svc, acct string) error {
	if m.svcErr != nil {
		return m.svcErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runners[id]
	r.ServiceName, r.Account = svc, acct
	m.runners[id] = r
	return nil
}
func (m *memStore) SetRunnerRegistration(_ context.Context, id string, _ int64, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.runners[id]
	r.RunnerVersion = v
	m.runners[id] = r
	return nil
}
func (m *memStore) DeleteRunner(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.runners, id)
	return nil
}
func (m *memStore) SavePlan(_ context.Context, p Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.Steps = append([]Step(nil), p.Steps...)
	m.plans[p.ID] = p
	return nil
}
func (m *memStore) GetPlan(_ context.Context, id string) (Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	if !ok {
		return Plan{}, ErrNotFound
	}
	p.Steps = append([]Step(nil), p.Steps...)
	return p, nil
}

type fakeMachines map[string]Machine

func (f fakeMachines) GetMachine(_ context.Context, id string) (Machine, error) {
	m, ok := f[id]
	if !ok {
		return Machine{}, ErrNotFound
	}
	return m, nil
}

type fakeResolver struct{}

func (fakeResolver) Resolve(_ context.Context, s Scope) (Target, error) {
	return Target{Scope: github.RunnerScope{Owner: "luis-mc", Repo: "demo"}, InstallationID: 7, URL: "https://github.com/luis-mc/demo"}, nil
}

type fakeGitHub struct {
	runners  []github.Runner
	listErr  error
	regCalls int
	rmCalls  int
	jitCalls []github.JITRequest
	jitErr   error
	tokens   []string
	deleted  []int64
	delErr   error
}

func (g *fakeGitHub) CreateRegistrationToken(context.Context, int64, github.RunnerScope) (string, error) {
	g.regCalls++
	g.tokens = append(g.tokens, "REG-TOKEN-1")
	return "REG-TOKEN-1", nil
}
func (g *fakeGitHub) CreateRemoveToken(context.Context, int64, github.RunnerScope) (string, error) {
	g.rmCalls++
	g.tokens = append(g.tokens, "REMOVE-TOKEN-1")
	return "REMOVE-TOKEN-1", nil
}
func (g *fakeGitHub) GenerateJITConfig(_ context.Context, _ int64, _ github.RunnerScope, in github.JITRequest) (github.JITConfig, error) {
	g.jitCalls = append(g.jitCalls, in)
	if g.jitErr != nil {
		return github.JITConfig{}, g.jitErr
	}
	return github.NewJITConfig(4242, "JIT-CONFIG-SECRET-1"), nil
}
func (g *fakeGitHub) ListRunners(context.Context, int64, github.RunnerScope) ([]github.Runner, error) {
	return g.runners, g.listErr
}

func (g *fakeGitHub) DeleteRunner(_ context.Context, _ int64, _ github.RunnerScope, id int64) error {
	if g.delErr != nil {
		return g.delErr
	}
	g.deleted = append(g.deleted, id)
	return nil
}

type fakeAgent struct {
	added    []backend.RunnerSpec
	cfgs     []backend.StartConfig
	removed  []RemoveRequest
	stopped  []string
	started  []string
	startErr error // StartService fails with it
	folders  []string
	addErr   error
	cleanErr error  // CleanUp fails with it
	errRef   string // service ref returned with addErr: the service exists but did not start
	rmErr    error
}

func (a *fakeAgent) AddRunner(_ context.Context, _ string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	a.added, a.cfgs = append(a.added, spec), append(a.cfgs, cfg)
	if a.addErr != nil {
		return backend.Handle{Ref: a.errRef}, a.addErr
	}
	return backend.Handle{RunnerID: spec.ID, Ref: "actions.runner.luis-mc-demo." + spec.Name}, nil
}
func (a *fakeAgent) StopService(_ context.Context, _ string, svc string) error {
	a.stopped = append(a.stopped, svc)
	return nil
}
func (a *fakeAgent) StartService(_ context.Context, _ string, svc string) error {
	if a.startErr != nil {
		return a.startErr
	}
	a.started = append(a.started, svc)
	return nil
}
func (a *fakeAgent) RemoveRunner(_ context.Context, _ string, req RemoveRequest) error {
	if a.rmErr != nil {
		return a.rmErr
	}
	a.removed = append(a.removed, req)
	return nil
}
func (a *fakeAgent) CleanUp(_ context.Context, _ string, svc string) error {
	if a.cleanErr != nil {
		return a.cleanErr
	}
	a.folders = append(a.folders, svc)
	return nil
}

type fakeAudit struct{ entries []string }

func (f *fakeAudit) Audit(_ context.Context, actor, action, machine, subject, detail string) {
	f.entries = append(f.entries, fmt.Sprintf("%s|%s|%s|%s|%s", actor, action, machine, subject, detail))
}

func (m *memStore) SetRunnerLabels(_ context.Context, id string, labels []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runners[id]
	if !ok {
		return ErrNotFound
	}
	r.Labels = append([]string(nil), labels...)
	m.runners[id] = r
	return nil
}
