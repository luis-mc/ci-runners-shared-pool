package builds

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateOneBuildAtATime(t *testing.T) {
	g := NewGate()
	var inside, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := g.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for p := atomic.LoadInt32(&peak); n > p && !atomic.CompareAndSwapInt32(&peak, p, n); p = atomic.LoadInt32(&peak) {
			}
			runtime.Gosched() // stay inside long enough for a second build to try
			atomic.AddInt32(&inside, -1)
			rel()
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("%d builds ran at once", peak)
	}
	if g.Busy() {
		t.Fatal("the gate is still held")
	}
}

func TestGateWaiterEndsWithItsContext(t *testing.T) {
	g := NewGate()
	rel, _ := g.Acquire(context.Background())
	defer rel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx); err == nil {
		t.Fatal("a held gate must make the second asker wait until its context ends")
	}
	rel()
	rel() // twice is safe
	if g.Busy() {
		t.Fatal("released")
	}
}

func TestCoresHalfWhileJobsRun(t *testing.T) {
	for _, c := range []struct {
		total   int
		running bool
		want    int
	}{{8, false, 8}, {8, true, 4}, {7, true, 3}, {1, true, 1}, {0, true, 1}, {0, false, 1}} {
		if got := Cores(c.total, c.running); got != c.want {
			t.Errorf("Cores(%d,%v) = %d, want %d", c.total, c.running, got, c.want)
		}
	}
}
