package ingest

import (
	"log/slog"
	"sync"
)

// Worker runs submitted functions one at a time, in order, on its own
// goroutine. Submit never blocks: when the queue is full the work is dropped and
// logged, so a slow store can never hold the webhook answer or the demand loop.
type Worker struct {
	ch   chan func()
	log  *slog.Logger
	done chan struct{}
	once sync.Once
}

// NewWorker starts a worker with room for size waiting functions.
func NewWorker(size int, log *slog.Logger) *Worker {
	if size < 1 {
		size = 1
	}
	if log == nil {
		log = slog.Default()
	}
	w := &Worker{ch: make(chan func(), size), log: log, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for fn := range w.ch {
			fn()
		}
	}()
	return w
}

// Submit queues fn and reports false when the queue is full or the worker is stopped.
func (w *Worker) Submit(fn func()) (queued bool) {
	defer func() {
		if recover() != nil { // the channel was closed by Stop
			queued = false
		}
	}()
	select {
	case w.ch <- fn:
		return true
	default:
		w.log.Warn("jobs ingest queue is full; an event was dropped")
		return false
	}
}

// Stop lets the queued work finish and ends the goroutine.
func (w *Worker) Stop() {
	w.once.Do(func() { close(w.ch) })
	<-w.done
}
