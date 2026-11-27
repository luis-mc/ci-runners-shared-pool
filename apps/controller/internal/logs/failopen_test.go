package logs

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu    sync.Mutex
	lines []Line
	err   error
	block chan struct{}
}

func (s *fakeStore) Insert(ctx context.Context, l []Line) error {
	if s.block != nil {
		<-s.block
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.lines = append(s.lines, l...)
	return nil
}
func (s *fakeStore) Search(context.Context, Query) (Page, error)            { return Page{}, nil }
func (s *fakeStore) Prune(context.Context, time.Time, int64) (int64, error) { return 0, nil }

// TestFullStoreDoesNotBlockJobs: a store that fails, or one that never answers,
// does not make Add wait or fail; the failures and drops are counted (R6.9).
func TestFullStoreDoesNotBlockJobs(t *testing.T) {
	bad := &fakeStore{err: errors.New("database or disk is full")}
	f := NewFailOpen(bad)
	for i := 0; i < 10; i++ {
		f.Add(Line{Message: "x"})
	}
	f.Close()
	st := f.Stats()
	if st.Failures == 0 || st.Dropped != 10 || st.LastError == "" {
		t.Fatalf("stats %+v, want failures counted, 10 dropped and the error", st)
	}

	stuck := &fakeStore{block: make(chan struct{})}
	g := NewFailOpen(stuck)
	start := time.Now()
	for i := 0; i < queueSize*2; i++ {
		g.Add(Line{Message: "y"})
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("Add waited %v on a stuck store", d)
	}
	if g.Stats().Dropped == 0 {
		t.Fatal("a full queue must count dropped lines")
	}
	close(stuck.block)
	g.Close()
}

func TestFailOpenWritesWhenHealthy(t *testing.T) {
	s := &fakeStore{}
	f := NewFailOpen(s)
	f.Add(Line{Message: "a"})
	f.Add(Line{Message: "b"})
	f.Close()
	if len(s.lines) != 2 || f.Stats().Failures != 0 {
		t.Fatalf("lines %d stats %+v", len(s.lines), f.Stats())
	}
}
