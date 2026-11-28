package live

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// fakeStore counts reads and can slow or fail them. The embedded nil Store panics on any method the tests do not expect.
type fakeStore struct {
	jobsdata.Store
	groupReads, statsReads atomic.Int64
	fail                   atomic.Bool
	writes                 atomic.Int64
	gate                   atomic.Pointer[chan struct{}] // when set, Groups waits for it to close
	blocked                atomic.Int64
	rows                   atomic.Int64 // what Groups reports as Done, so a rebuild is visible
}

func (f *fakeStore) UpsertJob(context.Context, jobsdata.Job) error {
	f.writes.Add(1)
	f.rows.Add(1)
	return nil
}
func (f *fakeStore) SetFacts(context.Context, int64, jobsdata.Facts) error {
	f.writes.Add(1)
	return nil
}
func (f *fakeStore) Groups(context.Context, jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	f.groupReads.Add(1)
	if g := f.gate.Load(); g != nil {
		f.blocked.Add(1)
		<-*g
	}
	if f.fail.Load() {
		return nil, errors.New("boom")
	}
	return []jobsdata.GroupRow{{Name: "r", Done: int(f.rows.Load())}}, nil
}
func (f *fakeStore) Stats(context.Context, int, jobsdata.JobFilter) (jobsdata.Stats, error) {
	f.statsReads.Add(1)
	return jobsdata.Stats{Days: []jobsdata.Day{{Jobs: int(f.rows.Load())}}}, nil
}

type pub struct {
	mu  sync.Mutex
	got []events.Type
	ids []string
}

func (p *pub) Publish(t events.Type, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, t)
	p.ids = append(p.ids, string(data))
}
func (p *pub) types() []events.Type {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]events.Type(nil), p.got...)
}

func enc(j jobsdata.Job) []byte {
	return []byte(`{"id":` + itoa(j.ID) + `,"run_id":` + itoa(j.RunID) + `}`)
}
func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); runtime.Gosched() {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out: %s", what)
}

func newStore(f *fakeStore, p events.Publisher) *Store {
	return New(Options{Store: f, Events: p, Encode: enc, Every: 10 * time.Millisecond})
}

