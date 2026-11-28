package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches/toolcache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/config"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/lancache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/mirror"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// runningJobs says which jobs of the pool run now; a wipe waits for them all,
// because any job may read the shared caches (caches R5.32, R5.69).
type runningJobs struct{ svc *machines.Service }

func (r runningJobs) Using(ctx context.Context, _, _ string) ([]string, error) {
	vs, err := r.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, v := range vs {
		for _, j := range v.RunningJobs {
			out = append(out, fmt.Sprintf("%s run %d, job %s on %s", j.Repository, j.RunID, j.Job, v.Machine.Name))
		}
	}
	return out, nil
}

// newMirrorService builds the cache holder side of the Caches tab (caches
// R5.65 to R5.71). Its folders are under the data folder of the controller.
// The Actions receiver listener is not started here (caches section 11).
func newMirrorService(cfg config.Config, db *store.Store, rec audit.Recorder, svc *machines.Service, logger *slog.Logger) *mirror.Service {
	layout := mirror.Layout{Base: filepath.Join(cfg.DataDir, "mirrors")}
	m := &mirror.Service{Settings: settings.New(db), Layout: layout, Warmth: &mirror.Warmth{}, Machine: cachesHolder}
	m.Git = &mirror.Git{Dir: layout.Git(), Run: sys.ExecCommands{}}
	m.Tool = &toolcache.Store{Root: toolcache.DefaultRoot(runtime.GOOS)}
	// One budget for every kind of the hub (caches R5.73): the content store's
	// cap is the budget, and the budget pass weighs the store and the mirrors.
	budget := func() int64 {
		set, _ := m.Get(context.Background())
		return set.BudgetBytes()
	}
	capBytes := budget()
	st, err := lancache.Open(layout.Actions(), capBytes, nil)
	if err != nil {
		logger.Warn("the cache store did not open", "err", err)
	} else {
		m.ActStore, m.ActCap = st, capBytes
		m.Actions, _ = lancache.NewServer(st, nil, "", nil)
		m.Budget = &mirror.Budget{Store: st, Git: m.Git, Bytes: budget}
		// The npm and PyPI proxy (caches R5.74 to R5.76, R5.80), on the controller listener.
		if secret, err := lancache.LoadSecret(filepath.Join(layout.Base, "packages.key")); err != nil {
			logger.Warn("the package proxy did not start", "err", err)
		} else {
			m.Proxy = &lancache.Proxy{Store: st, Secret: secret, AfterPut: m.Budget.Kick, On: func() bool {
				set, err := m.Get(context.Background())
				return err == nil && set.PackageProxy
			}}
		}
	}
	m.Wiper = &mirror.Wiper{Targets: mirror.TargetsFor(m.Actions, m.ActStore, m.Git, m.Tool, layout.Registry()),
		Jobs: runningJobs{svc: svc}, Warmth: m.Warmth, Audit: rec, Machine: cachesHolder, Poll: 5 * time.Second, Wait: time.Hour}
	return m
}

// hubGrant gives a runner of one repository its package proxy key and token
// while the package proxy is on (caches R5.76). An org-wide runner (no
// repository in its address) gets none.
func hubGrant(m *mirror.Service) func(ctx context.Context, githubURL string) lancache.ProxyGrant {
	return func(ctx context.Context, githubURL string) lancache.ProxyGrant {
		if m == nil || m.Proxy == nil {
			return lancache.ProxyGrant{}
		}
		set, err := m.Get(ctx)
		if err != nil || !set.PackageProxy {
			return lancache.ProxyGrant{}
		}
		u, err := url.Parse(githubURL)
		if err != nil {
			return lancache.ProxyGrant{}
		}
		key, ok := lancache.RepoKey(strings.Trim(u.Path, "/"))
		if !ok {
			return lancache.ProxyGrant{}
		}
		return lancache.ProxyGrant{Repo: key, Token: lancache.Token(m.Proxy.Secret, key)}
	}
}

const cachesHolder = "controller"
