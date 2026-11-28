package mirror

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// Card kinds of the hub (R5.77).
const (
	CardActions  = "actions_cache"
	CardGit      = "git_mirror"
	CardRegistry = "registry_mirror"
	CardPackages = "package_proxy"
)

// HubCard is one card of the Caches tab.
type HubCard struct {
	Kind     string
	On       bool
	Size     int64
	Entries  int
	Hits     int64
	Misses   int64
	Served   int64
	LastUsed time.Time
	Status   string // ok, waiting, failed
	Note     string // one plain line, such as why the receiver waits
	WipeID   string // the cache id Wipe takes
}

// HubRepo is one row of Warmth by repository.
type HubRepo struct {
	Repo   string
	Hits   int64
	Misses int64
	Size   int64
	Warmth int
}

// Hub is what the Caches tab shows of the one hub (R5.77, R5.78).
type Hub struct {
	Settings     Settings
	BudgetBytes  int64
	UsedBytes    int64
	Cards        []HubCard
	Hits         int64
	Misses       int64
	Served       int64
	WarmMachines int
	Repos        []HubRepo
}

// Hub gathers the settings, the budget and the numbers of every kind.
func (s *Service) Hub(ctx context.Context) (Hub, error) {
	set, err := s.Get(ctx)
	if err != nil {
		return Hub{}, err
	}
	h := Hub{Settings: set, BudgetBytes: set.BudgetBytes()}
	repos := map[string]*HubRepo{}
	repo := func(k string) *HubRepo {
		r := repos[k]
		if r == nil {
			r = &HubRepo{Repo: k}
			repos[k] = r
		}
		return r
	}

	act := HubCard{Kind: CardActions, On: set.ActionsCache, Status: StatusOK, WipeID: IDPrefix + ScopeActionsAll}
	if s.Actions != nil {
		for _, x := range s.Actions.RepoStats() {
			act.Entries += x.Entries
			act.Size += x.Size
			act.Hits += x.Hits
			act.Misses += x.Misses
			act.Served += x.BytesServed
			if x.LastHit.After(act.LastUsed) {
				act.LastUsed = x.LastHit
			}
			r := repo(x.Repo)
			r.Hits += x.Hits
			r.Misses += x.Misses
			r.Size += x.Size
		}
	}
	if act.On && !s.Listening {
		act.Status, act.Note = StatusWaiting, "the receiver does not listen on the LAN yet"
	}

	git := HubCard{Kind: CardGit, On: set.GitMirror, Status: StatusOK, WipeID: IDPrefix + ScopeGitAll}
	if s.Git != nil {
		for _, m := range s.Git.OnDisk() {
			git.Entries++
			git.Size += m.Size
			if m.LastRefresh.After(git.LastUsed) {
				git.LastUsed = m.LastRefresh
			}
			if m.Status == StatusFailed {
				git.Status, git.Note = StatusFailed, m.Repo+": "+m.Error
			}
			repo(m.Repo).Size += m.Size
		}
	}

	reg := HubCard{Kind: CardRegistry, On: set.RegistryMirror, Status: StatusOK, WipeID: IDPrefix + ScopeRegistry}
	if s.RegistrySize != nil {
		reg.Size = s.RegistrySize()
	}

	pkg := HubCard{Kind: CardPackages, On: set.PackageProxy, Status: StatusOK, WipeID: IDPrefix + ScopePackages,
		Note: "npm, PyPI, crates and apt"}
	if s.ActStore != nil {
		for n, e := range s.ActStore.Entries(lancache.NSPackages + "/") {
			pkg.Entries++
			pkg.Size += e.Size
			if e.Used.After(pkg.LastUsed) {
				pkg.LastUsed = e.Used
			}
			if parts := strings.SplitN(n, "/", 3); len(parts) == 3 {
				repo(displayRepo(parts[1])).Size += e.Size
			}
		}
	}
	if s.Proxy != nil {
		for _, x := range s.Proxy.RepoStats() {
			pkg.Hits += x.Hits
			pkg.Misses += x.Misses
			pkg.Served += x.BytesServed
			r := repo(displayRepo(x.Repo))
			r.Hits += x.Hits
			r.Misses += x.Misses
		}
	}
	h.Cards = []HubCard{act, git, reg, pkg}
	for _, c := range h.Cards {
		h.Hits += c.Hits
		h.Misses += c.Misses
		h.Served += c.Served
	}

	if s.Budget != nil {
		h.UsedBytes = s.Budget.Used()
	} else {
		h.UsedBytes = act.Size + git.Size + pkg.Size
	}
	if s.Warmth != nil {
		byRepo, warm := s.Warmth.Snapshot()
		h.WarmMachines = warm
		for k, v := range byRepo {
			if r := repo(k); v > r.Warmth {
				r.Warmth = v
			}
		}
	}
	for _, r := range repos {
		h.Repos = append(h.Repos, *r)
	}
	sort.Slice(h.Repos, func(i, j int) bool { return h.Repos[i].Repo < h.Repos[j].Repo })
	return h, nil
}

// displayRepo turns a proxy key (owner__repo) back into owner/repo.
func displayRepo(key string) string { return strings.Replace(key, "__", "/", 1) }

// Snapshot is the highest warmth now of each repository over all machines,
// and the number of machines with any warmth.
func (w *Warmth) Snapshot() (map[string]int, int) {
	w.mu.Lock()
	keys := make([]warmKey, 0, len(w.cells))
	for k := range w.cells {
		keys = append(keys, k)
	}
	w.mu.Unlock()
	byRepo := map[string]int{}
	warm := map[string]bool{}
	for _, k := range keys {
		v := w.Value(k.machine, k.repo)
		if v > byRepo[k.repo] {
			byRepo[k.repo] = v
		}
		if v > 0 {
			warm[k.machine] = true
		}
	}
	return byRepo, len(warm)
}
