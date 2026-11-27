package caches_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/caches"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type fakeMachines struct {
	mu        sync.Mutex
	views     map[string]machines.View
	connected map[string]bool
	sent      []*agentv1.SessionResponse
}

func (f *fakeMachines) List(context.Context) ([]machines.View, error) {
	var out []machines.View
	for _, v := range f.views {
		out = append(out, v)
	}
	return out, nil
}
func (f *fakeMachines) Get(_ context.Context, id string) (machines.View, error) {
	if v, ok := f.views[id]; ok {
		return v, nil
	}
	return machines.View{}, machines.ErrNotFound
}
func (f *fakeMachines) Send(_ string, m *agentv1.SessionResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}
func (f *fakeMachines) Connected(id string) bool { return f.connected[id] }

type rec struct{ rows []string }

func (r *rec) Record(_ context.Context, a audit.Actor, action string, t audit.Target, d map[string]any) error {
	r.rows = append(r.rows, a.Login+"|"+action+"|"+t.Type+"|"+t.ID)
	if action == "cache.wipe.done" {
		r.rows = append(r.rows, "freed="+itoa(d["freed_bytes"].(int64)))
	}
	return nil
}
func (r *rec) RecordRefused(context.Context, audit.Actor, string, audit.Target, map[string]any) error {
	return nil
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func setup() (*caches.Service, *fakeMachines, *rec) {
	fm := &fakeMachines{
		views: map[string]machines.View{"m1": {
			Machine: machines.Machine{ID: "m1", Name: "build-1"}, Settings: machines.Settings{DrainTimeoutMinutes: 30},
			RunningJobs: []machines.JobRef{{Repository: "o/r", RunID: 9, Job: "test", RunnerName: "r1"}, {Repository: "o/x", RunID: 1, Job: "other", RunnerName: "r2"}},
		}},
		connected: map[string]bool{"m1": true},
	}
	r := &rec{}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	s := caches.New(caches.Options{Machines: fm, Audit: r, Now: func() time.Time { return now }})
	s.HandleReport("m1", &agentv1.CacheReport{Caches: []*agentv1.CacheStatus{
		{RunnerName: "r2", Kind: "rust_cache", SizeBytes: 5, EntryCount: 1},
		{RunnerName: "r1", Kind: "tool_cache", SizeBytes: 3 << 30, CapBytes: 20 << 30, EntryCount: 4},
		{RunnerName: "r1", Kind: "rust_cache", SizeBytes: 1 << 20},
		{RunnerName: "r1", Kind: "docker_cache", SizeBytes: 1}, // unknown kind: left out
	}})
	return s, fm, r
}

func TestListIsSortedFiltersAndHidesRemovedMachines(t *testing.T) {
	s, fm, _ := setup()
	l, err := s.List(context.Background(), "", "")
	if err != nil || len(l) != 3 {
		t.Fatalf("%v %v", l, err)
	}
	if l[0].RunnerName != "r1" || l[0].Kind != "rust_cache" || l[1].Kind != "tool_cache" || l[2].RunnerName != "r2" {
		t.Fatalf("order: %+v", l)
	}
	if l[1].Cap != 20<<30 || l[1].Entries != 4 || l[1].Size != 3<<30 {
		t.Fatalf("size against cap: %+v", l[1])
	}
	if l, _ = s.List(context.Background(), "", caches.KindTool); len(l) != 1 {
		t.Fatalf("kind filter: %+v", l)
	}
	if l, _ = s.List(context.Background(), "nope", ""); len(l) != 0 {
		t.Fatalf("machine filter: %+v", l)
	}
	delete(fm.views, "m1")
	if l, _ = s.List(context.Background(), "", ""); len(l) != 0 {
		t.Fatal("a removed machine's caches must go")
	}
}

func toolID(t *testing.T, s *caches.Service) string {
	t.Helper()
	l, _ := s.List(context.Background(), "", caches.KindTool)
	return l[0].ID
}

func TestWipeNeedsAYesThatNamesTheCacheItsSizeAndTheJobs(t *testing.T) {
	s, fm, r := setup()
	_, err := s.BeginWipe(context.Background(), audit.Actor{Login: "luis"}, toolID(t, s), false)
	var nc *caches.NeedsConfirmation
	if !errors.As(err, &nc) || len(nc.Items) != 2 {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(nc.Items[0].Text, "tool cache of runner r1 on build-1, 3.0 GiB") {
		t.Fatalf("the card names the cache and its size: %q", nc.Items[0].Text)
	}
	if nc.Items[1].Kind != "job" || !strings.Contains(nc.Items[1].Text, "o/r, run 9, job test") || strings.Contains(nc.Items[1].Text, "o/x") {
		t.Fatalf("only the jobs of this runner: %q", nc.Items[1].Text)
	}
	if len(fm.sent) != 0 || len(r.rows) != 0 {
		t.Fatal("nothing happens before the yes")
	}
}

func TestWipeLifecycleWaitingWipingDoneAndAudit(t *testing.T) {
	s, fm, r := setup()
	ctx := context.Background()
	id := toolID(t, s)
	c, err := s.BeginWipe(ctx, audit.Actor{Login: "luis", Source: audit.SourceWeb}, id, true)
	if err != nil || c.State != caches.StateWiping || len(c.WaitingFor) != 1 {
		t.Fatalf("%+v %v", c, err)
	}
	w := fm.sent[0].GetWipeCache()
	if w == nil || w.GetRunnerName() != "r1" || w.GetKind() != "tool_cache" || w.GetDrainTimeoutSeconds() != 1800 {
		t.Fatalf("what the agent is told: %v", fm.sent[0])
	}
	if _, err := s.BeginWipe(ctx, audit.Actor{Login: "luis"}, id, true); err == nil {
		t.Fatal("already wiping must be refused")
	}
	// The agent keeps waiting; a fresh measure does not end the wipe.
	s.OnMessage(ctx, "m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_CacheWipeStatus{CacheWipeStatus: &agentv1.CacheWipeStatus{
		CommandId: w.GetCommandId(), State: "waiting", WaitingFor: []*agentv1.JobRef{{Repository: "o/r", RunId: 9, Job: "test"}}}}}, nil)
	s.HandleReport("m1", &agentv1.CacheReport{Caches: []*agentv1.CacheStatus{{RunnerName: "r1", Kind: "tool_cache", SizeBytes: 3 << 30}}})
	if l, _ := s.List(ctx, "", caches.KindTool); l[0].State != caches.StateWiping || len(l[0].WaitingFor) != 1 {
		t.Fatalf("%+v", l)
	}
	s.HandleWipeStatus(ctx, "m1", &agentv1.CacheWipeStatus{CommandId: w.GetCommandId(), State: "wiping"})
	s.HandleWipeStatus(ctx, "m1", &agentv1.CacheWipeStatus{CommandId: w.GetCommandId(), State: "done", FreedBytes: 3 << 30})
	l, _ := s.List(ctx, "", caches.KindTool)
	if l[0].State != caches.StateReady || l[0].Size != 0 || l[0].LastWipedAt.IsZero() || len(l[0].WaitingFor) != 0 {
		t.Fatalf("after: %+v", l[0])
	}
	if len(r.rows) != 3 || r.rows[0] != "luis|cache.wipe.start|cache|"+id || r.rows[1] != "luis|cache.wipe.done|cache|"+id {
		t.Fatalf("audit: %v", r.rows)
	}
	if last := fm.sent[len(fm.sent)-1]; last.GetMeasureCaches() == nil {
		t.Fatal("the agent is asked to measure again after a wipe")
	}
	// The last wipe time survives the next report.
	s.HandleReport("m1", &agentv1.CacheReport{Caches: []*agentv1.CacheStatus{{RunnerName: "r1", Kind: "tool_cache"}}})
	if l, _ := s.List(ctx, "", caches.KindTool); l[0].LastWipedAt.IsZero() {
		t.Fatal("last wiped time lost")
	}
}

