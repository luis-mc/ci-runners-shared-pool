package scheduler

import (
	"sync"
	"time"
)

// Lifecycle is told when a job's first real step begins and when the job ends,
// for crsp's job and run timers (scheduling R5.99). The first real step is
// when the job-started hook returns: the gate's first go for the job. The
// callbacks run after the engine's transaction, with no lock held, and must not
// block.
type Lifecycle interface {
	// JobStarted is called on every go answer of the job; the receiver starts
	// the clock once. hold is how long the job waited at the gate.
	JobStarted(machineID string, job JobRef, hold time.Duration)
	// JobEnded is called when the job's slots are released.
	JobEnded(machineID string, job JobRef)
}

type lifecycleState struct {
	lifeMu sync.Mutex
	life   Lifecycle
}

// SetLifecycle sets where the engine reports job starts and ends. Nil turns it off.
func (e *Engine) SetLifecycle(l Lifecycle) {
	e.lifeMu.Lock()
	e.life = l
	e.lifeMu.Unlock()
}

func (e *Engine) lifecycle() Lifecycle {
	e.lifeMu.Lock()
	defer e.lifeMu.Unlock()
	return e.life
}

func (e *Engine) lifeStarted(machineID string, job JobRef, d Decision) {
	if len(d.Go) == 0 {
		return
	}
	if l := e.lifecycle(); l != nil {
		l.JobStarted(machineID, job, d.SlotWait)
	}
}

func (e *Engine) lifeEnded(machineID string, job JobRef) {
	if l := e.lifecycle(); l != nil {
		l.JobEnded(machineID, job)
	}
}
