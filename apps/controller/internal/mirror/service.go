package mirror

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches/toolcache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// IDPrefix marks a mirror row id (the cache id of the API).
const IDPrefix = "mirror:"

// Row is one line of the Caches tab for a cache holder (R5.29, R5.71).
type Row struct {
	ID          string
	Kind        string // the API cache kind
	Name        string // the repository for a git mirror, "" otherwise
	Size        int64
	Cap         int64
	Entries     int
	LastRefresh time.Time
	Status      string
	Error       string
	Hits        int64
	Misses      int64
	BytesServed int64
}

// Service ties the holder parts together for the API (R5.65 to R5.71).
type Service struct {
	Settings SettingsStore
	Layout   Layout
	Git      *Git
	Actions  *lancache.Server
	ActStore *lancache.Store
	Tool     *toolcache.Store
	ActCap   int64
	Wiper    *Wiper
	Warmth   *Warmth
	Machine  string
	// Listening says the receiver answers peers. Until the LAN listener is
	// wired (caches section 11) the row says it is waiting.
	Listening bool
	Now       func() time.Time
	// Proxy is the npm and PyPI proxy (R5.74 to R5.76), Budget the one budget
	// pass (R5.73) and RegistrySize the registry mirror's folder size; nil
	// parts show as empty.
	Proxy        *lancache.Proxy
	Budget       *Budget
	RegistrySize func() int64
}

// Get reads the settings with their defaults.
func (s *Service) Get(ctx context.Context) (Settings, error) {
	v, err := s.Settings.GetMirrorSettings(ctx)
	return v.Normalize(), err
}

// Put validates and stores the settings and writes an audit entry (R5.66).
func (s *Service) Put(ctx context.Context, actor audit.Actor, rec audit.Recorder, v Settings) (Settings, error) {
	if err := v.Validate(); err != nil {
		return Settings{}, err
	}
	v = v.Normalize()
	old, _ := s.Get(ctx)
	if err := s.Settings.SetMirrorSettings(ctx, v); err != nil {
		return Settings{}, err
	}
	if rec != nil && old != v {
		_ = rec.Record(ctx, actor, "cache.settings", audit.Target{Type: "cache", ID: "mirrors"}, map[string]any{
			"summary": "change the cache settings", "from": old, "to": v,
		})
	}
	// The budget applies at once (R5.73): the store's cap follows it and a
	// pass runs.
	if s.ActStore != nil && old.BudgetBytes() != v.BudgetBytes() {
		_ = s.ActStore.SetCap(v.BudgetBytes())
	}
	if s.Budget != nil {
		s.Budget.Kick()
	}
	return v, nil
}

// Rows lists what the holder keeps. A switch that is off gives no rows, so
// the screen shows "off" (caches section 4).
func (s *Service) Rows(ctx context.Context) ([]Row, error) {
	set, err := s.Get(ctx)
	if err != nil {
		return nil, err
	}
	var rows []Row
	if set.ActionsCache && s.Actions != nil {
		r := Row{ID: IDPrefix + ScopeActionsAll, Kind: "actions_cache", Size: s.Actions.ActionsSize(), Cap: s.ActCap, Status: StatusOK}
		if !s.Listening {
			r.Status, r.Error = StatusWaiting, "the receiver does not listen on the LAN yet"
		}
		for _, x := range s.Actions.RepoStats() {
			r.Entries += x.Entries
			r.Hits += x.Hits
			r.Misses += x.Misses
			r.BytesServed += x.BytesServed
		}
		rows = append(rows, r)
	}
	if set.GitMirror && s.Git != nil {
		for _, m := range s.Git.States() {
			rows = append(rows, Row{ID: IDPrefix + ScopeGitMirror + ":" + m.Repo, Kind: "git_mirror", Name: m.Repo,
				Size: m.Size, LastRefresh: m.LastRefresh, Status: m.Status, Error: m.Error})
		}
	}
	if s.Tool != nil {
		es, err := s.Tool.Entries()
		if err == nil {
			r := Row{ID: IDPrefix + ScopeToolCache, Kind: "tool_cache", Status: StatusOK, Entries: len(es)}
			for _, e := range es {
				r.Size += e.Size
			}
			rows = append(rows, r)
		}
	}
	if set.RegistryMirror {
		size := dirSize(s.Layout.Registry())
		if s.RegistrySize != nil {
			size = s.RegistrySize()
		}
		rows = append(rows, Row{ID: IDPrefix + ScopeRegistry, Kind: "registry_mirror", Size: size, Status: StatusOK})
	}
	return rows, nil
}

// ParseID turns a row id into a wipe scope and target.
func ParseID(id string) (scope, target string, ok bool) {
	rest, found := strings.CutPrefix(id, IDPrefix)
	if !found {
		return "", "", false
	}
	scope, target, _ = strings.Cut(rest, ":")
	switch scope {
	case ScopeActionsAll, ScopeToolCache, ScopeRegistry, ScopePackages, ScopeGitAll:
		return scope, "", target == ""
	case ScopeGitMirror, ScopeActionsRepo:
		return scope, target, target != ""
	}
	return "", "", false
}

// Refresh refreshes every wanted mirror; the controller calls it on a timer
// and on a push webhook (R5.11). A mirror that fails stays failed until the
// next call.
func (s *Service) Refresh(ctx context.Context, repos []string) error {
	set, err := s.Get(ctx)
	if err != nil || !set.GitMirror {
		return err
	}
	var first error
	for _, r := range repos {
		if err := s.Git.Refresh(ctx, r); err != nil && first == nil {
			first = fmt.Errorf("%s: %w", r, err)
		}
	}
	return first
}

// RefreshEvery is the timer period from the setting.
func (s *Service) RefreshEvery(ctx context.Context) time.Duration {
	set, _ := s.Get(ctx)
	return time.Duration(set.GitRefreshMin) * time.Minute
}

// Wipe plans or runs the wipe of one row. Without confirm it returns the plan
// and ErrNeedsYes.
func (s *Service) Wipe(ctx context.Context, actor audit.Actor, admin, confirm bool, id string) (Plan, int64, error) {
	scope, target, ok := ParseID(id)
	if !ok {
		return Plan{}, 0, ErrNoScope
	}
	return s.Wiper.Run(ctx, actor, admin, confirm, scope, target)
}
