package assign

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/admit"
)

var t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

const (
	repoName = "o/r"
	e2eLabel = "crsp-e2e-ab12"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type fakeEnv struct {
	machines []Machine
	req      map[string]admit.Req // by job name
	noRepo   bool
	err      error
}

func (e *fakeEnv) Machines(context.Context) ([]Machine, error) { return e.machines, e.err }
func (e *fakeEnv) Repo(context.Context, string) (Repo, bool, error) {
	return Repo{Scope: runners.Scope{Kind: "repository", RepositoryID: 7}, InstallationID: 1}, !e.noRepo, nil
}
func (e *fakeEnv) Requirement(_, job string) (admit.Req, string) {
	if r, ok := e.req[job]; ok {
		return r, "c"
	}
	return admit.Req{CPUs: 1, MemoryGiB: 1}, "default"
}

type fakeRunners struct {
	mu       sync.Mutex
	started  []runners.JobRunner
	removed  []string
	finished []string
	failNext error
	busy     map[string]bool
	n        int
}

func (f *fakeRunners) StartJobRunner(_ context.Context, j runners.JobRunner, _ string) (runners.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return runners.Record{}, err
	}
	f.n++
	f.started = append(f.started, j)
	return runners.Record{ID: fmt.Sprintf("r_%d", f.n), Name: j.Name, GitHubRunnerID: int64(100 + f.n)}, nil
}
func (f *fakeRunners) FinishJobRunner(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, id)
	return nil
}
func (f *fakeRunners) RemoveIdleRunner(_ context.Context, id, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[id] {
		return runners.ErrRunnerBusy
	}
	f.removed = append(f.removed, id)
	return nil
}

type fakeNotify struct{ texts []string }

func (n *fakeNotify) Notify(_ context.Context, l Lease, text string) {
	n.texts = append(n.texts, fmt.Sprintf("%d: %s", l.JobID, text))
}

type rig struct {
	t   *testing.T
	clk *clock
	env *fakeEnv
	st  *MemStore
	rn  *fakeRunners
	nt  *fakeNotify
	c   *Controller
}

func m1(cpus, mem int) Machine {
	return Machine{ID: "m1", Name: "M1", OS: "linux", Arch: "x64", Pool: admit.Pool{CPUs: cpus, MemoryGiB: mem},
		Labels: []string{"self-hosted", "linux", "x64"}, LoadOK: true, Online: true}
}

func newRig(t *testing.T, ms ...Machine) *rig {
	r := &rig{t: t, clk: &clock{t: t0}, env: &fakeEnv{machines: ms, req: map[string]admit.Req{}}, st: NewMemStore(), rn: &fakeRunners{busy: map[string]bool{}}, nt: &fakeNotify{}}
	r.c = r.controller()
	return r
}

func (r *rig) controller() *Controller {
	return &Controller{Store: r.st, Env: r.env, Runners: r.rn, Notify: r.nt, Now: r.clk.now, NewID: func() string { return "x" }}
}

func job(id int64, name string, labels ...string) github.Job {
	if len(labels) == 0 {
		labels = []string{"self-hosted", "linux", "x64", e2eLabel}
	}
	return github.Job{ID: id, RunID: id * 10, RunAttempt: 1, Repository: repoName, Name: name, Labels: labels, Status: "queued"}
}

func (r *rig) lease(id int64) Lease {
	r.t.Helper()
	l, ok, err := r.st.Get(context.Background(), id)
	if err != nil || !ok {
		r.t.Fatalf("lease %d: ok=%v err=%v", id, ok, err)
	}
	return l
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var errBoom = errors.New("boom")
