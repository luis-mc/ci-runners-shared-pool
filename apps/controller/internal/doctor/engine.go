package doctor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
)

// Check timeouts (R6.1).
const (
	DefaultCheckTimeout = 10 * time.Second
	MaxCheckTimeout     = 60 * time.Second
	maxParallel         = 8
)

// EnvFunc builds the Env of one run.
type EnvFunc func(ctx context.Context) (Env, error)

// Engine runs checks and keeps the results. One engine per controller.
type Engine struct {
	Registry *Registry
	Store    Store
	// Env gives each run its sources.
	Env EnvFunc
	// Events gets doctor.progress events; may be nil.
	Events events.Publisher
	Logger *slog.Logger
	// CheckTimeout is the deadline of each check; zero means DefaultCheckTimeout, above MaxCheckTimeout is lowered.
	CheckTimeout time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu      sync.Mutex
	active  map[string]string // scope key -> run id
	waiters map[string]chan struct{}
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) timeout() time.Duration {
	switch {
	case e.CheckTimeout <= 0:
		return DefaultCheckTimeout
	case e.CheckTimeout > MaxCheckTimeout:
		return MaxCheckTimeout
	}
	return e.CheckTimeout
}

func (e *Engine) log() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func newRunID() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	return "dr_" + hex.EncodeToString(b[:])
}

// Start begins a run and returns at once with its ID. A run of the same scope
// that is still going is joined: its ID comes back with joined true (R5.8). The
// run goes on after ctx ends; use Wait to block for it.
func (e *Engine) Start(ctx context.Context, scope Scope, trig Trigger, by string) (id string, joined bool, err error) {
	key := scope.Key()
	e.mu.Lock()
	if e.active == nil {
		e.active = map[string]string{}
		e.waiters = map[string]chan struct{}{}
	}
	if cur, ok := e.active[key]; ok {
		e.mu.Unlock()
		return cur, true, nil
	}
	id = newRunID()
	done := make(chan struct{})
	e.active[key] = id
	e.waiters[id] = done
	e.mu.Unlock()

	run := Run{ID: id, Trigger: trig, State: Running, Scope: scope, StartedBy: by, StartedAt: e.now(), Counts: map[Severity]int{}}
	if err := e.Store.SaveRun(ctx, run); err != nil {
		e.finish(key, id)
		return "", false, fmt.Errorf("save doctor run: %w", err)
	}
	e.publish(run)
	// The run outlives the request that started it.
	go func() {
		defer e.finish(key, id)
		e.execute(context.WithoutCancel(ctx), run)
	}()
	return id, false, nil
}

func (e *Engine) finish(key, id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.active, key)
	if ch, ok := e.waiters[id]; ok {
		close(ch)
		delete(e.waiters, id)
	}
}

// Wait blocks until the run has ended or ctx is done, then returns the run.
func (e *Engine) Wait(ctx context.Context, id string) (Run, error) {
	e.mu.Lock()
	ch := e.waiters[id]
	e.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		case <-ctx.Done():
			return Run{}, ctx.Err()
		}
	}
	r, ok, err := e.Store.GetRun(ctx, id)
	if err != nil {
		return Run{}, err
	}
	if !ok {
		return Run{}, errors.New("doctor run not found")
	}
	return r, nil
}

// Run starts a run and waits for it. Callers that only need the result (the
// schedule, tests) use this.
func (e *Engine) Run(ctx context.Context, scope Scope, trig Trigger, by string) (Run, error) {
	id, _, err := e.Start(ctx, scope, trig, by)
	if err != nil {
		return Run{}, err
	}
	return e.Wait(ctx, id)
}

func (e *Engine) execute(ctx context.Context, run Run) {
	env, err := e.Env(ctx)
	if err != nil {
		e.log().Error("doctor: env", "err", err)
		run.State, run.FinishedAt = Failed, e.now()
		_ = e.Store.SaveRun(ctx, run)
		e.publish(run)
		return
	}
	env.Scope = run.Scope
	env.Now = run.StartedAt
	env.memo = &memo{m: map[string]*memoEntry{}}
	checks := e.Registry.Selected(run.Scope)

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, maxParallel)
	)
	for _, c := range checks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fs := e.runCheck(ctx, c, env)
			mu.Lock()
			run.Findings = append(run.Findings, fs...)
			part := run
			part.Findings = append([]Finding(nil), run.Findings...)
			mu.Unlock()
			e.publish(part)
		}()
	}
	wg.Wait()

	SortFindings(run.Findings)
	run.Counts = map[Severity]int{}
	run.Worst = OK
	for _, f := range run.Findings {
		run.Counts[f.Severity]++
		run.Worst = Worst(run.Worst, f.Severity)
	}
	run.State, run.FinishedAt = Done, e.now()
	if err := e.Store.SaveRun(ctx, run); err != nil {
		e.log().Error("doctor: save run", "err", err, "run", run.ID)
	}
	e.publish(run)
}

// runCheck runs one check with a deadline and crash isolation (R5.5, R5.6, R6.1).
func (e *Engine) runCheck(parent context.Context, c Check, env Env) (out []Finding) {
	ctx, cancel := context.WithTimeout(parent, e.timeout())
	defer cancel()
	now := e.now()
	subj := Subject{Kind: "check", ID: c.ID(), Name: c.ID()}
	type result struct{ fs []Finding }
	ch := make(chan result, 1)
	go func() {
		defer func() {
			// A panic in the check's own goroutine is turned into a result here.
			if v := recover(); v != nil {
				e.log().Error("doctor: check crashed", "check", c.ID(), "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				ch <- result{[]Finding{{
					Check: c.ID(), Subject: subj, Severity: Fail, Title: "Check crashed", Detail: fmt.Sprint(v),
					Fix: Fix{Text: "This is a bug in the doctor. Report it with the controller log."},
				}}}
			}
		}()
		ch <- result{c.Run(ctx, env)}
	}()
	select {
	case r := <-ch:
		out = r.fs
	case <-ctx.Done():
		out = []Finding{{
			Check: c.ID(), Subject: subj, Severity: Skip, Title: "Check did not finish in time",
			Detail: fmt.Sprintf("It was stopped after %s.", e.timeout()),
			Fix:    Fix{Text: "Run the doctor again. If it repeats, the machine or GitHub is slow to answer."},
		}}
	}
	for i := range out {
		out[i] = e.stamp(c, now, out[i])
	}
	return out
}

// stamp fills what the engine owns and enforces the rules of R5.3 to R5.5, R6.3.
func (e *Engine) stamp(c Check, now time.Time, f Finding) Finding {
	if f.Check == "" {
		f.Check = c.ID()
	}
	f.Milestone = c.Milestone()
	f.NeedsAdmin = f.NeedsAdmin || (c.NeedsAdmin() && f.Severity == Skip)
	f.CheckedAt = now
	if !f.Severity.Valid() {
		f.Severity = Fail
		f.Detail = "The check returned an unknown severity. " + f.Detail
	}
	if f.Severity != OK && f.Fix.Text == "" {
		// R5.4: every finding that is not ok has a fix.
		f.Fix.Text = "No fix is known for this finding yet."
	}
	return redactFinding(f)
}

func (e *Engine) publish(r Run) {
	if e.Events == nil {
		return
	}
	b, err := json.Marshal(r.API())
	if err != nil {
		return
	}
	e.Events.Publish(events.DoctorProgress, b)
}
