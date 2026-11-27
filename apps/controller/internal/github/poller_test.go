package github

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
)

type pollFake struct {
	mu    sync.Mutex
	jobs  []Job
	err   error
	calls int
	bg    bool
}

func (p *pollFake) ListOpenJobs(ctx context.Context, _ int64, _, _ string) ([]Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.bg = isBackground(ctx)
	return append([]Job(nil), p.jobs...), p.err
}
func (p *pollFake) set(j ...Job) { p.mu.Lock(); p.jobs = j; p.mu.Unlock() }

// pollerHarness runs the poller with a Sleep that hands control to the test.
func pollerHarness(t *testing.T, c OpenJobsLister) (*Poller, <-chan Job, chan time.Duration, context.CancelFunc) {
	t.Helper()
	sleeps := make(chan time.Duration)
	next := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	p := &Poller{Client: c, Interval: 30 * time.Second, MaxInterval: 5 * time.Minute,
		Targets: func(context.Context) ([]PollTarget, error) { return []PollTarget{{10, "a", "b"}}, nil },
		Sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case sleeps <- d:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case <-next:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Now: time.Now}
	ch, err := p.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	tick := make(chan time.Duration)
	go func() {
		for d := range sleeps {
			tick <- d
			select {
			case next <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return p, ch, tick, cancel
}

func recv(t *testing.T, ch <-chan Job) Job {
	t.Helper()
	select {
	case j := <-ch:
		return j
	case <-time.After(3 * time.Second):
		t.Fatal("no job")
		return Job{}
	}
}

func TestPollerEmitsNewChangedAndGone(t *testing.T) { // R5.37
	pf := &pollFake{}
	pf.set(Job{ID: 1, Repository: "a/b", Status: "queued"})
	_, ch, tick, _ := pollerHarness(t, pf)
	if j := recv(t, ch); j.ID != 1 || j.Status != "queued" {
		t.Fatalf("%+v", j)
	}
	if d := <-tick; d != 30*time.Second {
		t.Fatalf("after a change the interval is the base: %v", d)
	}
	// Same job unchanged: nothing emitted, interval backs off.
	if d := <-tick; d != 60*time.Second {
		t.Fatalf("idle back-off: %v", d)
	}
	pf.set(Job{ID: 1, Repository: "a/b", Status: "in_progress", RunnerName: "r"})
	<-tick
	if j := recv(t, ch); j.Status != "in_progress" {
		t.Fatalf("%+v", j)
	}
	pf.set() // job gone
	<-tick
	if j := recv(t, ch); j.ID != 1 || j.Status != "completed" {
		t.Fatalf("gone job must be reported completed: %+v", j)
	}
	pf.mu.Lock()
	bg := pf.bg
	pf.mu.Unlock()
	if !bg {
		t.Fatal("polling is background work (R6.6)")
	}
}

func TestPollerBackoffCaps(t *testing.T) { // R5.37
	pf := &pollFake{}
	_, _, tick, _ := pollerHarness(t, pf)
	var last time.Duration
	for i := 0; i < 8; i++ {
		last = <-tick
	}
	if last != 5*time.Minute {
		t.Fatalf("back-off must cap at the maximum, got %v", last)
	}
}

func TestPollerWaitsOutRateLimit(t *testing.T) { // R6.6
	pf := &pollFake{err: &RateLimitError{ResetAt: time.Now().Add(10 * time.Minute)}}
	_, _, tick, _ := pollerHarness(t, pf)
	d := <-tick
	if d < 9*time.Minute || d > 10*time.Minute+time.Second {
		t.Fatalf("must wait until the reset GitHub gave: %v", d)
	}
}

func TestPollerErrorsReportedAndKeepGoing(t *testing.T) {
	pf := &pollFake{err: errors.New("boom")}
	var got error
	p := &Poller{Client: pf, Targets: func(context.Context) ([]PollTarget, error) { return nil, errors.New("targets down") },
		OnError: func(err error) { got = err },
		Sleep:   func(ctx context.Context, _ time.Duration) error { return context.Canceled }}
	ch, err := p.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if got == nil {
		t.Fatal("round error must be reported")
	}
}

func TestPollerNeedsClient(t *testing.T) {
	if _, err := (&Poller{}).Jobs(context.Background()); err == nil {
		t.Fatal("must refuse")
	}
}

func TestPollerAgainstFake(t *testing.T) {
	e := newEnv(t)
	e.fake.AddInstall(10, "alice", "User")
	e.fake.AddRepo(100, 10, "alice", "demo", false)
	e.fake.AddJob(fake.Job{ID: 5, RunID: 50, Repo: "alice/demo", Name: "build", Status: "queued"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Poller{Client: e.client, Interval: time.Hour,
		Targets: func(context.Context) ([]PollTarget, error) { return []PollTarget{{10, "alice", "demo"}}, nil }}
	ch, err := p.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j := recv(t, ch); j.ID != 5 || j.Repository != "alice/demo" {
		t.Fatalf("%+v", j)
	}
}

func TestPollerLastDemandIsTheLastQueuedJobListed(t *testing.T) { // doctor demand.quiet
	c := &pollFake{}
	p, ch, tick, _ := pollerHarness(t, c)
	<-tick // first round: nothing open
	if !p.LastDemand("A/B").IsZero() {
		t.Fatal("no demand yet")
	}
	c.set(Job{ID: 1, Repository: "a/b", Status: "in_progress"})
	<-tick
	<-ch
	if !p.LastDemand("a/b").IsZero() {
		t.Fatal("an in-progress job is not demand")
	}
	c.set(Job{ID: 2, Repository: "a/b", Status: "queued"})
	<-tick
	<-ch
	if p.LastDemand("A/B").IsZero() {
		t.Fatal("a queued job is demand, whatever the case of the name")
	}
}
