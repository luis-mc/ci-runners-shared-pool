package machines_test

import (
	"context"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

func loadReport(mem float64) *agentv1.SessionRequest {
	return &agentv1.SessionRequest{Message: &agentv1.SessionRequest_LoadReport{LoadReport: &agentv1.LoadReport{
		MemoryPct: mem, MemoryKnown: true, CpuPct: 10, CpuKnown: true, SlotsUsed: 1,
	}}}
}

// R6.7: the load is at most 20 seconds old, or it is marked stale with its age.
func TestLoadIsMarkedStaleAfter20Seconds(t *testing.T) {
	e, id, key := setup(t)
	fa, _ := e.session(id, key, nil)
	fa.Send(loadReport(55))
	waitFor(t, "load", func() bool {
		v, _ := e.Svc.Get(context.Background(), id)
		return v.Load != nil && v.Load.MemoryPct == 55
	})
	v, _ := e.Svc.Get(context.Background(), id)
	if v.Load.Stale(e.Clk.Now()) || v.SlotsUsed != 1 {
		t.Fatalf("fresh load marked stale / slots %d", v.SlotsUsed)
	}
	e.Clk.Add(19 * time.Second)
	if v.Load.Stale(e.Clk.Now()) {
		t.Fatal("stale at 19 s")
	}
	e.Clk.Add(2 * time.Second)
	if !v.Load.Stale(e.Clk.Now()) {
		t.Fatal("not stale at 21 s")
	}
}

// R6.8: current load per machine, and load history at 15-second steps for 24
// hours. Older load is dropped.
func TestLoadHistoryAt15SecondStepsFor24Hours(t *testing.T) {
	e, id, key := setup(t)
	ctx := context.Background()
	fa, _ := e.session(id, key, nil)
	start := e.Clk.Now()
	for i := 0; i < 6; i++ { // reports every 5 s: one stored per 15 s
		fa.Send(loadReport(float64(40 + i)))
		waitFor(t, "load received", func() bool { l, ok := e.Svc.CurrentLoad(id); return ok && l.MemoryPct == float64(40+i) })
		e.Clk.Add(5 * time.Second)
	}
	pts, err := e.Svc.LoadHistory(ctx, id, start.Add(-time.Minute), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("stored %d samples for 30 s of 5 s reports, want 2 (15 s steps)", len(pts))
	}
	if pts[0].MemoryPct == nil || *pts[0].MemoryPct != 40 || pts[1].At.Sub(pts[0].At) < 15*time.Second {
		t.Fatalf("points = %+v", pts)
	}
	// 24 hours later the samples are dropped by the tick.
	e.Clk.Add(24*time.Hour + time.Minute)
	if err := e.Svc.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	pts, _ = e.Svc.LoadHistory(ctx, id, start.Add(-time.Hour), time.Time{})
	if len(pts) != 0 {
		t.Fatalf("%d samples older than 24 hours kept", len(pts))
	}
}

// Section 8: with the database down the controller refuses joins and settings
// changes; nothing panics.
func TestDatabaseDownRefusesJoinsAndSettings(t *testing.T) {
	e, id, _ := setup(t)
	e.Repo.Down = true
	if _, err := e.Svc.CreateJoinCode(context.Background(), "owner", 0); err == nil {
		t.Fatal("a join code was made with the database down")
	}
	if _, _, err := e.Svc.UpdateSettings(context.Background(), "owner", id, machinesUpdateSlots(2)); err == nil {
		t.Fatal("settings changed with the database down")
	}
}
