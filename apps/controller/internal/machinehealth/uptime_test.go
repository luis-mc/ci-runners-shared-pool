package machinehealth

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

func near(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 0.01 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestComputeUptimeWindowsAndPeriods(t *testing.T) {
	now := t0
	rows := []store.PresenceRow{
		{At: now.Add(-48 * time.Hour), State: store.PresenceTaking},
		{At: now.Add(-12 * time.Hour), State: store.PresenceOffline, Reason: OfflineHeartbeatLost},
		{At: now.Add(-6 * time.Hour), State: store.PresenceNotTaking, Reason: "on_battery"},
		{At: now.Add(-3 * time.Hour), State: store.PresenceTaking},
	}
	u := ComputeUptime("m", rows, now)
	if !u.Watched || len(u.Windows) != 3 {
		t.Fatalf("%+v", u)
	}
	// 24h: offline 6 h of 24 h, so 75 % online; taking 24-6-3 = 15 h, so 62.5 %.
	near(t, u.Windows[0].UptimePct, 75)
	near(t, u.Windows[0].TakingPct, 62.5)
	// 7d and 30d: watched 48 h only; offline 6 h of 48 h.
	near(t, u.Windows[1].UptimePct, 87.5)
	if u.Windows[2].Covered != 48*time.Hour {
		t.Fatalf("covered %v", u.Windows[2].Covered)
	}
	if len(u.Periods) != 2 || u.Periods[0].Reason != "on_battery" || u.Periods[0].Offline || u.Periods[0].End.IsZero() ||
		u.Periods[1].Reason != OfflineHeartbeatLost || !u.Periods[1].Offline {
		t.Fatalf("periods newest first: %+v", u.Periods)
	}
}

func TestComputeUptimeNothingWatchedAndOngoing(t *testing.T) {
	u := ComputeUptime("m", nil, t0)
	if u.Watched || len(u.Windows) != 3 || u.Windows[0].UptimePct != nil {
		t.Fatalf("a machine never seen has no percent, never 0: %+v", u)
	}
	u = ComputeUptime("m", []store.PresenceRow{{At: t0.Add(-time.Hour), State: store.PresenceOffline, Reason: OfflineClockSkew}}, t0)
	near(t, u.Windows[0].UptimePct, 0)
	if len(u.Periods) != 1 || !u.Periods[0].End.IsZero() {
		t.Fatalf("an open period has no end: %+v", u.Periods)
	}
}

func TestObserveWritesOnlyChanges(t *testing.T) {
	ctx := context.Background()
	s, c, db := newSvc(t)
	step := func(d time.Duration, online bool, reason string) {
		c.t = c.t.Add(d)
		s.Observe(ctx, "m1", online, reason, c.t, c.t)
	}
	step(0, true, "")
	step(time.Minute, true, "")
	step(time.Minute, true, "busy")
	step(time.Minute, true, "busy")
	step(time.Minute, true, "disk_low")
	step(time.Minute, false, OfflineHeartbeatLost)
	step(time.Minute, false, OfflineClockSkew) // same state: the first reason stays
	step(time.Minute, true, "")
	rows, err := db.PresenceSince(ctx, "m1", t0.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"taking:", "not_taking:busy", "not_taking:disk_low", "offline:heartbeat_lost", "taking:"}
	if len(rows) != len(want) {
		t.Fatalf("rows %+v", rows)
	}
	for i, r := range rows {
		if r.State+":"+r.Reason != want[i] {
			t.Fatalf("row %d = %s:%s, want %s", i, r.State, r.Reason, want[i])
		}
	}
}

func TestObserveAfterControllerRestartClosesTheGap(t *testing.T) {
	ctx := context.Background()
	s, c, db := newSvc(t)
	s.Observe(ctx, "m1", true, "", c.t, c.t)
	// A new controller process: nothing in memory. The machine came back 2 hours later; it was last seen 5 minutes in.
	s2 := &Service{Store: db, Now: c.now}
	seen := t0.Add(5 * time.Minute)
	c.t = t0.Add(2 * time.Hour)
	s2.Observe(ctx, "m1", true, "", seen, c.t)
	rows, _ := db.PresenceSince(ctx, "m1", t0.Add(-time.Hour))
	if len(rows) != 3 || rows[1].State != store.PresenceOffline || rows[1].Reason != OfflineControllerStopped || !rows[1].At.Before(t0.Add(6*time.Minute)) || rows[2].State != store.PresenceTaking {
		t.Fatalf("rows %+v", rows)
	}
	u, err := s2.Uptime(ctx, "m1")
	if err != nil || u.Windows[0].UptimePct == nil || *u.Windows[0].UptimePct > 10 {
		t.Fatalf("2 h watched, 5 min online: %+v %v", u.Windows[0], err)
	}
}

func TestPresenceKeepsTheRowInForceAndPrunes(t *testing.T) {
	ctx := context.Background()
	_, _, db := newSvc(t)
	for i, st := range []string{store.PresenceTaking, store.PresenceOffline, store.PresenceTaking} {
		if err := db.AddPresence(ctx, store.PresenceRow{MachineID: "m", At: t0.Add(time.Duration(i*10) * time.Hour), State: st}); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := db.PresenceSince(ctx, "m", t0.Add(15*time.Hour))
	if len(rows) != 2 || rows[0].State != store.PresenceOffline {
		t.Fatalf("the row in force at the start is returned: %+v", rows)
	}
	n, err := db.PrunePresence(ctx, t0.Add(15*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d %v", n, err)
	}
	if last, ok, _ := db.LastPresence(ctx, "m"); !ok || last.At != t0.Add(20*time.Hour) {
		t.Fatalf("%+v", last)
	}
	if err := db.DeleteMachineHealth(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.LastPresence(ctx, "m"); ok {
		t.Fatal("a removed machine keeps no periods")
	}
}
