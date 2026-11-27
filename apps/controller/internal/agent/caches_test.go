package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

type staticRunners []agent.CacheRunner

func (s staticRunners) Runners(context.Context) ([]agent.CacheRunner, error) { return s, nil }

type failingRunners struct{}

func (failingRunners) Runners(context.Context) ([]agent.CacheRunner, error) {
	return nil, errors.New("service list unreadable")
}

func put(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runnerFolder makes a runner folder with a tool cache of 300 bytes in two
// entries, a Rust cache of 50 bytes and a .env with the declared quotas.
func runnerFolder(t *testing.T) string {
	t.Helper()
	f := t.TempDir()
	d := runnercache.DirsFor(f)
	put(t, filepath.Join(d.Tools, "node", "22", "bin"), 200)
	put(t, filepath.Join(d.Tools, "go", "1.27", "bin"), 100)
	put(t, filepath.Join(d.Rust, "debug", "x"), 50)
	env := "CI_TOOLS_QUOTA_GIB=20\nCRSP_RUST_CACHE_CAP_GIB=40\n"
	if err := os.WriteFile(filepath.Join(f, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMeasureCachesSizesBothFoldersWithTheirQuota(t *testing.T) {
	f := runnerFolder(t)
	got := agent.MeasureCaches(context.Background(), staticRunners{{Name: "r1", Service: "actions.runner.o.r1", Folder: f}}, runnercache.OSFS{})
	if len(got) != 2 {
		t.Fatalf("want a tool and a Rust cache, got %d", len(got))
	}
	tool, rust := got[0], got[1]
	if tool.GetKind() != "tool_cache" || tool.GetSizeBytes() != 300 || tool.GetEntryCount() != 2 || tool.GetCapBytes() != 20*runnercache.GiB {
		t.Fatalf("tool cache: %v", tool)
	}
	if rust.GetKind() != "rust_cache" || rust.GetSizeBytes() != 50 || rust.GetEntryCount() != 1 || rust.GetCapBytes() != 40*runnercache.GiB {
		t.Fatalf("rust cache: %v", rust)
	}
	if tool.GetRunnerName() != "r1" || tool.GetServiceName() != "actions.runner.o.r1" {
		t.Fatalf("names: %v", tool)
	}
}

func TestMeasureCachesWithoutFoldersOrQuotaIsEmptyAndUncapped(t *testing.T) {
	got := agent.MeasureCaches(context.Background(), staticRunners{{Name: "r2", Folder: t.TempDir()}}, runnercache.OSFS{})
	if len(got) != 2 || got[0].GetSizeBytes() != 0 || got[0].GetCapBytes() != 0 {
		t.Fatalf("a runner with no cache folder is an empty cache with no cap: %v", got)
	}
	if got := agent.MeasureCaches(context.Background(), failingRunners{}, runnercache.OSFS{}); got != nil {
		t.Fatal("an unreadable runner list reports nothing, never invented sizes")
	}
	if got := agent.MeasureCaches(context.Background(), nil, runnercache.OSFS{}); got != nil {
		t.Fatal("no source reports nothing")
	}
}

func TestGateHoldRunnerKeepsOnlyThatRunnersJobsOut(t *testing.T) {
	c := startController(t)
	cfg := fastConfig(t, c, "x")
	g := agent.NewGate(cfg.Sampler)
	g.SetSettings(machines.Settings{JobSlots: 4, AllowedBackends: []string{"persistent"}, BudgetMemoryPct: 100, BudgetCPUPct: 100}, machines.StateActive)
	cfg.Sampler.Sample()
	mine := machines.JobRef{Repository: "o/r", RunID: 1, Job: "a", RunnerName: "r1"}
	if d := g.Start("k1", mine, "persistent"); !d.Go {
		t.Skipf("the gate holds on load in this sandbox: %v", d.Reasons)
	}
	idle, waiting := g.HoldRunner("r1")
	if idle || len(waiting) != 1 || waiting[0].RunID != 1 {
		t.Fatalf("a running job of r1 must be named: idle=%v %v", idle, waiting)
	}
	if d := g.Start("k2", machines.JobRef{RunnerName: "r1"}, "persistent"); d.Go {
		t.Fatal("a new job of the held runner started")
	}
	if d := g.Start("k3", machines.JobRef{RunnerName: "r2"}, "persistent"); !d.Go {
		t.Fatalf("another runner must keep working: %v", d.Reasons)
	}
	g.Finish("k1")
	if idle, _ := g.HoldRunner("r1"); !idle {
		t.Fatal("r1 is idle after its job ended")
	}
	g.ReleaseRunner("r1")
	if d := g.Start("k4", machines.JobRef{RunnerName: "r1"}, "persistent"); !d.Go {
		t.Fatalf("after the release r1 takes jobs again: %v", d.Reasons)
	}
}

type recAudit struct {
	mu      sync.Mutex
	entries []string
}

func (r *recAudit) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.entries...)
}

func (r *recAudit) Record(_ context.Context, a audit.Actor, action string, _ audit.Target, d map[string]any) error {
	r.mu.Lock()
	r.entries = append(r.entries, a.Login+" "+action)
	r.mu.Unlock()
	_ = d
	return nil
}
func (r *recAudit) RecordRefused(context.Context, audit.Actor, string, audit.Target, map[string]any) error {
	return nil
}

// Caches R5.32, R5.34, web-console R5.25 end to end with a real agent: the
// agent measures, the controller lists, a wipe waits for the running job and
// names it, keeps new jobs of that runner out, deletes only after the job ends,
// and is audited with the bytes freed.
func TestWipeWaitsForTheRunningJobThenEmptiesTheFolder(t *testing.T) {
	c := startController(t)
	rec := &recAudit{}
	cs := caches.New(caches.Options{Machines: c.Svc, Audit: rec})
	// Every "waiting" report is one poll of the agent's wiper: count them, so the
	// test waits for polls instead of for the clock.
	polls := make(chan struct{}, 1024)
	c.Svc.SetHooks(machines.Hooks{OnHello: cs.OnHello, OnMessage: func(ctx context.Context, id string, m *agentv1.SessionRequest, reply func(*agentv1.SessionResponse)) bool {
		if st := m.GetCacheWipeStatus(); st != nil && st.GetState() == "waiting" {
			select {
			case polls <- struct{}{}:
			default:
			}
		}
		return cs.OnMessage(ctx, id, m, reply)
	}})
	ctx := context.Background()
	code, _ := c.Svc.CreateJoinCode(ctx, "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	folder := runnerFolder(t)
	cfg.Caches = staticRunners{{Name: "r1", Folder: folder}}
	cfg.CacheEvery = 50 * time.Millisecond
	// The agent ends its link after three silent heartbeats. At 50 ms that is
	// 150 ms, which a loaded Windows runner overruns (GC, race build, parallel
	// packages): the link drops, the WipeCache sent on it is lost and the old
	// session's wiper stops without a report, so the controller shows "wiping"
	// for ever. Heartbeats play no part here; keep the link up.
	cfg.HeartbeatEvery = 2 * time.Second
	runAgent(t, cfg)

	var list []caches.Cache
	waitFor(t, "the agent's report", func() bool {
		list, _ = cs.List(ctx, "", "")
		return len(list) == 2
	})
	var tool caches.Cache
	for _, l := range list {
		if l.Kind == caches.KindTool {
			tool = l
		}
	}
	if tool.Size != 300 || tool.Cap != 20*runnercache.GiB || tool.State != caches.StateReady {
		t.Fatalf("tool cache: %+v", tool)
	}
	waitFor(t, "settings", func() bool { _, have := cfg.Gate.Settings(); return have })

	// A job of runner r1 runs.
	jr := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "r1"}
	waitFor(t, "job starts", func() bool { return cfg.Gate.Start("j1", jr, "persistent").Go })
	c.Svc.SetRunning(tool.MachineID, []machines.JobRef{jr}, nil)

	actor := audit.Actor{Login: "luis", Source: audit.SourceWeb}
	_, err := cs.BeginWipe(ctx, actor, tool.ID, false)
	var nc *caches.NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) != 2 || nc.Items[1].Kind != "job" {
		t.Fatalf("the question names the cache and the job: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runnercache.DirsFor(folder).Tools, "node")); err != nil {
		t.Fatal("nothing may be deleted before the yes")
	}
	got, err := cs.BeginWipe(ctx, actor, tool.ID, true)
	if err != nil || got.State != caches.StateWiping {
		t.Fatalf("begin: %+v %v", got, err)
	}
	waitFor(t, "the wipe names the job it waits for", func() bool {
		l, _ := cs.List(ctx, "", caches.KindTool)
		return len(l) == 1 && len(l[0].WaitingFor) == 1
	})
	// Three more polls of the waiting wipe pass while the job runs.
	for i := 0; i < 3; i++ {
		select {
		case <-polls:
		case <-time.After(15 * time.Second):
			t.Fatal("the waiting wipe did not poll again")
		}
	}
	if _, err := os.Stat(filepath.Join(runnercache.DirsFor(folder).Tools, "node")); err != nil {
		t.Fatal("a running job's cache was cut")
	}
	if d := cfg.Gate.Start("j2", jr, "persistent"); d.Go {
		t.Fatal("a new job of the wiping runner started")
	}
	if _, err := cs.BeginWipe(ctx, actor, tool.ID, true); err == nil {
		t.Fatal("a second wipe of the same cache must be refused while it waits")
	}

	cfg.Gate.Finish("j1")
	waitFor(t, "wiped", func() bool {
		l, _ := cs.List(ctx, "", caches.KindTool)
		return len(l) == 1 && l[0].State == caches.StateReady && l[0].Size == 0 && !l[0].LastWipedAt.IsZero()
	})
	if es, _ := runnercache.Scan(runnercache.OSFS{}, runnercache.DirsFor(folder).Tools); len(es) != 0 {
		t.Fatalf("the folder is not empty: %v", es)
	}
	if _, err := os.Stat(runnercache.DirsFor(folder).Tools); err != nil {
		t.Fatal("the folder itself stays so the runner's environment still points at it")
	}
	if es, _ := runnercache.Scan(runnercache.OSFS{}, runnercache.DirsFor(folder).Rust); len(es) != 1 {
		t.Fatal("the Rust cache must stay")
	}
	waitFor(t, "audit", func() bool { return len(rec.got()) == 2 })
	if e := rec.got(); e[0] != "luis cache.wipe.start" || e[1] != "luis cache.wipe.done" {
		t.Fatalf("audit: %v", e)
	}
	if d := cfg.Gate.Start("j3", jr, "persistent"); !d.Go {
		t.Fatalf("after the wipe the runner takes jobs again: %v", d.Reasons)
	}
}
