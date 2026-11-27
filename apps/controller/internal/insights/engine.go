package insights

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

const (
	// WindowLength is the window every pass reads: the last 7 days (R5.9, R6.3).
	WindowLength = 7 * 24 * time.Hour
	// Interval is how often the engine runs the analysers (R6.3).
	Interval = 15 * time.Minute
	// SlowPass is the pass length over which the engine logs a warning and the doctor shows it (R6.3).
	SlowPass = 30 * time.Second
)

// Clock is the engine's time source, so tests need no sleeps.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives once d has passed.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the real clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// After is time.After.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// PassStats is what the doctor reads about the last pass of one analyser (R6.3, R6.8).
type PassStats struct {
	Analyser string
	Took     time.Duration
	OK       bool
	Slow     bool
	Err      string
}

// engine is the Engine of this package.
type engine struct {
	analysers []Analyser
	store     FindingStore
	runs      RunLog
	clock     Clock
	log       *slog.Logger
	redactor  *redact.Redactor
	onPass    func(PassStats)
	meter     Meter
	period    func() Period

	mu       sync.Mutex
	finished int           // CI runs finished since the last pass
	kick     chan struct{} // a finished run, wakes Run
}

// Option configures NewEngine.
type Option func(*engine)

// WithClock sets the clock (tests use a fake).
func WithClock(c Clock) Option { return func(e *engine) { e.clock = c } }

// WithLogger sets the logger; nil keeps the default.
func WithLogger(l *slog.Logger) Option {
	return func(e *engine) {
		if l != nil {
			e.log = l
		}
	}
}

// WithPassHook sets a callback run after every analyser pass, with its stats (the doctor's feed, R6.3).
func WithPassHook(f func(PassStats)) Option { return func(e *engine) { e.onPass = f } }

// WithMeter measures every RunOnce (average CPU, peak memory, store size on disk). Over a limit the engine opens
// the KindCost warning finding that recommends a slower update period (owner decisions D18, D19).
func WithMeter(m Meter) Option { return func(e *engine) { e.meter = m } }

// WithPeriod sets where the update period is read from, at every decision, so a change in Settings takes
// effect at once. The default is every finished CI run.
func WithPeriod(f func() Period) Option { return func(e *engine) { e.period = f } }

// NewEngine returns the Engine over the analysers. runs may be nil (no run log).
func NewEngine(analysers []Analyser, store FindingStore, runs RunLog, opts ...Option) Engine {
	e := &engine{analysers: analysers, store: store, runs: runs, clock: SystemClock{},
		log: slog.Default(), redactor: redact.New(), kick: make(chan struct{}, 1),
		period: func() Period { return Period{Mode: ModeEveryRun} }}
	for _, o := range opts {
		o(e)
	}
	return e
}

