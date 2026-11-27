package demand

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

type fakeEnv struct {
	repos map[string]Repo
	hosts []Host
}

func (f *fakeEnv) Repo(_ context.Context, name string) (Repo, bool, error) {
	r, ok := f.repos[name]
	return r, ok, nil
}
func (f *fakeEnv) Hosts(context.Context) ([]Host, error) { return f.hosts, nil }

type fakeRunners struct {
	mu        sync.Mutex
	started   []runners.JobRunner
	finished  []string
	cleared   []string
	removed   []string
	removeErr error
	running   map[string]int
	failNext  error
	// ceiling is the ephemeral ceiling of every machine; 0 leaves it wide open
	// (the job slots alone limit), noCeiling makes it zero.
	ceiling   int
	noCeiling bool
}

func (f *fakeRunners) StartJobRunner(_ context.Context, j runners.JobRunner, _ string) (runners.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext != nil {
		err := f.failNext
		f.failNext = nil
		return runners.Record{}, err
	}
	f.started = append(f.started, j)
	f.running[j.MachineID]++
	return runners.Record{ID: "r_" + j.Name, Name: j.Name}, nil
}
func (f *fakeRunners) ClearFailedStarts(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, key)
	return nil
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
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, id)
	return nil
}
func (f *fakeRunners) EphemeralOnMachine(_ context.Context, m string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[m], nil
}

func (f *fakeRunners) EphemeralCeiling(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.noCeiling:
		return 0, nil
	case f.ceiling > 0:
		return f.ceiling, nil
	}
	return 1000, nil
}

func newLoop(slots int) (*Loop, *fakeRunners, *time.Time) {
	now := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	fr := &fakeRunners{running: map[string]int{}}
	l := &Loop{
		Env: &fakeEnv{
			repos: map[string]Repo{"alice/demo": {Scope: runners.Scope{Kind: "repository", RepositoryID: 7}}},
			hosts: []Host{{MachineID: "m1", Arch: "x64", Slots: slots}},
		},
		Runners: fr, PoolName: "crsp-home", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now },
	}
	return l, fr, &now
}

func queued(id int64, labels ...string) github.Job {
	return github.Job{ID: id, Repository: "alice/demo", Status: "queued", Labels: labels, CreatedAt: time.Date(2026, 11, 15, 9, 0, int(id), 0, time.UTC)}
}

var linuxLabels = []string{"self-hosted", "Linux", "X64"}

// R5.37: a queued job whose labels a container runner honours gets one
// ephemeral runner, once, with the labels of the job.
func TestQueuedJobStartsOneRunner(t *testing.T) {
	l, fr, _ := newLoop(2)
	ctx := context.Background()
	j := queued(1, linuxLabels...)
	l.Observe(ctx, j)
	l.Dispatch(ctx)
	l.Dispatch(ctx)
	l.Observe(ctx, j) // the poller repeats it
	l.Dispatch(ctx)
	if len(fr.started) != 1 {
		t.Fatalf("started %d runners, want 1", len(fr.started))
	}
	s := fr.started[0]
	if s.Name != "crsp-job-1-1" || s.MachineID != "m1" || s.Backend != "docker" || s.Scope.RepositoryID != 7 ||
		!reflect.DeepEqual(s.Labels, []string{"linux", "self-hosted", "x64"}) {
		t.Fatalf("runner request %+v", s)
	}
}

// R5.37: labels no container runner can honour start nothing.
func TestLabelsThatDoNotMatchStartNothing(t *testing.T) {
	for name, labels := range map[string][]string{
		"hosted":     {"ubuntu-latest"},
		"windows":    {"self-hosted", "windows"},
		"arm":        {"self-hosted", "linux", "arm64"},
		"project":    {"self-hosted", "linux", "my-project"},
		"kvm":        {"self-hosted", "linux", "kvm"},
		"no linux":   {"self-hosted"},
		"no selfhst": {"linux"},
	} {
		l, fr, _ := newLoop(2)
		l.Observe(context.Background(), queued(1, labels...))
		l.Dispatch(context.Background())
		if len(fr.started) != 0 {
			t.Errorf("%s: started %+v", name, fr.started)
		}
	}
	got, why := Match([]string{"self-hosted", "linux", "docker", "Crsp-Home"}, "x64", "crsp-home")
	if why != "" || !reflect.DeepEqual(got, []string{"crsp-home", "docker", "linux", "self-hosted", "x64"}) {
		t.Fatalf("pool and docker labels: %v %q", got, why)
	}
}

