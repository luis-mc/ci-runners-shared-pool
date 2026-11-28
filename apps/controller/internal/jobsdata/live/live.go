// Package live is the live seam of the Jobs data (observability R5.4, R5.6, R6.2).
//
// Store wraps a jobsdata.Store. After a job write commits it publishes the job's
// event (job.queued, job.started, job.finished, with the run and job ids in the
// job payload) on the controller's event stream, so an open list in the console
// moves without a reload. It also keeps the groups and stats answers in memory
// and rebuilds them in the background after writes, so no page reads the whole
// history while a person waits: with thousands of past jobs and dozens of builds
// writing at once, a page costs one map read, and a burst of writes costs one
// rebuild per gap.
package live

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// Defaults.
const (
	// DefaultEvery is the least gap between two cache rebuilds.
	DefaultEvery = 2 * time.Second
	// maxStatsKeys caps the stats filters kept warm; another filter is read from the store each time.
	maxStatsKeys = 64
	// maxSeen caps the jobs whose last published state is remembered.
	maxSeen = 20000
	// DefaultStatsDays is the window the console asks first.
	DefaultStatsDays = 14
)

// Options configure a Store.
type Options struct {
	// Store is the wrapped store. Required.
	Store jobsdata.Store
	// Events receives the job events; nil publishes nothing.
	Events events.Publisher
	// Encode gives the JSON of a job as the LiveEvent `job` field (api.JobEventJSON); nil publishes nothing.
	Encode func(jobsdata.Job) []byte
	// Every is the least gap between two rebuilds; zero means DefaultEvery.
	Every time.Duration
	// Log receives rebuild failures; nil means slog.Default.
	Log *slog.Logger
}

// Store is a jobsdata.Store that tells the live view about job writes and serves
// Groups and Stats from a cache rebuilt in the background. It is safe for
// concurrent use. Call Close to stop the rebuild goroutine.
type Store struct {
	jobsdata.Store
	opt Options
	log *slog.Logger

	mu     sync.Mutex
	seen   map[int64]jobsdata.State
	groups map[jobsdata.GroupBy]*entry[[]jobsdata.GroupRow]
	stats  map[string]*statsEntry

	dirty    chan struct{}
	cancel   context.CancelFunc
	done     chan struct{}
	rebuilds int
}

// entry is one cached answer. Reads take the read lock only; the first fill is single-flight, so a cold
// page costs one store read however many pages ask at once.
type entry[T any] struct {
	fill  sync.Mutex
	mu    sync.RWMutex
	ready bool
	val   T
}

func (e *entry[T]) get(compute func() (T, error)) (T, error) {
	e.mu.RLock()
	if e.ready {
		v := e.val
		e.mu.RUnlock()
		return v, nil
	}
	e.mu.RUnlock()
	e.fill.Lock()
	defer e.fill.Unlock()
	e.mu.RLock()
	if e.ready {
		v := e.val
		e.mu.RUnlock()
		return v, nil
	}
	e.mu.RUnlock()
	v, err := compute()
	if err != nil {
		var zero T
		return zero, err
	}
	e.set(v)
	return v, nil
}

// set replaces the answer; a failed rebuild never calls it, so the last good answer stays.
func (e *entry[T]) set(v T) {
	e.mu.Lock()
	e.val, e.ready = v, true
	e.mu.Unlock()
}

type statsEntry struct {
	entry[jobsdata.Stats]
	days int
	f    jobsdata.JobFilter
}

var _ jobsdata.Store = (*Store)(nil)

// New wraps a store and starts the background rebuild. The default groups and the
// 14-day stats are built at once, not on the first request.
func New(o Options) *Store {
	if o.Every <= 0 {
		o.Every = DefaultEvery
	}
	s := &Store{Store: o.Store, opt: o, log: o.Log, seen: map[int64]jobsdata.State{},
		groups: map[jobsdata.GroupBy]*entry[[]jobsdata.GroupRow]{}, stats: map[string]*statsEntry{},
		dirty: make(chan struct{}, 1), done: make(chan struct{})}
	if s.log == nil {
		s.log = slog.Default()
	}
	for _, by := range []jobsdata.GroupBy{jobsdata.ByRepository, jobsdata.ByMachine, jobsdata.ByRunner, jobsdata.ByRunnerType, jobsdata.ByJobType} {
		s.groups[by] = &entry[[]jobsdata.GroupRow]{}
	}
	def := jobsdata.JobFilter{}
	s.stats[statsKey(DefaultStatsDays, def)] = &statsEntry{days: DefaultStatsDays, f: def}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.loop(ctx)
	s.markDirty() // the first build starts now
	return s
}

// Close stops the rebuild goroutine and waits for it.
func (s *Store) Close() {
	s.cancel()
	<-s.done
}

// Rebuilds is how many cache rebuilds have finished (tests, diagnostics).
func (s *Store) Rebuilds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rebuilds
}

func (s *Store) markDirty() {
	select {
	case s.dirty <- struct{}{}:
	default: // one is already waiting; it covers this write
	}
}

