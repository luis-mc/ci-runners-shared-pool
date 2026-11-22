package listenergate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/github"
)

// Gate is the runner gate. Every method takes one lock, so a webhook and the
// sweep never start and stop the same listener at once; the lease store is the
// compare-and-set across restarts.
type Gate struct {
	Settings func() Settings
	Store    Store
	Labels   Labels
	Service  Service
	Runners  Runners
	Audit    Audit
	// Queue says why jobs wait for a runner; nil gives no reason (the console
	// then shows the lease reason of the scheduler).
	Queue func(ctx context.Context, r Runner) string
	Now   func() time.Time

	mu sync.Mutex
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func (g *Gate) enforce() bool { return g.Settings != nil && g.Settings().Enforce }

func (g *Gate) record(ctx context.Context, e Event) {
	if g.Audit != nil {
		_ = g.Audit.Record(ctx, e)
	}
}

// Job is a job admission admitted for a gated runner.
type Job struct {
	JobID    int64
	JobLabel string // the job-name label, "" when the job has none
	Runner   Runner
}

// Admit is called when admission admits a job for a gated persistent runner.
// In enforce mode it adds the job's label and starts the listener, and returns
// taken=true: the runner takes the job. In observe mode it writes would_start
// once for the job and returns taken=false, and the caller goes on as before.
func (g *Gate) Admit(ctx context.Context, j Job) (taken bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	enforce := g.enforce()
	state := Observed
	if enforce {
		state = Started
	}
	l, fresh, err := g.Store.Claim(ctx, Lease{JobID: j.JobID, Runner: j.Runner, JobLabel: strings.ToLower(j.JobLabel), State: state, StartedAt: now, UpdatedAt: now})
	if err != nil {
		return false, err
	}
	if !enforce {
		if fresh {
			g.record(ctx, Event{Action: ActionWouldStart, JobID: j.JobID, Runner: j.Runner.Name, Label: l.JobLabel,
				Detail: "observe mode: would add the job label and start the listener"})
		}
		return false, nil
	}
	switch {
	case fresh:
	case l.State == Started || l.State == Stopping:
		return true, nil // a repeat of the same job: already on
	case l.State == Finished:
		return false, nil
	}
	// A lease left over from observe mode or a failed try is made again.
	l.State, l.Runner, l.JobLabel, l.StartedAt, l.UpdatedAt = Started, j.Runner, strings.ToLower(j.JobLabel), now, now
	if l.JobLabel != "" {
		if err := g.Labels.AddRunnerLabels(ctx, j.Runner.InstallationID, j.Runner.Scope, j.Runner.GitHubRunnerID, []string{l.JobLabel}); err != nil {
			return false, g.fail(ctx, l, fmt.Errorf("add label %s: %w", l.JobLabel, err))
		}
	}
	others, err := g.othersOpen(ctx, l)
	if err != nil {
		return false, g.fail(ctx, l, err)
	}
	if len(others) == 0 { // another job's listener is already on otherwise
		if err := g.Service.StartListener(ctx, j.Runner.MachineID, j.Runner.ServiceRef, false); err != nil {
			g.removeLabel(ctx, l, others)
			return false, g.fail(ctx, l, fmt.Errorf("start the listener of %s: %w", j.Runner.Name, err))
		}
	}
	if err := g.Store.Save(ctx, l); err != nil {
		return false, err
	}
	g.record(ctx, Event{Action: ActionStart, JobID: l.JobID, Runner: j.Runner.Name, Label: l.JobLabel,
		Detail: "label added and listener started for the admitted job"})
	return true, nil
}

func (g *Gate) fail(ctx context.Context, l Lease, cause error) error {
	l.State, l.UpdatedAt = Failed, g.now()
	_ = g.Store.Save(ctx, l)
	g.record(ctx, Event{Action: ActionFailed, JobID: l.JobID, Runner: l.Runner.Name, Label: l.JobLabel, Detail: cause.Error()})
	return cause
}

func (g *Gate) othersOpen(ctx context.Context, l Lease) ([]Lease, error) {
	all, err := g.Store.OpenFor(ctx, l.Runner.ID)
	if err != nil {
		return nil, err
	}
	var out []Lease
	for _, o := range all {
		if o.JobID != l.JobID {
			out = append(out, o)
		}
	}
	return out, nil
}

// removeLabel removes the job's label unless another open lease of the runner
// holds the same one.
func (g *Gate) removeLabel(ctx context.Context, l Lease, others []Lease) error {
	if l.JobLabel == "" {
		return nil
	}
	for _, o := range others {
		if o.JobLabel == l.JobLabel {
			return nil
		}
	}
	err := g.Labels.RemoveRunnerLabel(ctx, l.Runner.InstallationID, l.Runner.Scope, l.Runner.GitHubRunnerID, l.JobLabel)
	if errors.Is(err, github.ErrNotFound) {
		return nil
	}
	return err
}

// Completed is called after the job's completed event (or when the job is gone).
// It stops the listener and removes the label, unless another job of the runner
// still needs the listener. A step that fails is tried again by Sweep.
func (g *Gate) Completed(ctx context.Context, jobID int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok, err := g.Store.Get(ctx, jobID)
	if err != nil || !ok {
		return err
	}
	return g.finish(ctx, l)
}

func (g *Gate) finish(ctx context.Context, l Lease) error {
	now := g.now()
	switch l.State {
	case Finished, Failed:
		return nil
	case Observed:
		g.record(ctx, Event{Action: ActionWouldStop, JobID: l.JobID, Runner: l.Runner.Name, Label: l.JobLabel,
			Detail: "observe mode: would stop the listener and remove the job label"})
		l.State, l.UpdatedAt = Finished, now
		return g.Store.Save(ctx, l)
	}
	l.State, l.UpdatedAt = Stopping, now
	if err := g.Store.Save(ctx, l); err != nil {
		return err
	}
	others, err := g.othersOpen(ctx, l)
	if err != nil {
		return err
	}
	if len(others) == 0 {
		// The job ended (the completed event came), so nothing is assigned.
		if err := g.Service.StopListener(ctx, l.Runner.MachineID, l.Runner.ServiceRef, false); err != nil {
			g.record(ctx, Event{Action: ActionFailed, JobID: l.JobID, Runner: l.Runner.Name, Label: l.JobLabel,
				Detail: "the listener did not stop: " + err.Error()})
			return fmt.Errorf("stop the listener of %s: %w", l.Runner.Name, err)
		}
	}
	if err := g.removeLabel(ctx, l, others); err != nil {
		return fmt.Errorf("remove label %s: %w", l.JobLabel, err)
	}
	l.State, l.UpdatedAt = Finished, now
	if err := g.Save(ctx, l); err != nil {
		return err
	}
	g.record(ctx, Event{Action: ActionStop, JobID: l.JobID, Runner: l.Runner.Name, Label: l.JobLabel,
		Detail: "job ended: listener stopped and label removed"})
	return nil
}

// Save is the store write, kept apart so finish reads clearly.
func (g *Gate) Save(ctx context.Context, l Lease) error { return g.Store.Save(ctx, l) }

// Holds is true while the gate keeps a label or a listener for the job.
func (g *Gate) Holds(ctx context.Context, jobID int64) bool {
	l, ok, err := g.Store.Get(ctx, jobID)
	return err == nil && ok && (l.State == Started || l.State == Stopping)
}

// Serves reports whether the runner can carry a job: the OS matches and every
// generic label of the job is one the runner has. A routing label is the one the
// gate adds, so it is skipped.
func (r Runner) Serves(os string, labels []string, isRouting func(string) bool) bool {
	if os != "" && !strings.EqualFold(r.OS, os) {
		return false
	}
	have := map[string]bool{"self-hosted": true, strings.ToLower(r.OS): true}
	for _, l := range r.Labels {
		have[strings.ToLower(l)] = true
	}
	for _, raw := range labels {
		l := strings.ToLower(strings.TrimSpace(raw))
		if l == "" || (isRouting != nil && isRouting(l)) {
			continue
		}
		if !have[l] {
			return false
		}
	}
	return true
}

// Pick chooses the gated runner of a machine that takes a job: one that serves
// its labels and holds the fewest open leases, so a runner with no job is
// preferred. ok is false when the machine has none.
func (g *Gate) Pick(ctx context.Context, machineID, os string, labels []string, isRouting func(string) bool) (Runner, bool, error) {
	rs, err := g.Runners.Gated(ctx)
	if err != nil {
		return Runner{}, false, err
	}
	var best Runner
	bestOpen, found := 0, false
	for _, r := range rs {
		if r.MachineID != machineID || !r.Serves(os, labels, isRouting) {
			continue
		}
		open, err := g.Store.OpenFor(ctx, r.ID)
		if err != nil {
			return Runner{}, false, err
		}
		if !found || len(open) < bestOpen {
			best, bestOpen, found = r, len(open), true
		}
	}
	return best, found, nil
}
