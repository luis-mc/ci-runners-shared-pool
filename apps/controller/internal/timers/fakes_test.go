package timers

import (
	"context"
	"errors"
	"sync"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

type fakeAgent struct {
	calls []string
	err   error
}

func (a *fakeAgent) StopJobRunner(_ context.Context, machine, ref string) error {
	a.calls = append(a.calls, machine+"/"+ref)
	return a.err
}

type fakeGH struct {
	calls []int64
	err   error
}

func (g *fakeGH) CancelRun(_ context.Context, _ string, id int64) error {
	g.calls = append(g.calls, id)
	return g.err
}

type fakeAudit struct {
	rows []Event
	err  error
}

func (a *fakeAudit) Record(_ context.Context, e Event) error {
	if a.err != nil {
		return a.err
	}
	a.rows = append(a.rows, e)
	return nil
}

func (a *fakeAudit) actions() []string {
	var out []string
	for _, r := range a.rows {
		out = append(out, r.Action)
	}
	return out
}

type fakeStore struct {
	mu   sync.Mutex
	jobs map[JobKey]JobClock
	runs map[RunKey]RunClock
}

func newFakeStore() *fakeStore {
	return &fakeStore{jobs: map[JobKey]JobClock{}, runs: map[RunKey]RunClock{}}
}

func (s *fakeStore) SaveJob(_ context.Context, c JobClock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[c.Key] = c
	return nil
}

func (s *fakeStore) SaveRun(_ context.Context, c RunClock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[c.Key] = c
	return nil
}

func (s *fakeStore) Open(context.Context) ([]JobClock, []RunClock, error) {
	var js []JobClock
	var rs []RunClock
	for _, c := range s.jobs {
		if c.State == StateRunning || c.State == StateGrace {
			js = append(js, c)
		}
	}
	for _, c := range s.runs {
		if c.State == StateRunning || c.State == StateGrace {
			rs = append(rs, c)
		}
	}
	return js, rs, nil
}

func (s *fakeStore) LoadJob(_ context.Context, k JobKey) (JobClock, bool, error) {
	c, ok := s.jobs[k]
	return c, ok, nil
}

func (s *fakeStore) LoadRun(_ context.Context, k RunKey) (RunClock, bool, error) {
	c, ok := s.runs[k]
	return c, ok, nil
}

var errBoom = errors.New("boom")

type rig struct {
	clock *fakeClock
	agent *fakeAgent
	gh    *fakeGH
	audit *fakeAudit
	store *fakeStore
	eng   *Engine
}

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

// newRig is an engine in enforce mode: the behaviour before observe mode.
func newRig() *rig { return newRigMode(true) }

func newRigMode(enforce bool) *rig {
	r := &rig{clock: &fakeClock{t: t0}, agent: &fakeAgent{}, gh: &fakeGH{}, audit: &fakeAudit{}, store: newFakeStore()}
	set := Defaults()
	set.Enforce = enforce
	r.eng = New(Config{Settings: func() Settings { return set }, Clock: r.clock, Agent: r.agent, GitHub: r.gh, Audit: r.audit, Store: r.store})
	return r
}

func jobKey(id int64) JobKey {
	return JobKey{Repo: "luis-mc/app", RunID: 900, Attempt: 1, JobID: id}
}

func (r *rig) start(id int64) JobKey {
	k := jobKey(id)
	if err := r.eng.JobStarted(context.Background(), JobStart{Key: k, MachineID: "m1", RunnerRef: "runner-" + string(rune('a'+id))}); err != nil {
		panic(err)
	}
	return k
}

func (r *rig) tick(d time.Duration) {
	r.clock.Advance(d)
	if err := r.eng.Tick(context.Background()); err != nil {
		panic(err)
	}
}
