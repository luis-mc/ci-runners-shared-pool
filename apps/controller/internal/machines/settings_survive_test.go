package machines_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// userSet changes every setting a user can make on a machine, so a test can
// tell a reported fact from a user value (RES6).
func userSet(t *testing.T, e *env, id string) machines.Settings {
	t.Helper()
	// The pool size is checked against a measured load.
	if err := e.Svc.HandleLoad(context.Background(), id, &agentv1.LoadReport{MemoryPct: 25, MemoryKnown: true, CpuPct: 10, CpuKnown: true}); err != nil {
		t.Fatal(err)
	}
	u := machines.SettingsUpdate{
		BudgetMemoryPct: ptr(70), BudgetCPUPct: ptr(45), JobSlots: ptr(3), DrainTimeoutMinutes: ptr(95),
		CacheCapBytes: ptr(int64(7 << 30)), Mode: ptr(machines.ModeAlways), CPUCapPct: ptr(50), PauseOnBattery: ptr(false),
		PoolCPUs: ptr(2), PoolMemoryGiB: ptr(8.0), DiskFloorGiB: ptr(150),
	}
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, u); err != nil {
		t.Fatalf("user settings: %v", err)
	}
	s, err := e.Repo.GetSettings(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if s.BudgetMemoryPct != 70 || s.DrainTimeoutMinutes != 95 || s.CPUCapPct != 50 || s.PoolCPUs != 2 || s.DiskFloorGiB != 150 {
		t.Fatalf("the update did not land: %+v", s)
	}
	return s
}

func sameSettings(t *testing.T, e *env, id, when string, want machines.Settings) {
	t.Helper()
	got, err := e.Repo.GetSettings(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: a user setting changed\n got  %+v\n want %+v", when, got, want)
	}
}

// RES6, machines R5.22 to R5.26, R5.61, R5.64: what the agent reports about
// itself (facts, load, accepting, caches) never overwrites a value the user set,
// after a reconnect, a sync and an offline round, and the user values come back
// to the agent in HelloAck.
func TestUserSettingsNeverRevertAfterReconnectOrSync(t *testing.T) {
	e, id, key := setup(t)
	ctx := context.Background()
	want := userSet(t, e, id)

	// A different machine came back: more cores and memory, other backends.
	other := testFacts("renamed-box")
	other.Cores, other.Threads, other.MemoryBytes = 16, 32, 128<<30
	other.Backends = []*agentv1.BackendSupport{{Name: "persistent", Present: true, Usable: true}}
	fa, err := e.session(id, key, &agentv1.Hello{Facts: other, AgentVersion: "9.9.9",
		Load: &agentv1.LoadReport{MemoryPct: 99, MemoryKnown: true, CpuPct: 99, CpuKnown: true, SlotsUsed: 9}})
	if err != nil {
		t.Fatal(err)
	}
	sameSettings(t, e, id, "after hello", want)
	ack := fa.Ack.GetSettings()
	if ack.GetBudgetMemoryPct() != 70 || ack.GetDrainTimeoutMinutes() != 95 || ack.GetMode() != machines.ModeAlways ||
		ack.GetCpuCapPct() != 50 || ack.GetPoolCpus() != 2 || ack.GetCacheCapBytes() != 7<<30 || ack.GetJobSlots() != 3 {
		t.Fatalf("HelloAck carries the user values, got %+v", ack)
	}

	// The agent reports a paused/100% state, new facts and a cache report that
	// disagree with the user's values.
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_AcceptingReport{AcceptingReport: &agentv1.AcceptingReport{
		Accepting: false, PauseReason: machines.PauseReasonPaused, CpuCapPct: 100, CpuCapState: machines.CapApplied}}})
	fa.Send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Facts{Facts: other}})
	fa.Send(heartbeat())
	fa.Next(func(m *agentv1.SessionResponse) bool { return m.GetHeartbeat() != nil })
	sameSettings(t, e, id, "after reports", want)
	if err := e.Svc.HandleFacts(ctx, id, "9.9.9", other); err != nil {
		t.Fatal(err)
	}
	sameSettings(t, e, id, "after a facts sync", want)

	// Offline, then back.
	fa.Close()
	e.Clk.Add(2 * time.Minute)
	if err := e.Svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sameSettings(t, e, id, "after going offline", want)
	fa2, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := fa2.Ack.GetSettings().GetBudgetCpuPct(); got != 45 {
		t.Fatalf("HelloAck after reconnect: CPU budget %d", got)
	}
	sameSettings(t, e, id, "after reconnecting", want)
}

