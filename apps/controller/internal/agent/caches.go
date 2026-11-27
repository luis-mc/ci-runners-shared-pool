package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// Cache kinds the agent reports and wipes (caches R5.36).
const (
	CacheKindTool = "tool_cache"
	CacheKindRust = "rust_cache"
	// CacheKindDependency is the runner's own dependency folder (caches R5.56).
	CacheKindDependency = "dependency_cache"
)

// CacheRunner is one runner whose cache folders the agent measures. The agent
// finds the folder itself; the controller never sends a path.
type CacheRunner struct {
	Name    string
	Service string
	Folder  string
}

// CacheSource lists the runners on this machine.
type CacheSource interface {
	Runners(ctx context.Context) ([]CacheRunner, error)
}

// FoundRunners reads the runners from the OS service list (runners R5.5).
type FoundRunners struct {
	Find func(ctx context.Context) ([]CacheRunner, error)
	// Container is the persistent Linux runners in the pool container; nil on a machine without it.
	Container ContainerRunners
}

// Runners implements CacheSource.
func (f FoundRunners) Runners(ctx context.Context) ([]CacheRunner, error) {
	if f.Find == nil {
		return nil, nil
	}
	return f.Find(ctx)
}

// cacheDir is the folder of one kind under a runner folder.
func cacheDir(folder, kind string) (string, bool) {
	d := runnercache.DirsFor(folder)
	switch kind {
	case CacheKindTool:
		return d.Tools, true
	case CacheKindRust:
		return d.Rust, true
	case CacheKindDependency:
		return runnercache.DependencyRoot(folder, filepath.Join), true
	}
	return "", false
}

// readQuotas reads the declared caps from the runner's own .env (caches R5.39).
// A runner without them has no cap.
func readQuotas(folder string) map[string]int64 {
	b, err := os.ReadFile(filepath.Join(folder, ".env")) //nolint:gosec // the runner folder the agent made
	if err != nil {
		return nil
	}
	kv := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(ln), "="); ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	out := map[string]int64{}
	for kind, key := range map[string]string{CacheKindTool: runnercache.EnvToolQuota, CacheKindRust: runnercache.EnvRustCap} {
		if gib, err := strconv.Atoi(kv[key]); err == nil && gib > 0 {
			out[kind] = int64(gib) * runnercache.GiB
		}
	}
	return out
}

// MeasureCaches sizes both cache folders of every runner. A folder that does
// not exist yet is an empty cache, not an error. A runner whose folder cannot be
// read is left out of the report.
func MeasureCaches(ctx context.Context, src CacheSource, fsys runnercache.FS) []*agentv1.CacheStatus {
	if src == nil {
		return nil
	}
	runners, err := src.Runners(ctx)
	if err != nil {
		return nil
	}
	var out []*agentv1.CacheStatus
	for _, r := range runners {
		if r.Folder == "" {
			continue
		}
		quotas := readQuotas(r.Folder)
		for _, kind := range []string{CacheKindTool, CacheKindRust} {
			dir, _ := cacheDir(r.Folder, kind)
			es, err := runnercache.Scan(fsys, dir)
			if err != nil {
				continue
			}
			out = append(out, &agentv1.CacheStatus{
				RunnerName: r.Name, ServiceName: r.Service, Kind: kind,
				SizeBytes: runnercache.Total(es), CapBytes: quotas[kind], EntryCount: int32(len(es)), //nolint:gosec // small
			})
		}
		// A persistent runner whose hooks clean has its own dependency folder: a line of its own.
		if managesClean(r.Folder) {
			out = append(out, &agentv1.CacheStatus{
				RunnerName: r.Name, ServiceName: r.Service, Kind: CacheKindDependency, SizeBytes: dependencyBytes(fsys, r.Folder),
			})
		}
	}
	return out
}

// wiper carries out WipeCache commands one runner at a time.
type wiper struct {
	cfg  Config
	send func(*agentv1.SessionRequest) error
	fsys runnercache.FS
	poll time.Duration

	mu     sync.Mutex
	active map[string]bool // runner name + kind with a wipe in progress
}

func (w *wiper) report(id, state string, waiting []machines.JobRef, freed int64, errText string) {
	st := &agentv1.CacheWipeStatus{CommandId: id, State: state, FreedBytes: freed, Error: errText}
	for _, j := range waiting {
		st.WaitingFor = append(st.WaitingFor, jobToProto(j))
	}
	_ = w.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_CacheWipeStatus{CacheWipeStatus: st}})
}

// start finds the folder from the agent's own runner list, then waits for the
// runner to be idle in a goroutine and deletes. Nothing is deleted before the
// idle check that runs right before the delete (runners R6.4).
func (w *wiper) start(ctx context.Context, m *agentv1.WipeCache) {
	id := m.GetCommandId()
	fail := func(msg string) { w.report(id, "failed", nil, 0, msg) }
	if _, ok := cacheDir("x", m.GetKind()); !ok {
		fail("this cache cannot be wiped here")
		return
	}
	if w.cfg.Caches == nil {
		fail("this machine has no runners")
		return
	}
	runners, err := w.cfg.Caches.Runners(ctx)
	if err != nil {
		fail("the runner list cannot be read, so nothing was wiped")
		return
	}
	var folder string
	for _, r := range runners {
		if r.Name == m.GetRunnerName() && r.Folder != "" {
			folder = r.Folder
		}
	}
	if folder == "" {
		fail("no runner of that name on this machine")
		return
	}
	dir, _ := cacheDir(folder, m.GetKind())
	key := m.GetRunnerName() + "/" + m.GetKind()
	w.mu.Lock()
	if w.active == nil {
		w.active = map[string]bool{}
	}
	if w.active[key] {
		w.mu.Unlock()
		fail("a wipe of this cache is already waiting")
		return
	}
	w.active[key] = true
	w.mu.Unlock()
	go func() {
		defer func() { w.mu.Lock(); delete(w.active, key); w.mu.Unlock() }()
		w.run(ctx, id, m.GetRunnerName(), dir)
	}()
}

func (w *wiper) run(ctx context.Context, id, runner, dir string) {
	poll := w.poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	gate := w.cfg.Gate
	defer gate.ReleaseRunner(runner)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		// Hold and the idle answer are one step, so no job slipped in since.
		idle, waiting := gate.HoldRunner(runner)
		if idle {
			w.report(id, "wiping", nil, 0, "")
			freed, err := wipeDir(w.fsys, dir)
			if err != nil {
				w.cfg.Logger.Warn("cache wipe failed", "runner", runner, "err", err)
				w.report(id, "failed", nil, freed, "the folder could not be emptied; "+shortErr(err))
				return
			}
			w.report(id, "done", nil, freed, "")
			return
		}
		w.report(id, "waiting", waiting, 0, "")
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func shortErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return "see the agent log"
}

// wipeDir removes every top-level entry of dir and keeps dir itself, so the
// runner's environment still points at a folder that exists. It returns the
// bytes it removed.
func wipeDir(fsys runnercache.FS, dir string) (int64, error) {
	es, err := runnercache.Scan(fsys, dir)
	if err != nil {
		return 0, err
	}
	var freed int64
	for _, e := range es {
		if err := fsys.RemoveAll(e.Path); err != nil {
			return freed, fmt.Errorf("remove %s: %w", filepath.Base(e.Path), err)
		}
		freed += e.Size
	}
	return freed, nil
}