// CRSP50: never more ephemeral runners than the machine's job slots; a job
// waits and goes when a runner ends.
func TestSlotBudgetHoldsJobsBack(t *testing.T) {
	l, fr, _ := newLoop(2)
	ctx := context.Background()
	for i := int64(1); i <= 3; i++ {
		l.Observe(ctx, queued(i, linuxLabels...))
	}
	l.Dispatch(ctx)
	if len(fr.started) != 2 || l.Waiting() != 1 {
		t.Fatalf("started %d, waiting %d", len(fr.started), l.Waiting())
	}
	if fr.started[0].Name != "crsp-job-1-1" || fr.started[1].Name != "crsp-job-2-1" {
		t.Fatalf("older jobs go first: %+v", fr.started)
	}
	// Job 1 ends on its runner: that runner is cleaned up and job 3 starts.
	fr.running["m1"]--
	l.Observe(ctx, github.Job{ID: 1, Repository: "alice/demo", Status: "completed", RunnerName: "crsp-job-1-1"})
	l.Dispatch(ctx)
	if !reflect.DeepEqual(fr.finished, []string{"r_crsp-job-1-1"}) || len(fr.started) != 3 || fr.started[2].Name != "crsp-job-3-1" {
		t.Fatalf("finished %v started %+v", fr.finished, fr.started)
	}
}

func TestNoSlotsNoRunner(t *testing.T) {
	l, fr, _ := newLoop(0)
	l.Observe(context.Background(), queued(1, linuxLabels...))
	l.Dispatch(context.Background())
	if len(fr.started) != 0 || l.Waiting() != 1 {
		t.Fatalf("started %d waiting %d", len(fr.started), l.Waiting())
	}
}

// A machine that is not on the allow list is not in Hosts, so nothing starts;
// a repository with the pool off starts nothing either.
func TestNoHostOrPoolOffStartsNothing(t *testing.T) {
	l, fr, _ := newLoop(2)
	l.Env.(*fakeEnv).hosts = nil
	l.Observe(context.Background(), queued(1, linuxLabels...))
	l.Dispatch(context.Background())
	l2, fr2, _ := newLoop(2)
	j := queued(1, linuxLabels...)
	j.Repository = "alice/other"
	l2.Observe(context.Background(), j)
	l2.Dispatch(context.Background())
	if len(fr.started)+len(fr2.started) != 0 {
		t.Fatal("started a runner")
	}
}

// R5.26: a fork pull request waits unless the policy allows it.
func TestForkPullRequest(t *testing.T) {
	l, fr, _ := newLoop(2)
	j := queued(1, linuxLabels...)
	j.FromFork = true
	l.Observe(context.Background(), j)
	l.Dispatch(context.Background())
	if len(fr.started) != 0 {
		t.Fatal("fork job got a runner")
	}
	l.Env.(*fakeEnv).repos["alice/demo"] = Repo{Scope: runners.Scope{Kind: "repository", RepositoryID: 7}, AllowFork: true}
	l.Dispatch(context.Background())
	if len(fr.started) != 1 {
		t.Fatal("allowed fork job got no runner")
	}
}

// A job taken by another runner stops waiting; a failed start backs off and
// is tried again, then given up after five tries.
func TestTakenJobAndFailedStart(t *testing.T) {
	ctx := context.Background()
	l, fr, now := newLoop(2)
	l.Observe(ctx, queued(1, linuxLabels...))
	l.Observe(ctx, github.Job{ID: 1, Repository: "alice/demo", Status: "in_progress", RunnerName: "persistent-1"})
	l.Dispatch(ctx)
	if len(fr.started) != 0 {
		t.Fatal("a job another runner took got a runner")
	}
	l.Observe(ctx, queued(2, linuxLabels...))
	fr.failNext = errors.New("engine down")
	l.Dispatch(ctx)
	l.Dispatch(ctx) // inside the back-off: no new try
	if len(fr.started) != 0 || l.Waiting() != 1 {
		t.Fatalf("started %d waiting %d", len(fr.started), l.Waiting())
	}
	*now = now.Add(time.Minute)
	l.Dispatch(ctx)
	if len(fr.started) != 1 || l.Waiting() != 0 {
		t.Fatalf("retry: started %d waiting %d", len(fr.started), l.Waiting())
	}
	for i := 0; i < maxFails; i++ {
		fr.failNext = errors.New("engine down")
		l.Observe(ctx, queued(3, linuxLabels...))
		l.Dispatch(ctx)
		*now = now.Add(time.Hour)
	}
	if l.Waiting() != 0 {
		t.Fatalf("a job that failed %d times still waits", maxFails)
	}
}

// A runner that ended a job it was not made for sends its own job back to waiting.
func TestRunnerTookAnotherJob(t *testing.T) {
	l, fr, _ := newLoop(2)
	ctx := context.Background()
	l.Observe(ctx, queued(1, linuxLabels...))
	l.Observe(ctx, queued(2, linuxLabels...))
	l.Dispatch(ctx)
	// Runner of job 2 ran job 1; job 2 is still queued.
	l.Observe(ctx, github.Job{ID: 1, Repository: "alice/demo", Status: "completed", RunnerName: "crsp-job-2-1"})
	if l.Waiting() != 1 || len(fr.finished) != 1 || fr.finished[0] != "r_crsp-job-2-1" {
		t.Fatalf("waiting %d finished %v", l.Waiting(), fr.finished)
	}
}