// RES6: a controller restart (a new service on the stored records) keeps every
// user value, and a machine that reconnects to it is told the same values.
func TestUserSettingsSurviveAControllerRestart(t *testing.T) {
	e, id, key := setup(t)
	want := userSet(t, e, id)
	fa, err := e.session(id, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	fa.Close()

	restarted := machines.New(machines.Options{Repo: e.Repo, Now: e.Clk.Now})
	v, err := restarted.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.Settings, want) {
		t.Fatalf("after a restart\n got  %+v\n want %+v", v.Settings, want)
	}
	if err := restarted.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.HandleFacts(context.Background(), id, "x", testFacts("box")); err != nil {
		t.Fatal(err)
	}
	sameSettings(t, e, id, "after a restart, tick and sync", want)
}

// RES6: a facts sync never changes the machine's name, state or settings, even
// when it races with a rename from the console (the sync read the record before
// the rename and writes after it).
func TestFactsSyncDoesNotRevertARename(t *testing.T) {
	e, id, _ := setup(t)
	ctx := context.Background()
	g := &gatedRepo{MemRepo: e.Repo, reached: make(chan struct{}), release: make(chan struct{})}
	svc := machines.New(machines.Options{Repo: g, Now: e.Clk.Now})

	g.gate.Store(true)
	done := make(chan error, 1)
	go func() { done <- svc.MarkSeen(ctx, id) }() // reads the record, then waits
	select {
	case <-g.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat never read the machine")
	}
	renamed := make(chan error, 1)
	go func() {
		_, _, err := svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{Name: ptr("renamed")})
		renamed <- err
	}()
	// The rename queues behind the machine's lock, which the waiting sync holds; nothing
	// reports that it has queued, so give it the scheduler (not a clock) before the sync goes on.
	for i := 0; i < 200; i++ {
		runtime.Gosched()
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-renamed; err != nil {
		t.Fatal(err)
	}
	v, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Machine.Name != "renamed" {
		t.Fatalf("the name went back to %q", v.Machine.Name)
	}
}

// RES6, machines R5.30 to R5.32: a change that waits for a drain carries only
// what it changes. A setting the user changes while it waits is not undone when
// the drain finishes.
func TestSettingChangedWhileADrainWaitsIsNotUndone(t *testing.T) {
	e, id, key := setup(t)
	ctx := context.Background()
	fa, err := e.session(id, key, &agentv1.Hello{Holders: &agentv1.HolderReport{Running: []*agentv1.JobRef{oneJob}}})
	if err != nil {
		t.Fatal(err)
	}
	_, ch, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{
		AllowedBackends: ptr([]string{"persistent", "docker"}), Confirm: true})
	if err != nil || ch == nil {
		t.Fatalf("the backend change should wait for the job: %v %v", ch, err)
	}
	fa.Next(isDrain)
	// While it waits, the user changes other settings.
	if _, _, err := e.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{
		BudgetMemoryPct: ptr(60), DrainTimeoutMinutes: ptr(30), CacheCapBytes: ptr(int64(5 << 30)), Mode: ptr(machines.ModePaused)}); err != nil {
		t.Fatal(err)
	}
	fa.Send(drainStatus(ch.ID, true))
	waitFor(t, "the change to finish", func() bool {
		c, err := e.Svc.GetChange(ctx, id, ch.ID)
		return err == nil && c.State == machines.ChangeDone
	})
	got, err := e.Repo.GetSettings(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AllowedBackends, []string{"persistent", "docker"}) {
		t.Fatalf("the waiting change did not land: %v", got.AllowedBackends)
	}
	if got.BudgetMemoryPct != 60 || got.DrainTimeoutMinutes != 30 || got.CacheCapBytes != 5<<30 || got.EffectiveMode() != machines.ModePaused {
		t.Fatalf("a setting the user changed while the drain waited was undone: %+v", got)
	}
}
