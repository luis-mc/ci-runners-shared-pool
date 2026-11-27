package logs

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Writer queue and flush sizes (R6.9).
const (
	queueSize     = 4096
	flushEvery    = 250 * time.Millisecond
	flushMaxLines = 256
	writeTimeout  = 5 * time.Second
)

// Stats is what the doctor shows about the log store (R6.9).
type Stats struct {
	// Failures counts failed store writes (each loses its batch).
	Failures int64
	// Dropped counts lines lost because the queue was full or a write failed.
	Dropped int64
	// LastError is the text of the last failure, "" when none.
	LastError string
	LastAt    time.Time
}

// FailOpen writes lines to a Store without ever blocking or failing the caller
// (R6.9): Add puts a line in a bounded queue and returns at once; one goroutine
// writes batches. A failed write or a full queue is counted, never returned.
// The audit log does not use it: it fails closed (R5.25).
type FailOpen struct {
	store Store
	ch    chan Line
	done  chan struct{}
	stop  sync.Once

	failures atomic.Int64
	dropped  atomic.Int64
	mu       sync.Mutex
	lastErr  string
	lastAt   time.Time
	now      func() time.Time
}

// NewFailOpen starts the writer. Close flushes what is queued.
func NewFailOpen(s Store) *FailOpen {
	f := &FailOpen{store: s, ch: make(chan Line, queueSize), done: make(chan struct{}), now: time.Now}
	go f.run()
	return f
}

// Add queues a line. It never blocks.
func (f *FailOpen) Add(l Line) {
	defer func() {
		if recover() != nil { // a send after Close
			f.dropped.Add(1)
		}
	}()
	select {
	case f.ch <- l:
	default:
		f.dropped.Add(1)
	}
}

// Stats returns the counters for the doctor.
func (f *FailOpen) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Stats{Failures: f.failures.Load(), Dropped: f.dropped.Load(), LastError: f.lastErr, LastAt: f.lastAt}
}

// Close stops the writer after it wrote the queue.
func (f *FailOpen) Close() {
	f.stop.Do(func() { close(f.ch) })
	<-f.done
}

func (f *FailOpen) run() {
	defer close(f.done)
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	var batch []Line
	flush := func() {
		if len(batch) == 0 {
			return
		}
		f.write(batch)
		batch = batch[:0]
	}
	for {
		select {
		case l, ok := <-f.ch:
			if !ok {
				flush()
				return
			}
			batch = append(batch, l)
			if len(batch) >= flushMaxLines {
				flush()
			}
		case <-tick.C:
			flush()
		}
	}
}

func (f *FailOpen) write(batch []Line) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil { // a store that panics is a failed write too
			f.fail(batch, "panic in the log store")
		}
	}()
	if err := f.store.Insert(ctx, batch); err != nil {
		f.fail(batch, err.Error())
	}
}

func (f *FailOpen) fail(batch []Line, msg string) {
	f.failures.Add(1)
	f.dropped.Add(int64(len(batch)))
	f.mu.Lock()
	f.lastErr, f.lastAt = msg, f.now()
	f.mu.Unlock()
}