// RunOnce runs every analyser once, one at a time, over the 7 days ending at now, and syncs each one's
// findings. A failed analyser is counted and logged and resolves nothing (R6.8); the others still run.
func (e *engine) RunOnce(ctx context.Context, now time.Time) error {
	now = now.UTC()
	w := Window{From: now.Add(-WindowLength), To: now}
	var end func() Usage
	if e.meter != nil {
		end = e.meter.Begin()
	}
	var errs []error
	for _, a := range e.analysers {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if err := e.pass(ctx, a, w, now); err != nil {
			errs = append(errs, err)
		}
	}
	if end != nil {
		if err := e.reportCost(ctx, end(), now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// reportCost opens, refreshes or resolves the cost warning from one measured pass (D18, D19). A failed
// store write is logged and returned; the analysers' findings are not touched.
func (e *engine) reportCost(ctx context.Context, u Usage, now time.Time) error {
	var found []Finding
	if f, over := CostFinding(u, e.period()); over {
		found = append(found, f)
		e.log.Warn("insights: analysis over its budget", "cpuPercent", round1(u.CPUPercent), "peakRamBytes", u.PeakRAMBytes, "storeBytes", u.StoreBytes)
	}
	if _, err := e.store.Sync(ctx, []Kind{KindCost}, found, now); err != nil {
		return fmt.Errorf("cost: %s", e.safe(err))
	}
	return nil
}

func (e *engine) pass(ctx context.Context, a Analyser, w Window, now time.Time) (err error) {
	start := e.clock.Now()
	var runID int64
	if e.runs != nil {
		if id, berr := e.runs.Begin(ctx, a.Name(), w, now); berr != nil {
			e.log.Warn("insights: run log begin failed", "analyser", a.Name(), "err", e.safe(berr))
		} else {
			runID = id
		}
	}
	var found []Finding
	var res SyncResult
	// A panic in an analyser is a failed pass, never a crash (R6.8).
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("analyser %s panicked: %v", a.Name(), p)
			}
		}()
		found, err = a.Analyse(ctx, w)
		if err == nil {
			found = ownedOnly(a, found)
			res, err = e.store.Sync(ctx, a.Kinds(), found, now)
		}
	}()
	took := e.clock.Now().Sub(start)
	st := PassStats{Analyser: a.Name(), Took: took, OK: err == nil, Slow: took > SlowPass}
	if err != nil {
		st.Err = e.safe(err)
		e.log.Warn("insights: pass failed", "analyser", a.Name(), "err", st.Err)
	}
	if st.Slow {
		e.log.Warn("insights: slow pass", "analyser", a.Name(), "took", took.String())
	}
	if e.runs != nil && runID != 0 {
		if eerr := e.runs.End(ctx, runID, e.clock.Now().UTC(), err == nil, len(found), res.Resolved, st.Err); eerr != nil {
			e.log.Warn("insights: run log end failed", "analyser", a.Name(), "err", e.safe(eerr))
		}
	}
	if e.onPass != nil {
		e.onPass(st)
	}
	if err != nil {
		return fmt.Errorf("%s: %s", a.Name(), st.Err)
	}
	return nil
}

// ownedOnly drops findings of kinds the analyser does not own: Sync resolves by owned kinds, so a stray kind
// would be opened but never closed.
func ownedOnly(a Analyser, found []Finding) []Finding {
	owned := map[Kind]bool{}
	for _, k := range a.Kinds() {
		owned[k] = true
	}
	out := found[:0:0]
	for _, f := range found {
		if owned[f.Kind] {
			out = append(out, f)
		}
	}
	return out
}

func (e *engine) safe(err error) string { return e.redactor.Redact(err.Error()) }

// Trigger tells the engine that a CI run finished (a job completed webhook, D18). It never blocks. Whether a
// pass starts now depends on the update period: every run, every Nth run, or only on the hourly timer.
func (e *engine) Trigger() {
	e.mu.Lock()
	e.finished++
	e.mu.Unlock()
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

// Triggerer is an Engine that can be told a CI run finished.
type Triggerer interface {
	Trigger()
}

// Run calls RunOnce at once, then after finished CI runs and on the timer as the update period says, until ctx
// ends. A failed pass never stops the loop. A pass never overlaps another: runs that finish during a pass are
// counted and decided after it.
func (e *engine) Run(ctx context.Context) error {
	last := e.clock.Now()
	e.mu.Lock()
	e.finished = 0
	e.mu.Unlock()
	_ = e.RunOnce(ctx, last)
	tick := e.clock.After(Interval)
	for {
		fromTimer := false
		select {
		case <-ctx.Done():
			return nil
		case <-e.kick:
		case <-tick:
			fromTimer = true
		}
		now := e.clock.Now()
		e.mu.Lock()
		n := e.finished
		e.mu.Unlock()
		if !e.period().Due(n, now.Sub(last), fromTimer, false) {
			if fromTimer {
				tick = e.clock.After(Interval)
			}
			continue
		}
		e.mu.Lock()
		e.finished = 0
		e.mu.Unlock()
		_ = e.RunOnce(ctx, now)
		last = e.clock.Now()
		if fromTimer {
			tick = e.clock.After(Interval)
		}
	}
}
