package main

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
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
	st, err := lancache.Open(layout.Actions(), 20<<30, nil)
	if err != nil {
		logger.Warn("the Actions cache store did not open", "err", err)
	} else {
		m.ActStore, m.ActCap = st, 20<<30
		m.Actions, _ = lancache.NewServer(st, nil, "", nil)
	}
	m.Wiper = &mirror.Wiper{Targets: mirror.TargetsFor(m.Actions, m.ActStore, m.Git, m.Tool, layout.Registry()),
		Jobs: runningJobs{svc: svc}, Warmth: m.Warmth, Audit: rec, Machine: cachesHolder, Poll: 5 * time.Second, Wait: time.Hour}
	return m
}

const cachesHolder = "controller"
