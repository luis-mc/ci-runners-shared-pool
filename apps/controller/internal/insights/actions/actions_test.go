package actions

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

type memPlans struct {
	mu sync.Mutex
	m  map[string]Plan
}

func (p *memPlans) Insert(_ context.Context, pl Plan) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[pl.ID] = pl
	return nil
}
func (p *memPlans) Get(_ context.Context, id string) (Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl, ok := p.m[id]
	if !ok {
		return Plan{}, ErrNotFound
	}
	return pl, nil
}
func (p *memPlans) Update(_ context.Context, pl Plan) error {
	return p.Insert(context.Background(), pl)
}

type fakeFindings map[int64]insights.StoredFinding

func (f fakeFindings) Get(_ context.Context, id int64) (insights.StoredFinding, error) {
	x, ok := f[id]
	if !ok {
		return x, ErrNotFound
	}
	return x, nil
}

type fakeSetter struct {
	applied []Change
	audit   []string // one audit row per applied change, as a real setter writes
	failOn  string
	onApply func()
}

func (s *fakeSetter) Apply(_ context.Context, c Change, by string) error {
	if s.onApply != nil {
		s.onApply()
	}
	if c.Target == s.failOn {
		return errors.New("machine is gone")
	}
	s.applied = append(s.applied, c)
	s.audit = append(s.audit, by+" "+c.Target+" "+c.Field)
	return nil
}

type fakeRunning map[string][]string

func (r fakeRunning) RunningOn(_ context.Context, t string) ([]string, error) { return r[t], nil }

type fakeAudit struct{ refused []string }

func (a *fakeAudit) Refused(_ context.Context, _, _ string, c Change, reason string) error {
	a.refused = append(a.refused, c.Target+": "+reason)
	return nil
}

func evidence(t *testing.T, field string, targets ...string) json.RawMessage {
	t.Helper()
	var ch []map[string]any
	for _, tg := range targets {
		ch = append(ch, map[string]any{"target": tg, "field": field, "from": 4, "to": 2, "words": "two slots"})
	}
	b, err := json.Marshal(map[string]any{"changes": ch})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t *testing.T, kind insights.ActionKind, field string, targets ...string) (*Service, *fakeSetter, *fakeAudit) {
	t.Helper()
	set := &fakeSetter{}
	au := &fakeAudit{}
	f := insights.StoredFinding{ID: 7}
	f.Action = kind
	f.MinutesSavedWeek = 42
	f.Evidence = evidence(t, field, targets...)
	s := &Service{
		Plans: &memPlans{m: map[string]Plan{}}, Findings: fakeFindings{7: f},
		Setters: Setters{kind: set}, Running: fakeRunning{"m1": {"job 1"}}, Audit: au,
		Now: func() time.Time { return time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC) },
	}
	return s, set, au
}

func TestNothingChangesBeforeStart(t *testing.T) {
	s, set, _ := fixture(t, insights.ActionMachineSlots, "job_slots", "m1", "m2")
	p, err := s.Plan(context.Background(), 7, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != Draft || len(set.applied) != 0 || len(set.audit) != 0 {
		t.Fatalf("state %s applied %v", p.State, set.applied)
	}
	if got := p.Changes[0].JobsWaitedFor; len(got) != 1 || got[0] != "job 1" {
		t.Fatalf("lowering names running jobs: %v", got)
	}
	if p.Estimate == "" || p.Results[0].State != "pending" {
		t.Fatalf("estimate %q results %v", p.Estimate, p.Results)
	}
	g, err := s.Get(context.Background(), p.ID)
	if err != nil || g.State != Draft {
		t.Fatalf("get: %v %v", g.State, err)
	}
}

func TestPlanRefusals(t *testing.T) {
	s, _, _ := fixture(t, insights.ActionMachineSlots, "job_slots", "m1")
	none := insights.StoredFinding{ID: 8}
	resolved := insights.StoredFinding{ID: 9, ResolvedAt: time.Now()}
	resolved.Action = insights.ActionMachineSlots
	resolved.Evidence = evidence(t, "job_slots", "m1")
	wrongField := insights.StoredFinding{ID: 10}
	wrongField.Action = insights.ActionLightLane
	wrongField.Evidence = evidence(t, "job_slots", "m1")
	s.Findings.(fakeFindings)[8], s.Findings.(fakeFindings)[9], s.Findings.(fakeFindings)[10] = none, resolved, wrongField
	for _, id := range []int64{8, 9} {
		if _, err := s.Plan(context.Background(), id, "a"); !errors.Is(err, ErrNoAction) {
			t.Fatalf("finding %d: %v", id, err)
		}
	}
	if _, err := s.Plan(context.Background(), 10, "a"); err == nil {
		t.Fatal("a field of another kind must be refused")
	}
	if _, err := s.Plan(context.Background(), 99, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown finding: %v", err)
	}
}

func TestStartWritesAuditPerChange(t *testing.T) {
	s, set, _ := fixture(t, insights.ActionRepoParallelLimit, "max_parallel_jobs", "owner/a", "owner/b")
	p, _ := s.Plan(context.Background(), 7, "alice")
	done, err := s.Start(context.Background(), p.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if done.State != Done || len(set.audit) != 2 || set.audit[0] != "bob owner/a max_parallel_jobs" {
		t.Fatalf("state %s audit %v", done.State, set.audit)
	}
	if done.StartedAt.IsZero() || done.FinishedAt.IsZero() {
		t.Fatal("times missing")
	}
	// A second Start never repeats the work.
	if _, err := s.Start(context.Background(), p.ID, "bob"); !errors.Is(err, ErrNotDraft) || len(set.applied) != 2 {
		t.Fatalf("second start: %v applied %d", err, len(set.applied))
	}
}

func TestFailedChangeKeepsDoneOnes(t *testing.T) {
	s, set, au := fixture(t, insights.ActionLightLane, "light_slots", "m1", "m2", "m3")
	set.failOn = "m2"
	p, _ := s.Plan(context.Background(), 7, "a")
	r, err := s.Start(context.Background(), p.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{r.Results[0].State, r.Results[1].State, r.Results[2].State}
	if r.State != Failed || got[0] != "done" || got[1] != "failed" || got[2] != "done" {
		t.Fatalf("state %s results %v", r.State, got)
	}
	if len(au.refused) != 1 || len(set.audit) != 2 {
		t.Fatalf("refused %v audit %v", au.refused, set.audit)
	}
}

func TestStopSkipsTheRest(t *testing.T) {
	s, set, _ := fixture(t, insights.ActionMachineSlots, "job_slots", "m1", "m2")
	ctx, cancel := context.WithCancel(context.Background())
	set.onApply = cancel
	p, _ := s.Plan(context.Background(), 7, "a")
	r, _ := s.Start(ctx, p.ID, "bob")
	if r.State != Stopped || r.Results[0].State != "done" || r.Results[1].State != "skipped" {
		t.Fatalf("state %s results %v", r.State, r.Results)
	}
	stored, _ := s.Get(context.Background(), p.ID)
	if stored.State != Stopped {
		t.Fatalf("stored %s", stored.State)
	}
}
