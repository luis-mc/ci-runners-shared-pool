package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// Engine is the SQLite-backed SlotQueue. Ages are judged on its clock, which
// is the controller clock (R6.4).
type Engine struct {
	store slots.Store
	now   func() time.Time
	log   *slog.Logger
	wake  *waker
	alert func(Alert)
	// accepting says whether a machine takes new jobs and why not (machines R5.62).
	accepting func(machineID string) (ok bool, reason string)
	// unclean gives the reason a runner is unclean after a failed clean-up, or
	// "" (runners R5.91). Nil means every runner is clean.
	unclean func(machineID, runnerName string) string
	// warm lists the warm runners of a machine and warmRelease drains some of
	// them when a real job needs the budget (scheduling R5.66). Both nil: no warm pool.
	warm        func(machineID string) []WarmRunner
	warmRelease func(machineID string, names []string)
	// runnerClass, forecastEnd and zone feed admission (scheduling R5.90 to R5.94).
	runnerClass func(machineID, runnerName string) string
	forecastEnd func(machineID string, job slots.Job, start time.Time) (time.Time, bool)
	zone        *time.Location
	// lifecycleState reports job starts and ends to the timers (SetLifecycle).
	lifecycleState
	// factsState reports job facts to the Jobs section (SetFacts).
	factsState
}

// Option configures an Engine.
type Option func(*Engine)

// WithClock sets the controller clock (tests).
func WithClock(now func() time.Time) Option { return func(e *Engine) { e.now = now } }

// WithLogger sets the logger. Logs never hold secrets (R6.5): only job
// identity, slot numbers and load figures are logged.
func WithLogger(l *slog.Logger) Option { return func(e *Engine) { e.log = l } }

// WithAlerts sets where the engine reports a job that ended without a slot
// (R5.61). The callback must not block; the console and the notifications read
// the alert from there.
func WithAlerts(f func(Alert)) Option { return func(e *Engine) { e.alert = f } }

// WithAccepting sets where the gate reads a machine's acceptance: the agent's
// own report of paused or on battery (scheduling R5.68). A machine
// that is not accepting holds every new job with that reason. A machine the
// callback knows nothing about accepts. The budget reason is not held here:
// the gate checks the live load itself.
func WithAccepting(f func(machineID string) (ok bool, reason string)) Option {
	return func(e *Engine) { e.accepting = f }
}

// SetUncleanLookup sets where the gate reads whether a runner is unclean: the
// reason its last clean-up failed, "" when it is clean. An unclean runner takes
// no new job (runners R5.91). The runner registry answers it.
func (e *Engine) SetUncleanLookup(f func(machineID, runnerName string) string) { e.unclean = f }

// SetRunnerClass sets where the gate reads a runner's size class after the
// engine was built (the runner registry is made later). See WithRunnerClass.
func (e *Engine) SetRunnerClass(f func(machineID, runnerName string) string) { e.runnerClass = f }

// SetZone sets the zone of the times in wait lines (see WithZone).
func (e *Engine) SetZone(z *time.Location) { e.zone = z }

// NewEngine returns the slot queue over a store.
func NewEngine(store slots.Store, opts ...Option) *Engine {
	e := &Engine{store: store, now: func() time.Time { return time.Now().UTC() }, log: slog.Default(), wake: newWaker()}
	for _, o := range opts {
		o(e)
	}
	return e
}

var _ SlotQueue = (*Engine)(nil)

// Wake returns a channel that receives when a slot on the machine is freed
// (R5.17), and a cancel func. A gate service selects on it next to its poll timer.
func (e *Engine) Wake(machineID string) (<-chan struct{}, func()) { return e.wake.subscribe(machineID) }

type waker struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func newWaker() *waker { return &waker{subs: map[string]map[chan struct{}]struct{}{}} }

func (w *waker) subscribe(machine string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	if w.subs[machine] == nil {
		w.subs[machine] = map[chan struct{}]struct{}{}
	}
	w.subs[machine][ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subs[machine], ch)
		w.mu.Unlock()
	}
}

func (w *waker) notify(machine string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for ch := range w.subs[machine] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ---------------------------------------------------------------- conversions

func toJob(j JobRef) slots.Job {
	return slots.Job{Repository: j.Repository, RunID: j.RunID, RunAttempt: j.RunAttempt, Job: j.Job,
		GitHubJobID: j.GitHubJobID, Shard: j.Shard, RunnerName: j.RunnerName}
}

func fromJob(j slots.Job) JobRef {
	return JobRef{Repository: j.Repository, RunID: j.RunID, RunAttempt: j.RunAttempt, Job: j.Job,
		GitHubJobID: j.GitHubJobID, Shard: j.Shard, RunnerName: j.RunnerName}
}

func toHolder(h Holder) slots.Holder {
	return slots.Holder{Machine: h.Machine, PID: h.HolderPID, GatePID: h.GatePID, Started: h.HolderStarted}
}

func newClaim() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func randomSuffix() int64 {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<40))
	if err != nil {
		return 0
	}
	return n.Int64()
}

