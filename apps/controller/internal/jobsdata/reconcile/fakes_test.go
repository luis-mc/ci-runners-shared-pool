package reconcile

import (
	"context"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/share"
)

var t0 = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

type fakeRunners map[string]bool

func (f fakeRunners) IsCrsp(_ context.Context, name string) (bool, error) { return f[name], nil }

type fakeGH struct {
	mu       sync.Mutex
	jobs     map[int64]RemoteJob
	runs     map[int64][]RemoteJob
	goneRuns map[int64]bool
	jobCalls int
	runCalls []int64
}

func (g *fakeGH) Job(_ context.Context, _ int64, _, _ string, id int64) (RemoteJob, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.jobCalls++
	j, ok := g.jobs[id]
	if !ok {
		return RemoteJob{}, ErrGone
	}
	return j, nil
}

func (g *fakeGH) RunJobs(_ context.Context, _ int64, _, _ string, runID int64, _ int) ([]RemoteJob, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.runCalls = append(g.runCalls, runID)
	if g.goneRuns[runID] {
		return nil, ErrGone
	}
	return g.runs[runID], nil
}

type applied struct {
	job    RemoteJob
	origin Origin
}

type fakeStore struct {
	runs    []StaleRun
	open    map[int64][]int64
	applied []applied
	lost    map[int64]string
	steps   map[int64][]RemoteStep
	refs    map[int64]JobRef
	gotLim  int
}

func (s *fakeStore) StaleRuns(_ context.Context, _, _ time.Time, limit int) ([]StaleRun, error) {
	s.gotLim = limit
	return s.runs, nil
}
func (s *fakeStore) Apply(_ context.Context, _ int64, j RemoteJob, o Origin, _ time.Time) error {
	s.applied = append(s.applied, applied{j, o})
	return nil
}
func (s *fakeStore) MarkLost(_ context.Context, id int64, reason string, _ time.Time) error {
	if s.lost == nil {
		s.lost = map[int64]string{}
	}
	s.lost[id] = reason
	return nil
}
func (s *fakeStore) OpenJobs(_ context.Context, _, runID int64, _ int) ([]int64, error) {
	return s.open[runID], nil
}
func (s *fakeStore) JobRef(_ context.Context, id int64) (JobRef, bool, error) {
	r, ok := s.refs[id]
	return r, ok, nil
}
func (s *fakeStore) ApplySteps(_ context.Context, id int64, st []RemoteStep, _ time.Time) error {
	if s.steps == nil {
		s.steps = map[int64][]RemoteStep{}
	}
	s.steps[id] = st
	return nil
}

type fakeBudget struct{ left int }

func (b *fakeBudget) Take(_ context.Context, _ int64, _ share.Priority, n int) (bool, time.Time) {
	if b.left < n {
		return false, t0.Add(time.Hour)
	}
	b.left -= n
	return true, time.Time{}
}
func (b *fakeBudget) Backlog() (int, time.Time) { return 0, time.Time{} }

func newRec(gh *fakeGH, st *fakeStore, b *fakeBudget, now *time.Time) Reconciler {
	return New(Config{GitHub: gh, Store: st, Jobs: st, Budget: b,
		Classify:     NewClassifier(fakeRunners{"crsp-1": true}),
		Installation: func(context.Context, int64) (int64, bool) { return 7, true },
		Now:          func() time.Time { return *now }})
}
