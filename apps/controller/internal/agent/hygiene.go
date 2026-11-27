package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// The agent's side of the clean-up of persistent runners (runners R5.90, R5.91).
// The hooks of a runner clean the job's temporary files before and after every
// job and write the result into the runner folder; the agent reads it, measures
// the runner's own dependency folder and tells the controller. The controller
// answers with the keep setting and a Clear now, which the agent carries out
// when the runner is idle.

type sysFS struct{}

func (sysFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) } //nolint:gosec // the runner folder the agent made

func (sysFS) WriteFile(p string, d []byte, perm os.FileMode) error { return os.WriteFile(p, d, perm) }

// managesClean is true for a runner folder whose hooks clean: its .env names the
// folder, or the clean-up already left a file there.
func managesClean(folder string) bool {
	for _, n := range []string{hygiene.SettingsFile, hygiene.StateFile} {
		if _, err := os.Stat(filepath.Join(folder, n)); err == nil {
			return true
		}
	}
	b, err := os.ReadFile(filepath.Join(folder, ".env")) //nolint:gosec // the runner folder the agent made
	return err == nil && strings.Contains(string(b), hygiene.EnvRunnerDir+"=")
}

// dependencyBytes is the size of the runner's own dependency folder.
func dependencyBytes(fsys runnercache.FS, folder string) int64 {
	es, err := runnercache.Scan(fsys, runnercache.DependencyRoot(folder, filepath.Join))
	if err != nil {
		return 0
	}
	return runnercache.Total(es)
}

// ContainerRunners is the persistent Linux runners of the pool container of a
// Windows machine (runners R5.93): their folders are inside the container, so
// the container backend reads and changes them.
type ContainerRunners interface {
	// Clean gives the state of each runner: name, dependency bytes, unclean reason.
	Clean(ctx context.Context) ([]ContainerClean, error)
	// Apply writes the keep setting of a runner and clears its dependencies when asked.
	Apply(ctx context.Context, name string, keep, clear bool) error
}

// ContainerClean is one persistent container runner's clean-up state.
type ContainerClean struct {
	Name            string
	DependencyBytes int64
	UncleanReason   string
}

// containerSource is the part of a CacheSource that also has container runners.
type containerSource interface{ ContainerRunners() ContainerRunners }

// ContainerRunners implements containerSource.
func (f FoundRunners) ContainerRunners() ContainerRunners { return f.Container }

// CleanStatuses reads the state of every runner whose hooks clean.
func CleanStatuses(ctx context.Context, src CacheSource, fsys runnercache.FS) []*agentv1.RunnerCleanStatus {
	if src == nil {
		return nil
	}
	rs, err := src.Runners(ctx)
	if err != nil {
		return nil
	}
	var out []*agentv1.RunnerCleanStatus
	if cs, ok := src.(containerSource); ok && cs.ContainerRunners() != nil {
		if states, err := cs.ContainerRunners().Clean(ctx); err == nil {
			for _, s := range states {
				out = append(out, &agentv1.RunnerCleanStatus{RunnerName: s.Name, DependencyCacheBytes: s.DependencyBytes, UncleanReason: s.UncleanReason})
			}
		}
	}
	for _, r := range rs {
		if r.Folder == "" || !managesClean(r.Folder) {
			continue
		}
		st, err := hygiene.ReadState(sysFS{}, r.Folder)
		reason := st.UncleanReason
		if err != nil {
			reason = "the result of the last clean-up cannot be read"
		}
		out = append(out, &agentv1.RunnerCleanStatus{
			RunnerName: r.Name, ServiceName: r.Service, DependencyCacheBytes: dependencyBytes(fsys, r.Folder), UncleanReason: reason,
		})
	}
	return out
}

func (l *link) reportClean(ctx context.Context) {
	st := CleanStatuses(ctx, l.cfg.Caches, l.cfg.cacheFS())
	if len(st) == 0 {
		return
	}
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerClean{RunnerClean: &agentv1.RunnerClean{Runners: st}}})
}

// hygieneKeeper applies RunnerHygiene answers, one runner at a time.
type hygieneKeeper struct {
	mu     sync.Mutex
	active map[string]bool
}