// UpsertJob writes the job, then publishes its event when its state changed and marks the cache stale.
func (s *Store) UpsertJob(ctx context.Context, j jobsdata.Job) error {
	if err := s.Store.UpsertJob(ctx, j); err != nil {
		return err
	}
	s.publish(j)
	s.markDirty()
	return nil
}

// SetFacts merges crsp's facts, then marks the cache stale (the machine and runner columns changed).
func (s *Store) SetFacts(ctx context.Context, jobID int64, f jobsdata.Facts) error {
	if err := s.Store.SetFacts(ctx, jobID, f); err != nil {
		return err
	}
	s.markDirty()
	return nil
}

// Prune removes old rows, then marks the cache stale.
func (s *Store) Prune(ctx context.Context, now time.Time, r jobsdata.Retention) (jobsdata.Pruned, error) {
	p, err := s.Store.Prune(ctx, now, r)
	if err == nil {
		s.markDirty()
	}
	return p, err
}

func eventOf(st jobsdata.State) (events.Type, bool) {
	switch st {
	case jobsdata.Queued:
		return events.JobQueued, true
	case jobsdata.Running:
		return events.JobStarted, true
	case jobsdata.Done:
		return events.JobFinished, true
	}
	return "", false
}

// publish sends the event of a job's state once per change. The store keeps a job's state from moving
// backward, so a stale write repeats the state that was already told and is dropped here.
func (s *Store) publish(j jobsdata.Job) {
	if s.opt.Events == nil || s.opt.Encode == nil {
		return
	}
	t, ok := eventOf(j.State)
	if !ok {
		return
	}
	s.mu.Lock()
	if prev, had := s.seen[j.ID]; had && prev == j.State {
		s.mu.Unlock()
		return
	}
	if len(s.seen) >= maxSeen {
		s.seen = map[int64]jobsdata.State{} // a repeat after this is harmless: the console replaces by id
	}
	s.seen[j.ID] = j.State
	s.mu.Unlock()
	if b := s.opt.Encode(j); b != nil {
		s.opt.Events.Publish(t, b)
	}
}

func statsKey(days int, f jobsdata.JobFilter) string { return fmt.Sprintf("%d|%+v", days, f) }

// Groups serves the grouped table from memory. Only a read before the first build reads the store.
func (s *Store) Groups(ctx context.Context, by jobsdata.GroupBy) ([]jobsdata.GroupRow, error) {
	s.mu.Lock()
	e := s.groups[by]
	s.mu.Unlock()
	if e == nil { // a grouping the cache does not know
		return s.Store.Groups(ctx, by)
	}
	rows, err := e.get(func() ([]jobsdata.GroupRow, error) { return s.Store.Groups(ctx, by) })
	return append([]jobsdata.GroupRow(nil), rows...), err
}

// Stats serves the chart data from memory for the filters people use; past maxStatsKeys other filters read the store.
func (s *Store) Stats(ctx context.Context, days int, f jobsdata.JobFilter) (jobsdata.Stats, error) {
	f.After, f.Limit = 0, 0 // paging does not change a chart
	key := statsKey(days, f)
	s.mu.Lock()
	e := s.stats[key]
	if e == nil {
		if len(s.stats) >= maxStatsKeys {
			s.mu.Unlock()
			return s.Store.Stats(ctx, days, f)
		}
		e = &statsEntry{days: days, f: f}
		s.stats[key] = e
	}
	s.mu.Unlock()
	return e.get(func() (jobsdata.Stats, error) { return s.Store.Stats(ctx, days, f) })
}

// loop rebuilds after writes. Writes that arrive while it waits or builds are covered by the next build.
func (s *Store) loop(ctx context.Context) {
	defer close(s.done)
	first := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.dirty:
		}
		if !first {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.opt.Every): // let a burst of writes settle
			}
		}
		first = false
		select { // a write during the wait is covered by this build
		case <-s.dirty:
		default:
		}
		s.rebuild(ctx)
	}
}

func (s *Store) rebuild(ctx context.Context) {
	s.mu.Lock()
	gs := make(map[jobsdata.GroupBy]*entry[[]jobsdata.GroupRow], len(s.groups))
	for k, v := range s.groups {
		gs[k] = v
	}
	ss := make([]*statsEntry, 0, len(s.stats))
	for _, v := range s.stats {
		ss = append(ss, v)
	}
	s.mu.Unlock()
	for by, e := range gs {
		if ctx.Err() != nil {
			return
		}
		if rows, err := s.Store.Groups(ctx, by); err == nil {
			e.set(rows)
		} else if ctx.Err() == nil {
			s.log.Warn("rebuild job groups", "by", by, "err", err)
		}
	}
	for _, e := range ss {
		if ctx.Err() != nil {
			return
		}
		if st, err := s.Store.Stats(ctx, e.days, e.f); err == nil {
			e.set(st)
		} else if ctx.Err() == nil {
			s.log.Warn("rebuild job stats", "days", e.days, "err", err)
		}
	}
	s.mu.Lock()
	s.rebuilds++
	s.mu.Unlock()
}
