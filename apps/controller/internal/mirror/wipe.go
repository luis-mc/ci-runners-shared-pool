package mirror

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

// Scopes of a wipe (caches R5.30, R5.69).
const (
	ScopeActionsEntry = "actions_entry"
	ScopeActionsRepo  = "actions_repo"
	ScopeActionsAll   = "actions_all"
	ScopeGitMirror    = "git_mirror"
	ScopeToolCache    = "tool_cache"
	ScopeRegistry     = "registry_mirror"
)

// Item is one line of the plan: what goes and how big it is.
type Item struct {
	Text string
	Size int64
}

// Plan is what a wipe would delete and what it waits for (R5.31, R5.32).
type Plan struct {
	Scope  string
	Target string
	Items  []Item
	Total  int64
	Jobs   []string // running jobs that use the scope
}

// Target is one thing a wipe can remove. The holders implement it.
type Target interface {
	// Plan lists what goes for a target name ("" for the whole scope).
	Plan(ctx context.Context, name string) ([]Item, error)
	// Delete removes it and returns the bytes freed.
	Delete(ctx context.Context, name string) (int64, error)
	// Warm are the warmth kinds that go to 0 with it.
	Warm() []string
}

// Jobs says which running jobs use a scope on this holder.
type Jobs interface {
	Using(ctx context.Context, scope, target string) ([]string, error)
}

// Errors of a wipe.
var (
	ErrDenied   = errors.New("only the owner or an admin can wipe a cache")
	ErrNoScope  = errors.New("no such cache to wipe")
	ErrNeedsYes = errors.New("this wipe needs a yes")
)

// StillDraining is a wipe that waited and did not delete (R5.32).
type StillDraining struct {
	Jobs   []string
	Waited time.Duration
}

func (e *StillDraining) Error() string {
	return fmt.Sprintf("still draining, %d jobs running, waited %s", len(e.Jobs), e.Waited.Round(time.Second))
}

// Wiper plans and runs wipes.
type Wiper struct {
	Targets map[string]Target
	Jobs    Jobs
	Warmth  *Warmth
	Audit   audit.Recorder
	Machine string // the holder machine id, for warmth
	Now     func() time.Time
	// Poll is how long to wait between drain checks; Wait is the longest wait.
	Poll, Wait time.Duration
	Sleep      func(context.Context, time.Duration) error
}

func (w *Wiper) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Plan builds the plan of a wipe. Nothing is deleted.
func (w *Wiper) Plan(ctx context.Context, scope, target string) (Plan, error) {
	t, ok := w.Targets[scope]
	if !ok {
		return Plan{}, ErrNoScope
	}
	items, err := t.Plan(ctx, target)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Scope: scope, Target: target, Items: items}
	for _, it := range items {
		p.Total += it.Size
	}
	if w.Jobs != nil {
		if p.Jobs, err = w.Jobs.Using(ctx, scope, target); err != nil {
			return Plan{}, err
		}
		sort.Strings(p.Jobs)
	}
	return p, nil
}

// Run wipes after a yes. admin says the caller may wipe (R5.30). It waits while
// jobs run and checks again right before the delete (R5.32); a wipe that is
// still waiting at the end deletes nothing.
func (w *Wiper) Run(ctx context.Context, actor audit.Actor, admin, confirm bool, scope, target string) (Plan, int64, error) {
	if !admin {
		return Plan{}, 0, ErrDenied
	}
	p, err := w.Plan(ctx, scope, target)
	if err != nil {
		return Plan{}, 0, err
	}
	if !confirm {
		return p, 0, ErrNeedsYes
	}
	start := w.now()
	for len(p.Jobs) > 0 {
		if w.Wait <= 0 || w.now().Sub(start) >= w.Wait {
			return p, 0, &StillDraining{Jobs: p.Jobs, Waited: w.now().Sub(start)}
		}
		sleep := w.Sleep
		if sleep == nil {
			sleep = sleepCtx
		}
		poll := w.Poll
		if poll <= 0 {
			poll = 5 * time.Second
		}
		if err := sleep(ctx, poll); err != nil {
			return p, 0, err
		}
		if p, err = w.Plan(ctx, scope, target); err != nil {
			return Plan{}, 0, err
		}
	}
	t := w.Targets[scope]
	freed, err := t.Delete(ctx, target)
	if err != nil {
		return p, freed, err
	}
	if w.Warmth != nil {
		w.Warmth.Reset(w.Machine, t.Warm()...)
	}
	if w.Audit != nil {
		items := make([]string, 0, len(p.Items))
		for _, it := range p.Items {
			items = append(items, it.Text)
		}
		_ = w.Audit.Record(ctx, actor, "cache.wipe", audit.Target{Type: "cache", ID: scope}, map[string]any{
			"summary": fmt.Sprintf("wipe %s %s", scope, target), "scope": scope, "target": target,
			"items": items, "bytes_freed": freed,
		})
	}
	return p, freed, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
