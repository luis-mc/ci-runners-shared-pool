package update

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Agent is a connected agent-only machine as the follower sees it.
type Agent struct {
	ID      string
	Name    string
	Version string
	// Updating is true while the agent reported an update in progress (waiting for
	// jobs, downloading, installing); UpdatingSince says when it last said so.
	Updating      bool
	UpdatingSince time.Time
}

// Fleet is the controller's view of its agents.
type Fleet interface {
	Agents(ctx context.Context) ([]Agent, error)
	AskUpdate(machineID, version string) error
}

// Follower makes the connected agent machines follow the controller's version
// (operations R5.39): after the controller updated, each agent whose version
// is older gets the ask, over the agent link, again at every tick until it
// runs the same version. One agent is asked per tick, and none while another
// reports an update in progress (R5.18: one machine at a time). The agent applies the same drain rule as the
// controller (60 minutes, then it postpones and the next tick asks again) and
// the same retry rule (three tries per version), so the follower itself keeps
// no count.
type Follower struct {
	Fleet   Fleet
	Current string // the controller's version
	Logger  *slog.Logger
	Now     func() time.Time
	// Every is the tick (default 10 minutes); ActiveFor is how long an "updating"
	// report counts before the agent is asked again (default 90 minutes).
	Every     time.Duration
	ActiveFor time.Duration

	mu    sync.Mutex
	asked map[string]time.Time
}

// Tick asks every connected agent that runs an older stable version. It returns
// the machine ids it asked.
func (f *Follower) Tick(ctx context.Context) []string {
	if _, ok := ParseVersion(f.Current); !ok {
		return nil // a dev or edge controller never pulls agents to it
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	activeFor := f.ActiveFor
	if activeFor <= 0 {
		activeFor = 90 * time.Minute
	}
	log := f.Logger
	if log == nil {
		log = slog.Default()
	}
	agents, err := f.Fleet.Agents(ctx)
	if err != nil {
		log.Warn("follow: cannot list the agents", "err", err)
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.asked == nil {
		f.asked = map[string]time.Time{}
	}
	// One machine at a time (operations R5.18): while an agent reports an update
	// in progress nobody else is asked, and each tick asks one agent, the one
	// asked least recently, so an agent that keeps postponing never starves the rest.
	var due []Agent
	for _, a := range agents {
		if !Newer(a.Version, f.Current) {
			delete(f.asked, a.ID) // same version, newer, or not a stable build: leave it alone
			continue
		}
		if a.Updating && now().Sub(a.UpdatingSince) < activeFor {
			return nil
		}
		due = append(due, a)
	}
	if len(due) == 0 {
		return nil
	}
	sort.SliceStable(due, func(i, j int) bool {
		ti, tj := f.asked[due[i].ID], f.asked[due[j].ID]
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return due[i].ID < due[j].ID
	})
	var asked []string
	for _, a := range due {
		if err := f.Fleet.AskUpdate(a.ID, f.Current); err != nil {
			log.Warn("follow: cannot ask the agent", "machine", a.Name, "err", err)
			continue
		}
		f.asked[a.ID] = now()
		asked = append(asked, a.ID)
		break
	}
	return asked
}

// Run ticks until ctx ends: the first after a short wait (agents reconnect
// after the controller restarted), then every Every.
func (f *Follower) Run(ctx context.Context, first time.Duration) {
	every := f.Every
	if every <= 0 {
		every = 10 * time.Minute
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.Tick(ctx)
			t.Reset(every)
		}
	}
}