func TestRunReadsTheChannel(t *testing.T) {
	l, fr, _ := newLoop(2)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan github.Job, 1)
	ch <- queued(1, linuxLabels...)
	close(ch)
	l.Run(ctx, ch)
	cancel()
	if len(fr.started) != 1 {
		t.Fatalf("started %d", len(fr.started))
	}
}

func startOne(t *testing.T) (*Loop, *fakeRunners, *time.Time) {
	t.Helper()
	l, fr, now := newLoop(2)
	ctx := context.Background()
	l.Observe(ctx, queued(1, linuxLabels...))
	l.Dispatch(ctx)
	if len(fr.started) != 1 {
		t.Fatal("no runner started")
	}
	return l, fr, now
}

// Question 35: a runner with no job for 15 minutes is removed.
func TestIdleRunnerRemovedAfterFifteenMinutes(t *testing.T) {
	l, fr, now := startOne(t)
	ctx := context.Background()
	*now = now.Add(14 * time.Minute)
	l.SweepIdle(ctx)
	if len(fr.removed) != 0 {
		t.Fatal("removed too early")
	}
	*now = now.Add(2 * time.Minute)
	l.SweepIdle(ctx)
	if len(fr.removed) != 1 || fr.removed[0] != "r_crsp-job-1-1" {
		t.Fatalf("removed %v", fr.removed)
	}
	l.SweepIdle(ctx)
	if len(fr.removed) != 1 {
		t.Fatalf("removed twice: %v", fr.removed)
	}
	// The job it was made for is still queued, so it waits again.
	if l.Waiting() != 1 {
		t.Fatalf("waiting %d", l.Waiting())
	}
}

func TestRunnerWithAJobIsNotIdle(t *testing.T) {
	l, fr, now := startOne(t)
	ctx := context.Background()
	l.Observe(ctx, github.Job{ID: 1, Repository: "alice/demo", Status: "in_progress", RunnerName: "crsp-job-1-1"})
	*now = now.Add(time.Hour)
	l.SweepIdle(ctx)
	if len(fr.removed) != 0 {
		t.Fatalf("removed a runner with a job: %v", fr.removed)
	}
}

func TestBusyRunnerIsLeftAlone(t *testing.T) {
	l, fr, now := startOne(t)
	ctx := context.Background()
	fr.removeErr = runners.ErrRunnerBusy
	*now = now.Add(20 * time.Minute)
	l.SweepIdle(ctx)
	fr.removeErr = nil
	l.SweepIdle(ctx)
	if len(fr.removed) != 0 {
		t.Fatalf("removed a busy runner: %v", fr.removed)
	}
}

func TestFailedIdleRemovalRetriesLater(t *testing.T) {
	l, fr, now := startOne(t)
	ctx := context.Background()
	fr.removeErr = errors.New("github down")
	*now = now.Add(20 * time.Minute)
	l.SweepIdle(ctx)
	fr.removeErr = nil
	l.SweepIdle(ctx) // inside the retry wait
	if len(fr.removed) != 0 {
		t.Fatal("retried at once")
	}
	*now = now.Add(2 * time.Minute)
	l.SweepIdle(ctx)
	if len(fr.removed) != 1 {
		t.Fatalf("removed %v", fr.removed)
	}
}

func TestIdleRunnerSlotIsFreed(t *testing.T) {
	l, fr, now := startOne(t)
	*now = now.Add(16 * time.Minute)
	l.SweepIdle(context.Background())
	if len(fr.removed) != 1 {
		t.Fatal("not removed")
	}
}

// R5.73: a job that finished or was cancelled after its last event is never
// given a runner; one API read settles a stale event.
func TestFinishedOrCancelledJobStartsNoRunner(t *testing.T) {
	for _, status := range []string{"completed", "in_progress"} {
		l, fr, now := newLoop(2)
		reads := 0
		l.ReadJob = func(_ context.Context, inst int64, owner, repo string, id int64) (github.Job, error) {
			reads++
			if owner != "alice" || repo != "demo" || id != 1 {
				t.Errorf("read %s/%s %d", owner, repo, id)
			}
			return github.Job{ID: 1, Status: status, Conclusion: "cancelled"}, nil
		}
		ctx := context.Background()
		l.Observe(ctx, queued(1, linuxLabels...))
		*now = now.Add(time.Minute) // the queued event is old; the webhook for the end never came
		l.Dispatch(ctx)
		l.Dispatch(ctx)
		if len(fr.started) != 0 {
			t.Fatalf("%s: started %+v", status, fr.started)
		}
		if reads != 1 || l.Waiting() != 0 {
			t.Fatalf("%s: reads %d waiting %d, want 1 and 0", status, reads, l.Waiting())
		}
	}
}

