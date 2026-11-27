package mirror

import (
	"sync"
	"time"
)

// Kinds of cache a job report names (caches R5.68).
const (
	WarmImage     = "image"
	WarmTools     = "tools"
	WarmWorkspace = "workspace"
	WarmGit       = "git"
	WarmActions   = "actions"
)

func points(kind string) int {
	switch kind {
	case WarmWorkspace:
		return 20
	case WarmImage, WarmTools, WarmGit, WarmActions:
		return 25
	}
	return 0
}

type warmKey struct{ machine, repo string }

type warmCell struct {
	kinds map[string]time.Time
}

// Warmth keeps one value 0 to 100 per machine and repository (R5.27, R5.28).
// It never places a job: the scheduler reads Value.
type Warmth struct {
	Now   func() time.Time
	Hours func() int // warmth_hours; 0 takes the default

	mu    sync.Mutex
	cells map[warmKey]*warmCell
}

func (w *Warmth) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Warmth) ttl() time.Duration {
	h := DefaultWarmthHours
	if w.Hours != nil {
		if v := w.Hours(); v > 0 {
			h = v
		}
	}
	return time.Duration(h) * time.Hour
}

// Record notes what a machine holds for a repository after a job. Kinds this
// part does not know are ignored.
func (w *Warmth) Record(machine, repo string, kinds []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cells == nil {
		w.cells = map[warmKey]*warmCell{}
	}
	k := warmKey{machine, repo}
	c := w.cells[k]
	if c == nil {
		c = &warmCell{kinds: map[string]time.Time{}}
		w.cells[k] = c
	}
	for _, kind := range kinds {
		if points(kind) > 0 {
			c.kinds[kind] = w.now()
		}
	}
}

// Value is the warmth now: the points of the kinds held, each falling in a
// straight line to 0 at the warmth time after its last report.
func (w *Warmth) Value(machine, repo string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.cells[warmKey{machine, repo}]
	if c == nil {
		return 0
	}
	ttl, now := w.ttl(), w.now()
	var sum float64
	for kind, at := range c.kinds {
		age := now.Sub(at)
		if age >= ttl {
			continue
		}
		if age < 0 {
			age = 0
		}
		sum += float64(points(kind)) * (1 - float64(age)/float64(ttl))
	}
	if sum > 100 {
		sum = 100
	}
	return int(sum + 0.5)
}

// Reset sets the named kinds to 0 on a machine for every repository (a wipe
// of the related cache, R5.28). An empty machine means every machine.
func (w *Warmth) Reset(machine string, kinds ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, c := range w.cells {
		if machine != "" && k.machine != machine {
			continue
		}
		for _, kind := range kinds {
			delete(c.kinds, kind)
		}
	}
}

// ResetRepo sets the named kinds to 0 for one repository on every machine.
func (w *Warmth) ResetRepo(repo string, kinds ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for k, c := range w.cells {
		if k.repo != repo {
			continue
		}
		for _, kind := range kinds {
			delete(c.kinds, kind)
		}
	}
}
