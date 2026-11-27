package demand

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// scriptDemand is a Demand whose every Jobs call is scripted.
type scriptDemand struct {
	mu    sync.Mutex
	calls int
	// open gives the stream of call n (1-based) or an error.
	open func(n int, ctx context.Context) (<-chan github.Job, error)
}

func (s *scriptDemand) Jobs(ctx context.Context) (<-chan github.Job, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	return s.open(n, ctx)
}

func (s *scriptDemand) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.calls }

// streamOf sends the jobs, then waits for ctx (a source that keeps running).
func streamOf(ctx context.Context, jobs ...github.Job) <-chan github.Job {
	ch := make(chan github.Job)
	go func() {
		defer close(ch)
		for _, j := range jobs {
			select {
			case ch <- j:
			case <-ctx.Done():
				return
			}
		}
		<-ctx.Done()
	}()
	return ch
}

func nextJob(t *testing.T, ch <-chan github.Job) github.Job {
	t.Helper()
	select {
	case j, ok := <-ch:
		if !ok {
			t.Fatal("stream closed")
		}
		return j
	case <-time.After(5 * time.Second):
		t.Fatal("no job in time")
	}
	return github.Job{}
}

// R5.40: the scale set enters through github.Demand only; the loop sees its
// jobs like any other source's.
func TestScaleSetBehindDemandSeam(t *testing.T) {
	primary := &scriptDemand{open: func(_ int, ctx context.Context) (<-chan github.Job, error) {
		return streamOf(ctx, github.Job{ID: 7, Status: "queued", ScaleSetID: 5}), nil
	}}
	fallback := &scriptDemand{open: func(int, context.Context) (<-chan github.Job, error) {
		t.Error("the poller must not run while the scale set works")
		return nil, errors.New("unused")
	}}
	var d github.Demand = &Failover{Primary: primary, Fallback: fallback}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := d.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j := nextJob(t, ch); j.ID != 7 || j.ScaleSetID != 5 {
		t.Fatalf("job = %+v", j)
	}
	if fallback.count() != 0 {
		t.Fatal("the fallback started")
	}
}

// R5.40: a primary that cannot open, or drops, makes the poller run; the
// primary is tried again after the back-off, and the back-off doubles.
func TestFallsBackToPollingAndRetries(t *testing.T) {
	var sleeps []time.Duration
	var smu sync.Mutex
	polled := make(chan struct{}) // closed when a polling job reached the test
	states := make(chan string, 16)
	primary := &scriptDemand{open: func(n int, ctx context.Context) (<-chan github.Job, error) {
		switch n {
		case 1:
			return nil, errors.New("session cannot be opened")
		case 2: // opens, then drops at once
			ch := make(chan github.Job)
			close(ch)
			return ch, nil
		}
		return streamOf(ctx, github.Job{ID: 70, Status: "queued"}), nil
	}}
	fallback := &scriptDemand{open: func(_ int, ctx context.Context) (<-chan github.Job, error) {
		return streamOf(ctx, github.Job{ID: 1, Status: "queued"}), nil
	}}
	f := &Failover{Primary: primary, Fallback: fallback, MinBackoff: time.Second, MaxBackoff: time.Minute,
		Why: func() error { return github.ErrScaleSetAPI },
		OnState: func(s, _ string) {
			select {
			case states <- s:
			default:
			}
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			smu.Lock()
			sleeps = append(sleeps, d)
			first := len(sleeps) == 1
			smu.Unlock()
			if first { // the first back-off lasts until the poller's job came through
				select {
				case <-polled:
				case <-ctx.Done():
				}
			}
			return nil
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := f.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Polling jobs come first (from the two fallback rounds), then the scale set's.
	seen := map[int64]bool{}
	for !seen[70] {
		j := nextJob(t, ch)
		if j.ID == 1 && !seen[1] {
			close(polled)
		}
		seen[j.ID] = true
	}
	if !seen[1] {
		t.Fatal("the poller never ran while the scale set was down")
	}
	if primary.count() < 3 {
		t.Fatalf("the primary was tried %d times", primary.count())
	}
	smu.Lock()
	defer smu.Unlock()
	if len(sleeps) < 2 || sleeps[0] != time.Second || sleeps[1] != 2*time.Second {
		t.Fatalf("back-off = %v", sleeps)
	}
	var got []string
	for len(states) > 0 {
		got = append(got, <-states)
	}
	if len(got) == 0 || got[0] != "polling" {
		t.Fatalf("the first state change must say polling: %v", got)
	}
}

// Only one source runs at a time: the one before is stopped before the next
// is opened.
func TestFailoverRunsOneSourceAtATime(t *testing.T) {
	var mu sync.Mutex
	var open []context.Context
	overlap := false
	wrap := func(ch func(n int, ctx context.Context) (<-chan github.Job, error)) *scriptDemand {
		return &scriptDemand{open: func(n int, ctx context.Context) (<-chan github.Job, error) {
			mu.Lock()
			for _, o := range open {
				if o.Err() == nil {
					overlap = true
				}
			}
			open = append(open, ctx)
			mu.Unlock()
			return ch(n, ctx)
		}}
	}
	primary := wrap(func(n int, ctx context.Context) (<-chan github.Job, error) {
		if n < 3 {
			ch := make(chan github.Job)
			close(ch)
			return ch, nil
		}
		return streamOf(ctx, github.Job{ID: 9}), nil
	})
	fallback := wrap(func(_ int, ctx context.Context) (<-chan github.Job, error) { return streamOf(ctx), nil })
	f := &Failover{Primary: primary, Fallback: fallback, MinBackoff: time.Millisecond, Sleep: func(ctx context.Context, _ time.Duration) error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, _ := f.Jobs(ctx)
	if nextJob(t, ch).ID != 9 {
		t.Fatal("expected the scale-set job")
	}
	mu.Lock()
	defer mu.Unlock()
	if overlap {
		t.Fatal("a source was opened while the one before still ran")
	}
}

// R5.40: a runner for a scale-set job takes its JIT config from the scale
// set; a job from another source keeps the repository-level call.
func TestScaleSetJobGetsScaleSetJIT(t *testing.T) {
	l, fr, _ := newLoop(2)
	l.ScaleJIT = func(j github.Job, name string) func(context.Context) (github.JITConfig, error) {
		return func(context.Context) (github.JITConfig, error) {
			_ = name
			return github.NewJITConfig(1, "x"), nil
		}
	}
	ctx := context.Background()
	a := queued(1, linuxLabels...)
	a.ScaleSetID = 5
	l.Observe(ctx, a)
	l.Observe(ctx, queued(2, linuxLabels...))
	l.Dispatch(ctx)
	if len(fr.started) != 2 {
		t.Fatalf("started %d", len(fr.started))
	}
	byName := map[string]bool{}
	for _, s := range fr.started {
		byName[s.Name] = s.JIT != nil
	}
	if !byName["crsp-job-1-1"] || byName["crsp-job-2-1"] {
		t.Fatalf("JIT source by runner = %v", byName)
	}
}
