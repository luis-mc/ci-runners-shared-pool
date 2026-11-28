package mirror

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches/toolcache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
)

// ActionsTarget wipes the Actions receiver (scopes actions_entry, actions_repo,
// actions_all). A name is "<repo id>" or "<repo id>/<key>/<version>" with the
// key and version already escaped as the receiver stores them.
type ActionsTarget struct {
	Server *lancache.Server
	Store  *lancache.Store
	Kind   string // one of the three action scopes
}

func (a ActionsTarget) prefix(name string) string {
	switch a.Kind {
	case ScopeActionsAll:
		return lancache.NSActions + "/"
	case ScopeActionsRepo:
		return lancache.NSActions + "/" + name + "/"
	}
	return lancache.NSActions + "/" + name
}

func (a ActionsTarget) Plan(_ context.Context, name string) ([]Item, error) {
	if a.Kind != ScopeActionsAll && name == "" {
		return nil, fmt.Errorf("name the repository or entry to wipe")
	}
	es := a.Store.Entries(a.prefix(name))
	var out []Item
	var total int64
	for n, e := range es {
		if a.Kind == ScopeActionsEntry {
			out = append(out, Item{Text: n[len(lancache.NSActions)+1:], Size: e.Size})
		}
		total += e.Size
	}
	if a.Kind != ScopeActionsEntry {
		out = []Item{{Text: fmt.Sprintf("%d Actions cache entries", len(es)), Size: total}}
	}
	return out, nil
}

func (a ActionsTarget) Delete(_ context.Context, name string) (int64, error) {
	freed, _, err := a.Store.RemovePrefix(a.prefix(name))
	if a.Server != nil {
		switch a.Kind {
		case ScopeActionsAll:
			_, _, _ = a.Server.WipeAll()
		case ScopeActionsRepo:
			a.Server.ForgetStats(name)
		}
	}
	return freed, err
}

func (ActionsTarget) Warm() []string { return []string{WarmActions} }

// GitTarget wipes one git mirror, named by owner/repo.
type GitTarget struct{ Git *Git }

func (g GitTarget) Plan(_ context.Context, name string) ([]Item, error) {
	p, err := g.Git.Path(name)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(p); err != nil {
		return nil, fmt.Errorf("no git mirror of %s", name)
	}
	return []Item{{Text: "git mirror of " + name, Size: dirSize(p)}}, nil
}

func (g GitTarget) Delete(_ context.Context, name string) (int64, error) { return g.Git.Drop(name) }
func (GitTarget) Warm() []string                                         { return []string{WarmGit} }

// PackagesTarget wipes every package proxy entry (R5.73).
type PackagesTarget struct{ Store *lancache.Store }

func (p PackagesTarget) Plan(_ context.Context, _ string) ([]Item, error) {
	es := p.Store.Entries(lancache.NSPackages + "/")
	var total int64
	for _, e := range es {
		total += e.Size
	}
	if len(es) == 0 {
		return nil, nil
	}
	return []Item{{Text: fmt.Sprintf("%d package proxy entries", len(es)), Size: total}}, nil
}

func (p PackagesTarget) Delete(_ context.Context, _ string) (int64, error) {
	freed, _, err := p.Store.RemovePrefix(lancache.NSPackages + "/")
	return freed, err
}

func (PackagesTarget) Warm() []string { return nil }

// GitAllTarget wipes every git mirror (R5.73).
type GitAllTarget struct{ Git *Git }

func (g GitAllTarget) Plan(_ context.Context, _ string) ([]Item, error) {
	var out []Item
	for _, m := range g.Git.OnDisk() {
		out = append(out, Item{Text: "git mirror of " + m.Repo, Size: m.Size})
	}
	return out, nil
}

func (g GitAllTarget) Delete(_ context.Context, _ string) (int64, error) {
	var n int64
	for _, m := range g.Git.OnDisk() {
		freed, err := g.Git.Drop(m.Repo)
		if err != nil {
			return n, err
		}
		n += freed
	}
	return n, nil
}

func (GitAllTarget) Warm() []string { return []string{WarmGit} }

// ToolTarget wipes the machine's tool cache (the whole of it).
type ToolTarget struct{ Store *toolcache.Store }

func (t ToolTarget) Plan(_ context.Context, _ string) ([]Item, error) {
	es, err := t.Store.Entries()
	if err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(es))
	for _, e := range es {
		out = append(out, Item{Text: e.Tool + " " + e.Version, Size: e.Size})
	}
	return out, nil
}

func (t ToolTarget) Delete(_ context.Context, _ string) (int64, error) {
	es, _ := t.Store.Entries()
	var n int64
	for _, e := range es {
		n += e.Size
	}
	if _, err := t.Store.Wipe(); err != nil {
		return 0, err
	}
	return n, nil
}

func (ToolTarget) Warm() []string { return []string{WarmTools} }

// FolderTarget wipes a plain data folder, such as the registry mirror's.
type FolderTarget struct {
	Dir   string
	Label string
	Kinds []string
}

func (f FolderTarget) Plan(_ context.Context, _ string) ([]Item, error) {
	if _, err := os.Stat(f.Dir); err != nil {
		return nil, nil
	}
	return []Item{{Text: f.Label, Size: dirSize(f.Dir)}}, nil
}

func (f FolderTarget) Delete(_ context.Context, _ string) (int64, error) {
	n := dirSize(f.Dir)
	es, err := os.ReadDir(f.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	for _, e := range es {
		if err := os.RemoveAll(filepath.Join(f.Dir, e.Name())); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (f FolderTarget) Warm() []string { return f.Kinds }

// TargetsFor builds the wipe targets of a holder. Parts that are nil give no
// target for their scope.
func TargetsFor(srv *lancache.Server, st *lancache.Store, git *Git, tool *toolcache.Store, registryDir string) map[string]Target {
	t := map[string]Target{}
	if st != nil {
		for _, k := range []string{ScopeActionsEntry, ScopeActionsRepo, ScopeActionsAll} {
			t[k] = ActionsTarget{Server: srv, Store: st, Kind: k}
		}
	}
	if st != nil {
		t[ScopePackages] = PackagesTarget{Store: st}
	}
	if git != nil {
		t[ScopeGitMirror] = GitTarget{Git: git}
		t[ScopeGitAll] = GitAllTarget{Git: git}
	}
	if tool != nil {
		t[ScopeToolCache] = ToolTarget{Store: tool}
	}
	if registryDir != "" {
		t[ScopeRegistry] = FolderTarget{Dir: registryDir, Label: "registry mirror data", Kinds: nil}
	}
	return t
}
