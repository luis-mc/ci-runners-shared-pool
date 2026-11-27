package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// fakeClock hands out tickers that only tick when the test says so.
type fakeClock struct {
	mu      sync.Mutex
	tickers map[time.Duration]chan time.Time
	made    chan time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{tickers: map[time.Duration]chan time.Time{}, made: make(chan time.Duration, 8)}
}

func (c *fakeClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	ch := make(chan time.Time)
	c.mu.Lock()
	c.tickers[d] = ch
	c.mu.Unlock()
	c.made <- d
	return ch, func() {}
}

func (c *fakeClock) tick(t *testing.T, d time.Duration) {
	t.Helper()
	c.mu.Lock()
	ch := c.tickers[d]
	c.mu.Unlock()
	select {
	case ch <- time.Now():
	case <-time.After(5 * time.Second):
		t.Fatalf("no loop listens for %s", d)
	}
}

// TestRunsOnLoopChecksHourlyAndRestoresEveryTenMinutes: github-integration
// R5.28, R5.30. A fake clock drives both tickers.
func TestRunsOnLoopChecksHourlyAndRestoresEveryTenMinutes(t *testing.T) {
	var mu sync.Mutex
	var restores, checks int
	var checkErr error
	calls := make(chan string, 64)
	ready := true
	tasks := runsOnTasks{
		Ready: func() bool { mu.Lock(); defer mu.Unlock(); return ready },
		Reconcile: []func(context.Context) (string, error){
			func(context.Context) (string, error) {
				mu.Lock()
				restores++
				mu.Unlock()
				calls <- "restore"
				return "restore", nil
			},
		},
		Check: func(context.Context) error {
			mu.Lock()
			checks++
			e := checkErr
			mu.Unlock()
			calls <- "check"
			return e
		},
	}
	clk := newFakeClock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runsOnLoop(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), clk, tasks)
	}()
	waitCall := func(want string) {
		t.Helper()
		select {
		case got := <-calls:
			if got != want {
				t.Fatalf("got %s, want %s", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s call", want)
		}
	}
	// The first round runs at once, then both tickers exist.
	waitCall("restore")
	waitCall("check")
	got := map[time.Duration]bool{}
	for i := 0; i < 2; i++ {
		got[<-clk.made] = true
	}
	if !got[10*time.Minute] || !got[time.Hour] {
		t.Fatalf("tickers %v, want 10 minutes and one hour", got)
	}
	// Ten minutes pass: only the restore runs.
	clk.tick(t, 10*time.Minute)
	waitCall("restore")
	// One hour: the check runs, and an error (a rate limit) is only logged; the loop goes on.
	mu.Lock()
	checkErr = &github.RateLimitError{ResetAt: time.Now().Add(time.Hour)}
	mu.Unlock()
	clk.tick(t, time.Hour)
	waitCall("check")
	mu.Lock()
	checkErr = errors.New("later")
	mu.Unlock()
	clk.tick(t, time.Hour)
	waitCall("check")
	// No GitHub App yet: a tick does nothing.
	mu.Lock()
	ready = false
	mu.Unlock()
	clk.tick(t, time.Hour)
	clk.tick(t, 10*time.Minute)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if restores != 2 || checks != 3 {
		t.Errorf("restores %d checks %d, want 2 and 3", restores, checks)
	}
	if len(calls) != 0 {
		t.Errorf("calls with no App: %d", len(calls))
	}
}
