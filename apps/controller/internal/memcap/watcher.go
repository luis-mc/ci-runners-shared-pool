package memcap

import (
	"sync"
	"time"
)

// Tree reads the process tree of a root pid: the pids and the resident bytes of
// each. It is the platform part of the watcher; a fake gives it in tests.
type Tree interface {
	// Members returns the pids of the tree under root, root included, and the
	// sum of their resident memory in bytes. An empty list means the tree is gone.
	Members(root int) (pids []int, rssBytes int64)
}

// Watcher caps a job by watching its process tree (macOS, which has neither
// cgroups nor job objects). When the tree's resident memory is over the cap it
// kills every member and marks the job. Poll drives it: the caller feeds it
// ticks, so a test needs no sleep.
type Watcher struct {
	Tree Tree
	// Kill stops the pids. It is os.Process.Kill on a real machine.
	Kill func(pids []int)
	// Interval is the poll step Run uses.
	Interval time.Duration

	mu   sync.Mutex
	jobs map[string]*watched
}

type watched struct {
	job   Job
	limit int64
	over  bool
	done  bool
}

var _ Enforcer = (*Watcher)(nil)

// Name implements Enforcer.
func (w *Watcher) Name() string { return "watcher" }

// Start implements Enforcer.
func (w *Watcher) Start(j Job, limit int64) (Guard, error) {
	if limit <= 0 {
		return nil, ErrNoLimit
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.jobs == nil {
		w.jobs = map[string]*watched{}
	}
	g := &watched{job: j, limit: limit}
	w.jobs[j.ID] = g
	return &watchGuard{w: w, g: g}, nil
}

// Poll checks every job once and returns the ids it stopped.
func (w *Watcher) Poll() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var stopped []string
	for id, g := range w.jobs {
		if g.done || g.over {
			continue
		}
		pids, rss := w.Tree.Members(g.job.PID)
		if len(pids) == 0 || rss <= g.limit {
			continue
		}
		g.over = true
		w.Kill(pids)
		stopped = append(stopped, id)
	}
	return stopped
}

// Run polls on every tick until stop closes.
func (w *Watcher) Run(ticks <-chan time.Time, stop <-chan struct{}) {
	for {
		select {
		case <-ticks:
			w.Poll()
		case <-stop:
			return
		}
	}
}

type watchGuard struct {
	w *Watcher
	g *watched
}

func (g *watchGuard) Exceeded() bool {
	g.w.mu.Lock()
	defer g.w.mu.Unlock()
	return g.g.over
}

func (g *watchGuard) Release() error {
	g.w.mu.Lock()
	defer g.w.mu.Unlock()
	g.g.done = true
	delete(g.w.jobs, g.g.job.ID)
	return nil
}