// R5.73: a fresh event is trusted without an API read; a stale one that the
// read confirms as queued starts one runner; a read that fails starts none.
func TestRecheckOnlyForOldEventsAndFailsClosed(t *testing.T) {
	l, fr, now := newLoop(2)
	reads := 0
	var readErr error
	l.ReadJob = func(context.Context, int64, string, string, int64) (github.Job, error) {
		reads++
		if readErr != nil {
			return github.Job{}, readErr
		}
		return github.Job{ID: 1, Status: "queued"}, nil
	}
	ctx := context.Background()
	l.Observe(ctx, queued(1, linuxLabels...))
	*now = now.Add(time.Minute)
	readErr = errors.New("boom")
	l.Dispatch(ctx)
	if len(fr.started) != 0 || reads != 1 || l.Waiting() != 1 {
		t.Fatalf("failed read: started %d reads %d waiting %d", len(fr.started), reads, l.Waiting())
	}
	readErr = nil
	l.Dispatch(ctx)
	if len(fr.started) != 1 || reads != 2 {
		t.Fatalf("confirmed read: started %d reads %d", len(fr.started), reads)
	}
	// A fresh event starts without a read.
	l2, fr2, _ := newLoop(2)
	n := 0
	l2.ReadJob = func(context.Context, int64, string, string, int64) (github.Job, error) {
		n++
		return github.Job{Status: "completed"}, nil
	}
	l2.Observe(ctx, queued(2, linuxLabels...))
	l2.Dispatch(ctx)
	if len(fr2.started) != 1 || n != 0 {
		t.Fatalf("fresh event: started %d reads %d", len(fr2.started), n)
	}
}

// CRSP82: a job from a webhook does not say whether it comes from a fork, so
// it gets no runner until the poller read its run; the webhook never wipes
// what the poller read.
func TestWebhookJobWaitsForForkFlag(t *testing.T) {
	ctx := context.Background()
	l, fr, _ := newLoop(2)
	hook := queued(1, linuxLabels...)
	hook.Partial = true
	l.Observe(ctx, hook)
	l.Dispatch(ctx)
	if len(fr.started) != 0 {
		t.Fatal("a job of unknown origin got a runner")
	}
	full := queued(1, linuxLabels...)
	full.Event, full.FromFork = "pull_request", true
	l.Observe(ctx, full)
	l.Observe(ctx, hook) // a later webhook for the same job
	l.Dispatch(ctx)
	if len(fr.started) != 0 {
		t.Fatal("the webhook wiped the fork flag: a fork pull request got a runner")
	}
	ok := queued(2, linuxLabels...)
	ok.Event = "push"
	l.Observe(ctx, ok)
	l.Dispatch(ctx)
	if len(fr.started) != 1 {
		t.Fatalf("a push job of a known origin got %d runners", len(fr.started))
	}
}

// OPS89: each try has its own name, so GitHub never meets the name of a
// registration an earlier try left (409), and all tries share one job key.
func TestEachTryHasItsOwnName(t *testing.T) {
	l, fr, now := newLoop(2)
	ctx := context.Background()
	l.Observe(ctx, queued(5, linuxLabels...))
	fr.failNext = errors.New("engine down")
	l.Dispatch(ctx)
	*now = now.Add(time.Hour)
	l.Dispatch(ctx)
	if len(fr.started) != 1 || fr.started[0].Name != "crsp-job-5-2" || fr.started[0].JobKey != "crsp-job-5-" {
		t.Fatalf("second try = %+v", fr.started)
	}
}

// OPS89: after maxFails tries a new queued event does not start the loop again,
// and the failed rows of the job are cleared when the job ends.
func TestRetryLimitHoldsAndFailedRowsClear(t *testing.T) {
	l, fr, now := newLoop(2)
	ctx := context.Background()
	for i := 0; i < maxFails; i++ {
		l.Observe(ctx, queued(6, linuxLabels...))
		fr.failNext = errors.New("engine down")
		l.Dispatch(ctx)
		*now = now.Add(time.Hour)
	}
	l.Observe(ctx, queued(6, linuxLabels...)) // the poller repeats it
	l.Dispatch(ctx)
	if len(fr.started) != 0 || l.Waiting() != 0 {
		t.Fatalf("started %d waiting %d after the limit", len(fr.started), l.Waiting())
	}
	l.Observe(ctx, github.Job{ID: 6, Repository: "alice/demo", Status: "completed"})
	if len(fr.cleared) != 1 || fr.cleared[0] != "crsp-job-6-" {
		t.Fatalf("cleared = %v", fr.cleared)
	}
}