func TestFailedWipeKeepsTheErrorAndIsAudited(t *testing.T) {
	s, fm, r := setup()
	ctx := context.Background()
	if _, err := s.BeginWipe(ctx, audit.Actor{Login: "luis"}, toolID(t, s), true); err != nil {
		t.Fatal(err)
	}
	cmd := fm.sent[0].GetWipeCache().GetCommandId()
	s.HandleWipeStatus(ctx, "m1", &agentv1.CacheWipeStatus{CommandId: cmd, State: "failed", Error: "the folder could not be emptied; permission denied"})
	l, _ := s.List(ctx, "", caches.KindTool)
	if l[0].State != caches.StateReady || l[0].Error == "" || l[0].Size != 3<<30 || !l[0].LastWipedAt.IsZero() {
		t.Fatalf("a failed wipe frees nothing: %+v", l[0])
	}
	if r.rows[len(r.rows)-1] != "luis|cache.wipe.failed|cache|"+l[0].ID {
		t.Fatalf("audit: %v", r.rows)
	}
	// An unknown command id is ignored.
	s.HandleWipeStatus(ctx, "m1", &agentv1.CacheWipeStatus{CommandId: "nope", State: "done"})
}

func TestWipeRefusalsNotFoundAndOffline(t *testing.T) {
	s, fm, _ := setup()
	ctx := context.Background()
	if _, err := s.BeginWipe(ctx, audit.Actor{}, "cch_missing", true); !errors.Is(err, caches.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	fm.connected["m1"] = false
	_, err := s.BeginWipe(ctx, audit.Actor{}, toolID(t, s), true)
	var re *caches.RefusalError
	if !errors.As(err, &re) || !strings.Contains(re.Reason, "build-1 is offline") {
		t.Fatalf("%v", err)
	}
}

func TestOnHelloAsksTheAgentToMeasureAndOtherMessagesAreNotTaken(t *testing.T) {
	s, fm, _ := setup()
	s.OnHello(context.Background(), "m1", nil)
	if len(fm.sent) != 1 || fm.sent[0].GetMeasureCaches() == nil {
		t.Fatal("hello must trigger a measure")
	}
	if s.OnMessage(context.Background(), "m1", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_Heartbeat{}}, nil) {
		t.Fatal("not ours")
	}
	if got := caches.FormatBytes(1536); got != "1.5 KiB" {
		t.Fatal(got)
	}
	if got := caches.FormatBytes(12); got != "12 B" {
		t.Fatal(got)
	}
}

// caches R5.56: each persistent runner's own dependency folder is a line of its
// own with its size, next to the shared caches.
func TestDependencyFolderOfARunnerIsListed(t *testing.T) {
	s, _, _ := setup()
	s.HandleReport("m1", &agentv1.CacheReport{Caches: []*agentv1.CacheStatus{
		{RunnerName: "r1", Kind: "dependency_cache", SizeBytes: 7 << 20},
	}})
	l, err := s.List(context.Background(), "", caches.KindDependency)
	if err != nil || len(l) != 1 || l[0].RunnerName != "r1" || l[0].Size != 7<<20 {
		t.Fatalf("%+v %v", l, err)
	}
	if caches.KindName(caches.KindDependency) != "dependency cache" {
		t.Fatal(caches.KindName(caches.KindDependency))
	}
}