// The publisher: one event per state change, with the run and job ids, after the write.
func TestPublishesOneEventPerStateChange(t *testing.T) {
	p := &pub{}
	s := newStore(&fakeStore{}, p)
	defer s.Close()
	ctx := context.Background()
	j := jobsdata.Job{ID: 7, RunID: 70, State: jobsdata.Queued}
	for _, st := range []jobsdata.State{jobsdata.Queued, jobsdata.Queued, jobsdata.Running, jobsdata.Running, jobsdata.Done, jobsdata.Done} {
		j.State = st
		if err := s.UpsertJob(ctx, j); err != nil {
			t.Fatal(err)
		}
	}
	want := []events.Type{events.JobQueued, events.JobStarted, events.JobFinished}
	got := p.types()
	if len(got) != 3 {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	if p.ids[0] != `{"id":7,"run_id":70}` {
		t.Fatalf("payload = %s", p.ids[0])
	}
}

type failWrite struct{ fakeStore }

func (*failWrite) UpsertJob(context.Context, jobsdata.Job) error { return errors.New("disk full") }

func TestNoEventWhenTheWriteFails(t *testing.T) {
	p := &pub{}
	s := New(Options{Store: &failWrite{}, Events: p, Encode: enc, Every: time.Millisecond})
	defer s.Close()
	if err := s.UpsertJob(context.Background(), jobsdata.Job{ID: 1, State: jobsdata.Queued}); err == nil {
		t.Fatal("want the write error")
	}
	if n := len(p.types()); n != 0 {
		t.Fatalf("%d events for a rolled-back write", n)
	}
}

func TestNilPublisherIsQuiet(t *testing.T) {
	s := New(Options{Store: &fakeStore{}, Every: time.Millisecond})
	defer s.Close()
	if err := s.UpsertJob(context.Background(), jobsdata.Job{ID: 1, State: jobsdata.Done}); err != nil {
		t.Fatal(err)
	}
}

// The cache is built at start, so the first pages never scan.
func TestDefaultsAreBuiltAtStart(t *testing.T) {
	f := &fakeStore{}
	s := newStore(f, nil)
	defer s.Close()
	eventually(t, "first build", func() bool { return s.Rebuilds() >= 1 })
	before := f.groupReads.Load()
	for _, by := range []jobsdata.GroupBy{jobsdata.ByRepository, jobsdata.ByMachine, jobsdata.ByRunner, jobsdata.ByRunnerType, jobsdata.ByJobType} {
		if _, err := s.Groups(context.Background(), by); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Stats(context.Background(), DefaultStatsDays, jobsdata.JobFilter{Limit: 50, After: 9}); err != nil {
		t.Fatal(err)
	}
	if f.groupReads.Load() != before {
		t.Fatalf("a page read the store: %d reads after the build, %d before", f.groupReads.Load(), before)
	}
	if f.statsReads.Load() != 1 {
		t.Fatalf("stats reads = %d, want 1", f.statsReads.Load())
	}
}

// Many pages asking a cold filter at once cost one store read.
func TestColdReadIsSingleFlight(t *testing.T) {
	f := &fakeStore{}
	s := newStore(f, nil)
	defer s.Close()
	eventually(t, "first build", func() bool { return s.Rebuilds() >= 1 })
	f.statsReads.Store(0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Stats(context.Background(), 7, jobsdata.JobFilter{RepositoryID: 3}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.statsReads.Load(); n != 1 {
		t.Fatalf("stats reads = %d, want 1", n)
	}
}

// While a rebuild is stuck in the store, pages still answer from memory at once.
func TestReadsDoNotWaitForARebuild(t *testing.T) {
	f := &fakeStore{}
	s := newStore(f, nil)
	defer s.Close()
	ctx := context.Background()
	eventually(t, "first build", func() bool { return s.Rebuilds() >= 1 })
	gate := make(chan struct{})
	f.gate.Store(&gate)
	if err := s.UpsertJob(ctx, jobsdata.Job{ID: 1, State: jobsdata.Running}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the rebuild is inside the store", func() bool { return f.blocked.Load() > 0 })
	var wg sync.WaitGroup
	answered := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Groups(ctx, jobsdata.ByRepository); err != nil {
				t.Error(err)
			}
		}()
	}
	go func() { wg.Wait(); close(answered) }()
	select {
	case <-answered:
	case <-time.After(3 * time.Second):
		t.Fatal("pages waited for the rebuild")
	}
	close(gate)
}

// A burst of writes costs a few rebuilds, and the answer shows all of them.
func TestWritesCoalesceIntoFewRebuilds(t *testing.T) {
	f := &fakeStore{}
	s := newStore(f, nil)
	defer s.Close()
	ctx := context.Background()
	eventually(t, "first build", func() bool { return s.Rebuilds() >= 1 })
	base := s.Rebuilds()
	for i := 0; i < 200; i++ {
		if err := s.UpsertJob(ctx, jobsdata.Job{ID: int64(i), State: jobsdata.Running}); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "a rebuild shows the writes", func() bool {
		rows, _ := s.Groups(ctx, jobsdata.ByRepository)
		return len(rows) == 1 && rows[0].Done == 200
	})
	if n := s.Rebuilds() - base; n > 20 {
		t.Fatalf("200 writes cost %d rebuilds, want a few", n)
	}
}

func TestFailedRebuildKeepsTheLastGoodAnswer(t *testing.T) {
	f := &fakeStore{}
	s := newStore(f, nil)
	defer s.Close()
	ctx := context.Background()
	eventually(t, "first build", func() bool { return s.Rebuilds() >= 1 })
	f.fail.Store(true)
	base := s.Rebuilds()
	_ = s.SetFacts(ctx, 1, jobsdata.Facts{})
	eventually(t, "rebuild attempt", func() bool { return s.Rebuilds() > base })
	rows, err := s.Groups(ctx, jobsdata.ByMachine)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v, want the last good answer", rows, err)
	}
}

func TestFailedColdReadIsRetried(t *testing.T) {
	f := &fakeStore{}
	f.fail.Store(true)
	s := newStore(f, nil)
	defer s.Close()
	if _, err := s.Groups(context.Background(), jobsdata.ByRepository); err == nil {
		t.Fatal("want the store error")
	}
	f.fail.Store(false)
	if _, err := s.Groups(context.Background(), jobsdata.ByRepository); err != nil {
		t.Fatal(err)
	}
}

func TestCloseStopsTheLoop(t *testing.T) {
	s := newStore(&fakeStore{}, nil)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
}