// logRow writes one slot-log row; a failure never holds or fails a job (R5.39).
func (e *Engine) logRow(tx slots.Tx, r slots.LogRow) {
	if r.At.IsZero() {
		r.At = e.now()
	}
	if err := tx.AppendLog(r); err != nil {
		e.log.Warn("slot log write failed", "machine", r.MachineID, "err", err)
	}
}

func (e *Engine) freedRow(tx slots.Tx, s slots.Slot, event, reason string, now time.Time) Freed {
	e.logRow(tx, slots.LogRow{MachineID: s.MachineID, Event: event, Kind: s.Kind, Number: s.Number, Job: s.Job,
		QueuedAt: s.QueuedAt, SlotAt: s.TakenAt, ReleasedAt: now, Reason: reason, At: now})
	return Freed{MachineID: s.MachineID, Kind: SlotKind(s.Kind), Number: s.Number, Job: fromJob(s.Job), Reason: reason, TakenAt: s.TakenAt}
}

func (e *Engine) droppedRow(tx slots.Tx, t slots.Ticket, reason string, now time.Time) Freed {
	e.logRow(tx, slots.LogRow{MachineID: t.MachineID, Event: slots.EventDropped, Kind: t.Kind, Job: t.Job,
		QueuedAt: t.QueuedAt(), ReleasedAt: now, Reason: slots.OutcomeTicketDrop + ": " + reason, At: now})
	return Freed{MachineID: t.MachineID, Kind: SlotKind(t.Kind), Job: fromJob(t.Job), Reason: reason}
}

// ---------------------------------------------------------------- stale rules

// staleSlots frees slots past their maximum age or whose lease ended, and drops
// expired tickets (R5.16, R5.33, R5.34, R5.70), inside tx. machine "" means every machine.
func (e *Engine) staleSlots(tx slots.Tx, machine string, now time.Time) ([]Freed, error) {
	var freed []Freed
	var all []slots.Slot
	var err error
	if machine == "" {
		all, err = tx.AllSlots()
	} else {
		all, err = tx.Slots(machine)
	}
	if err != nil {
		return nil, err
	}
	for _, s := range all {
		aged := !s.ExpiresAt.After(now)
		lapsed := !slots.LeaseEnd(s).After(now)
		if !aged && !lapsed {
			continue
		}
		set, err := tx.Settings(s.MachineID)
		if err != nil {
			return nil, err
		}
		// The claim keeps a slot that was freed and taken again in between (R5.15).
		ok, err := tx.FreeSlot(s.MachineID, s.Kind, s.Number, s.Claim)
		if err != nil {
			return nil, err
		}
		if ok {
			reason := "lease expired"
			if aged {
				reason = fmt.Sprintf("older than %d min", set.SlotMaxAgeMin)
			}
			freed = append(freed, e.freedRow(tx, s, slots.EventStaleFreed, reason, now))
		}
	}
	var tickets []slots.Ticket
	if machine == "" {
		tickets, err = tx.AllTickets()
	} else {
		tickets, err = tx.Tickets(machine)
	}
	if err != nil {
		return nil, err
	}
	for _, t := range tickets {
		if t.ExpiresAt.After(now) {
			continue
		}
		set, err := tx.Settings(t.MachineID)
		if err != nil {
			return nil, err
		}
		reason := "no heartbeat"
		if now.Sub(t.QueuedAt()) > time.Duration(set.SlotMaxAgeMin)*time.Minute {
			reason = fmt.Sprintf("older than %d min", set.SlotMaxAgeMin)
		}
		if ok, err := tx.DeleteTicket(t.ID); err != nil {
			return nil, err
		} else if ok {
			freed = append(freed, e.droppedRow(tx, t, reason, now))
			if err := tx.DropPasses(t.ID); err != nil {
				return nil, err
			}
		}
	}
	return freed, nil
}

func (e *Engine) notifyFreed(freed []Freed) {
	e.emitFreed(freed)
	seen := map[string]bool{}
	for _, f := range freed {
		if f.Number > 0 && !seen[f.MachineID] {
			seen[f.MachineID] = true
			e.wake.notify(f.MachineID)
		}
	}
}

// ---------------------------------------------------------------- helpers

var errNoMachine = errors.New("machine id is required")

func jobSlots(all []slots.Slot, j slots.Job) []slots.Slot {
	var out []slots.Slot
	for _, s := range all {
		if s.Job.SameJob(j) {
			out = append(out, s)
		}
	}
	return out
}

func freeNumber(all []slots.Slot, kind slots.Kind, count int) (int, int) {
	used := map[int]bool{}
	n := 0
	for _, s := range all {
		if s.Kind == kind {
			used[s.Number] = true
			n++
		}
	}
	for i := 1; i <= count; i++ {
		if !used[i] {
			return i, count - n
		}
	}
	return 0, 0
}