func (l *link) applyHygiene(ctx context.Context, m *agentv1.RunnerHygiene) {
	if l.cfg.Caches == nil {
		return
	}
	rs, err := l.cfg.Caches.Runners(ctx)
	if err != nil {
		return
	}
	var folder string
	for _, r := range rs {
		if r.Name == m.GetRunnerName() && r.Folder != "" {
			folder = r.Folder
		}
	}
	if folder == "" {
		l.applyContainerHygiene(ctx, m)
		return
	}
	if hygiene.ReadKeep(sysFS{}, folder) != m.GetKeepDependencies() {
		if err := hygiene.WriteKeep(sysFS{}, folder, m.GetKeepDependencies()); err != nil {
			l.cfg.Logger.Warn("hygiene setting not written", "runner", m.GetRunnerName(), "err", err)
			return
		}
	}
	// A Clear now, or a setting turned off while dependencies are still there.
	if !m.GetClearCache() && (m.GetKeepDependencies() || dependencyBytes(l.cfg.cacheFS(), folder) == 0) {
		return
	}
	key := m.GetRunnerName()
	l.hyg.mu.Lock()
	if l.hyg.active == nil {
		l.hyg.active = map[string]bool{}
	}
	if l.hyg.active[key] {
		l.hyg.mu.Unlock()
		return
	}
	l.hyg.active[key] = true
	l.hyg.mu.Unlock()
	go func() {
		defer func() { l.hyg.mu.Lock(); delete(l.hyg.active, key); l.hyg.mu.Unlock() }()
		l.clearDependencies(ctx, key, folder)
	}()
}

// clearDependencies waits until the runner is idle, then empties its dependency
// folder (never during a job: the idle check and the hold are one step) and
// reports, which ends the Clear now at the controller.
func (l *link) clearDependencies(ctx context.Context, runner, folder string) {
	poll := l.cfg.DrainPollEvery
	if poll <= 0 {
		poll = 2 * time.Second
	}
	gate := l.cfg.Gate
	defer gate.ReleaseRunner(runner)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		idle, _ := gate.HoldRunner(runner)
		if idle {
			if _, err := wipeDir(l.cfg.cacheFS(), runnercache.DependencyRoot(folder, filepath.Join)); err != nil {
				l.cfg.Logger.Warn("dependency cache not cleared", "runner", runner, "err", err)
			}
			l.reportClean(ctx)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// applyContainerHygiene is applyHygiene for a runner in the pool container. The
// controller sent a Clear now or a changed setting; the agent carries it out
// when the runner is idle and reports again.
func (l *link) applyContainerHygiene(ctx context.Context, m *agentv1.RunnerHygiene) {
	cs, ok := l.cfg.Caches.(containerSource)
	if !ok || cs.ContainerRunners() == nil {
		return
	}
	cr := cs.ContainerRunners()
	name := m.GetRunnerName()
	known := false
	if states, err := cr.Clean(ctx); err == nil {
		for _, s := range states {
			known = known || s.Name == name
		}
	}
	if !known {
		return
	}
	if !m.GetClearCache() {
		if err := cr.Apply(ctx, name, m.GetKeepDependencies(), false); err != nil {
			l.cfg.Logger.Warn("hygiene setting not written", "runner", name, "err", err)
		}
		if m.GetKeepDependencies() {
			return
		}
	}
	l.hyg.mu.Lock()
	if l.hyg.active == nil {
		l.hyg.active = map[string]bool{}
	}
	if l.hyg.active[name] {
		l.hyg.mu.Unlock()
		return
	}
	l.hyg.active[name] = true
	l.hyg.mu.Unlock()
	go func() {
		defer func() { l.hyg.mu.Lock(); delete(l.hyg.active, name); l.hyg.mu.Unlock() }()
		poll := l.cfg.DrainPollEvery
		if poll <= 0 {
			poll = 2 * time.Second
		}
		defer l.cfg.Gate.ReleaseRunner(name)
		t := time.NewTicker(poll)
		defer t.Stop()
		for {
			if idle, _ := l.cfg.Gate.HoldRunner(name); idle {
				if err := cr.Apply(ctx, name, m.GetKeepDependencies(), true); err != nil {
					l.cfg.Logger.Warn("dependency cache not cleared", "runner", name, "err", err)
				}
				l.reportClean(ctx)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
