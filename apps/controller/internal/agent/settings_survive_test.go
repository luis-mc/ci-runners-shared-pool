package agent_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// RES6, machines R5.24, R5.61, R5.64: the controller owns the user's settings.
// An agent that reconnects, or restarts with an older settings file on disk,
// ends up with the controller's values, and what it reports never changes them.
func TestAgentReconnectKeepsTheUsersSettings(t *testing.T) {
	c := startController(t)
	ctx := context.Background()
	code, err := c.Svc.CreateJoinCode(ctx, "owner", 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := fastConfig(t, c, code.Code)
	stop := runAgent(t, cfg)
	waitFor(t, "settings cached", func() bool { _, _, ok := cfg.DataDirCache().Load(); return ok })
	vs, err := c.Svc.List(ctx)
	if err != nil || len(vs) != 1 {
		t.Fatalf("machines %d %v", len(vs), err)
	}
	id := vs[0].Machine.ID

	n, drain := 90, 45
	mode, capPct := machines.ModeAlways, 50
	if _, _, err := c.Svc.UpdateSettings(ctx, "owner", id, machines.SettingsUpdate{
		BudgetMemoryPct: &n, DrainTimeoutMinutes: &drain, Mode: &mode, CPUCapPct: &capPct}); err != nil {
		t.Fatal(err)
	}
	want, err := c.Repo.GetSettings(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the user's values reach the agent", func() bool {
		s, _, ok := cfg.DataDirCache().Load()
		return ok && s.BudgetMemoryPct == 90 && s.CPUCapPct == 50
	})
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	// While the agent is down its file goes stale (an older copy comes back, as
	// from a backup).
	stale := machines.DefaultSettings()
	if err := cfg.DataDirCache().Save(stale, machines.StateActive); err != nil {
		t.Fatal(err)
	}
	stop = runAgent(t, cfg)
	waitFor(t, "the controller's values replace the stale file", func() bool {
		s, _, ok := cfg.DataDirCache().Load()
		return ok && s.BudgetMemoryPct == 90 && s.DrainTimeoutMinutes == 45
	})
	got, err := c.Repo.GetSettings(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the agent changed the user's settings\n got  %+v\n want %+v", got, want)
	}
	_ = stop()
}
