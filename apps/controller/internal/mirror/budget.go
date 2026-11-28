package mirror

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// Kinds of item the budget pass weighs (caches R5.73).
const (
	BudgetStore = "store" // a content store entry (Actions or packages)
	BudgetGit   = "git"   // one git mirror
)

// DefaultKeepRecent is how long a git mirror stays safe after its last use.
const DefaultKeepRecent = 30 * time.Minute

// BudgetItem is one thing the budget pass may remove.
type BudgetItem struct {
	Kind string
	Name string // the store name, or owner/repo of a git mirror
	Size int64
	Used time.Time
}

// Budget keeps every cache kind of the hub's base folder under one byte
// budget, least recently used first, across kinds (R5.73).
type Budget struct {
	Store *lancache.Store
	Git   *Git
	// Bytes is the budget now (the setting); <= 0 turns the pass off.
	Bytes      func() int64
	KeepRecent time.Duration
	Now        func() time.Time

	mu      sync.Mutex
	running atomic.Bool
}

func (b *Budget) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Budget) keep() time.Duration {
	if b.KeepRecent > 0 {
		return b.KeepRecent
	}
	return DefaultKeepRecent
}

// Items lists what the pass weighs.
func (b *Budget) Items() []BudgetItem {
	var out []BudgetItem
	if b.Store != nil {
		for n, e := range b.Store.Entries("") {
			out = append(out, BudgetItem{Kind: BudgetStore, Name: n, Size: e.Size, Used: e.Used})
		}
	}
	if b.Git != nil {
		for _, m := range b.Git.OnDisk() {
			out = append(out, BudgetItem{Kind: BudgetGit, Name: m.Repo, Size: m.Size, Used: m.LastRefresh})
		}
	}
	return out
}

// Used is the bytes the hub keeps now: the store (each blob once) and the git
// mirrors.
func (b *Budget) Used() int64 {
	var n int64
	if b.Store != nil {
		n += b.Store.Size()
	}
	if b.Git != nil {
		for _, m := range b.Git.OnDisk() {
			n += m.Size
		}
	}
	return n
}

// Enforce removes the least recently used items of any kind until the hub fits
// its budget. A git mirror used within KeepRecent stays, so the hub may stay
// over the budget while jobs fetch. It returns the bytes freed and what went.
func (b *Budget) Enforce() (int64, []BudgetItem) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Bytes == nil {
		return 0, nil
	}
	limit := b.Bytes()
	if limit <= 0 {
		return 0, nil
	}
	total := b.Used()
	if total <= limit {
		return 0, nil
	}
	items := b.Items()
	sort.Slice(items, func(i, j int) bool {
		if !items[i].Used.Equal(items[j].Used) {
			return items[i].Used.Before(items[j].Used)
		}
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Name < items[j].Name
	})
	now := b.now()
	var freed int64
	var gone []BudgetItem
	for _, it := range items {
		if total <= limit {
			break
		}
		var n int64
		var err error
		switch it.Kind {
		case BudgetGit:
			if now.Sub(it.Used) < b.keep() {
				continue
			}
			n, err = b.Git.Drop(it.Name)
		default:
			n, err = b.Store.Remove(it.Name)
		}
		if err != nil {
			continue
		}
		total -= n
		freed += n
		gone = append(gone, it)
	}
	return freed, gone
}

// Kick runs one pass in the background unless one runs already; the proxy
// calls it after each stored package.
func (b *Budget) Kick() {
	if !b.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer b.running.Store(false)
		b.Enforce()
	}()
}

// Run passes every period until ctx ends (R5.73: every 10 minutes).
func (b *Budget) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	b.Enforce()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.Enforce()
		}
	}
}

// OnDisk lists every git mirror in the folder, with the size on disk and the
// last refresh (or the folder's change time when this run has not refreshed
// it), so mirrors made before a restart count too.
func (g *Git) OnDisk() []MirrorState {
	known := map[string]MirrorState{}
	for _, s := range g.States() {
		known[s.Repo] = s
	}
	es, err := os.ReadDir(g.Dir)
	if err != nil {
		return nil
	}
	var out []MirrorState
	for _, e := range es {
		name, ok := strings.CutSuffix(e.Name(), ".git")
		if !e.IsDir() || !ok {
			continue
		}
		repo := strings.Replace(name, "__", "/", 1)
		if !ValidRepo(repo) {
			continue
		}
		p := filepath.Join(g.Dir, e.Name())
		st := MirrorState{Repo: repo, Path: p, Size: dirSize(p), Status: StatusOK}
		if k, ok := known[repo]; ok {
			st.LastRefresh, st.Status, st.Error = k.LastRefresh, k.Status, k.Error
		}
		if st.LastRefresh.IsZero() {
			if fi, err := e.Info(); err == nil {
				st.LastRefresh = fi.ModTime()
			}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}
