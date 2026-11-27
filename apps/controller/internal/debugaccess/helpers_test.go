package debugaccess

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

var (
	owner  = auth.Principal{UserID: 1, Login: "owner1", Role: auth.RoleOwner, Source: "web"}
	admin  = auth.Principal{UserID: 2, Login: "admin1", Role: auth.RoleAdmin, Source: "web"}
	admin2 = auth.Principal{UserID: 3, Login: "admin2", Role: auth.RoleAdmin, Source: "cli"}
	viewer = auth.Principal{UserID: 4, Login: "viewer1", Role: auth.RoleViewer, Source: "web"}
)

const repoID = 100

type fakeJobs struct {
	mu     sync.Mutex
	jobs   map[int64]Job
	beside map[int64][]int64
}

func (f *fakeJobs) DebugJob(_ context.Context, id int64) (Job, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, ok := f.jobs[id]
	return j, ok, nil
}

func (f *fakeJobs) RunningBeside(_ context.Context, j Job) ([]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.beside[j.ID], nil
}

type fakeRepos struct{ repos map[int64]Repo }

func (f fakeRepos) DebugRepo(_ context.Context, id int64) (Repo, error) { return f.repos[id], nil }

type enterOK struct{}

func (enterOK) CanEnter(context.Context, Job) error { return nil }

type env struct {
	t    *testing.T
	ctx  context.Context
	db   *store.Store
	svc  *Service
	jobs *fakeJobs
	now  time.Time
	ids  int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := store.OpenForTests(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &env{t: t, ctx: context.Background(), db: db, now: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	e.jobs = &fakeJobs{jobs: map[int64]Job{}, beside: map[int64][]int64{}}
	for i := int64(1); i <= 5; i++ {
		e.jobs.jobs[i] = Job{ID: i, RepositoryID: repoID, Repository: "alice/app", Name: fmt.Sprintf("build %d", i), Running: true,
			Event: "push", MachineID: "m1", MachineName: "build-02", RunnerName: fmt.Sprintf("r%d", i), Backend: "docker",
			Isolation: "container", OS: "linux"}
	}
	e.svc = &Service{DB: db, Jobs: e.jobs, Repos: fakeRepos{repos: map[int64]Repo{repoID: {Known: true, Visibility: "private"}}},
		Enter: enterOK{}, Now: func() time.Time { return e.now }, NewID: func() string { e.ids++; return fmt.Sprintf("dbg_%d", e.ids) }}
	return e
}

func (e *env) turnOn(tweak func(*RepoSettings)) {
	e.t.Helper()
	r := DefaultRepoSettings()
	r.Enabled = true
	if tweak != nil {
		tweak(&r)
	}
	if _, err := e.svc.SetRepo(e.ctx, owner, repoID, "alice/app", r); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) open(p auth.Principal, job int64) (store.DebugSession, error) {
	return e.svc.Open(e.ctx, p, OpenRequest{JobID: job, Reason: "look at the disk", Source: "web", Addr: "192.0.2.7"})
}

func (e *env) mustOpen(p auth.Principal, job int64) store.DebugSession {
	e.t.Helper()
	d, err := e.open(p, job)
	if err != nil {
		e.t.Fatalf("open job %d: %v", job, err)
	}
	return d
}

func (e *env) audits(action string) []audit.Event {
	e.t.Helper()
	pg, err := audit.New(e.db).List(e.ctx, audit.Filter{Action: action, Limit: 200})
	if err != nil {
		e.t.Fatal(err)
	}
	return pg.Items
}

func codeOf(err error) string {
	if r, ok := err.(*Refusal); ok {
		return r.Code
	}
	if err == nil {
		return ""
	}
	return "error: " + err.Error()
}
