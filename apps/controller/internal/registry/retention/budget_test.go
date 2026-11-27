package retention

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// R5.41: the defaults are 40 GB standard and 30 GB custom (decimal).
func TestBudgetDefaults(t *testing.T) {
	e := newEnv(t)
	b, err := e.svc.Budgets(context.Background())
	must(t, err)
	if b[0].Kind != registry.KindStandard || b[0].LimitBytes != 40_000_000_000 || b[1].Kind != registry.KindCustom || b[1].LimitBytes != 30_000_000_000 {
		t.Fatalf("%+v", b)
	}
}

// R5.41: the sum may never be above half the free disk; the answer names the most allowed.
func TestPutBudgetsCapsAtHalfTheFreeDisk(t *testing.T) {
	e := newEnv(t)
	e.svc.o.Disk = fakeDisk{free: 100_000_000_000}
	ctx := context.Background()

	r, err := e.svc.PutBudgets(ctx, 30_000_000_000, 20_000_000_000, "luis")
	must(t, err)
	if r.MaxTotalBytes != 50_000_000_000 || e.repo.settings.StandardBudget != 30_000_000_000 || e.repo.settings.CustomBudget != 20_000_000_000 {
		t.Fatalf("exactly half is allowed: %+v %+v", r, e.repo.settings)
	}
	_, err = e.svc.PutBudgets(ctx, 30_000_000_000, 20_000_000_001, "luis")
	var big *BudgetTooBig
	if !errors.As(err, &big) || big.MaxTotalBytes != 50_000_000_000 {
		t.Fatalf("one byte over: got %v", err)
	}
	if e.repo.settings.CustomBudget != 20_000_000_000 {
		t.Fatal("a refused change was saved")
	}
	if _, err := e.svc.PutBudgets(ctx, -1, 5, "luis"); err == nil {
		t.Fatal("negative budget accepted")
	}
	// What the registry holds counts as already taken from the disk.
	e.repo.use[registry.KindStandard] = KindUse{UsedBytes: 10_000_000_000}
	r, err = e.svc.PutBudgets(ctx, 40_000_000_000, 20_000_000_000, "luis")
	must(t, err)
	if r.MaxTotalBytes != 60_000_000_000 {
		t.Fatalf("max %d", r.MaxTotalBytes)
	}
	if !e.aud.has(ActionBudgets) {
		t.Fatal("budget change not audited")
	}
}

func TestBudgetUseAndShared(t *testing.T) {
	e := newEnv(t)
	e.repo.use[registry.KindStandard] = KindUse{UsedBytes: 45_000_000_000, SharedBytes: 2}
	e.repo.use[registry.KindCustom] = KindUse{UsedBytes: 5}
	r, err := e.svc.BudgetReport(context.Background())
	must(t, err)
	if r.Standard.UsedBytes != 45_000_000_000 || r.Standard.SharedBytes != 2 || r.Custom.UsedBytes != 5 {
		t.Fatalf("%+v", r)
	}
}

// R5.42: settings are validated; the folder defaults to <data_dir>/registry; budgets are not touched.
func TestPutSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	st, err := e.svc.Settings(ctx)
	must(t, err)
	if st.Folder != filepath.Join(absPath("/data"), "registry") || st.UnusedDays != 30 || st.KeepOldDays != 7 || st.CleanupHour != 3 {
		t.Fatalf("%+v", st)
	}
	in := st
	in.UnusedDays, in.KeepOldDays, in.CleanupHour, in.StandardBudget = 45, 3, 5, 1
	got, err := e.svc.PutSettings(ctx, in, "luis")
	must(t, err)
	if got.UnusedDays != 45 || got.KeepOldDays != 3 || got.CleanupHour != 5 || got.StandardBudget != registry.DefaultStandardBudget {
		t.Fatalf("%+v", got)
	}
	if e.repo.settings.Folder != "" {
		t.Fatal("the default folder is stored as empty")
	}
	for name, mut := range map[string]func(*registry.Settings){
		"unused":  func(s *registry.Settings) { s.UnusedDays = 0 },
		"hour":    func(s *registry.Settings) { s.CleanupHour = 24 },
		"weekday": func(s *registry.Settings) { s.CleanupWeekday = 7 },
		"folder":  func(s *registry.Settings) { s.Folder = "relative/dir" },
		"variant": func(s *registry.Settings) { s.VariantUnused = 0 },
	} {
		bad := got
		mut(&bad)
		var ve *ValidationError
		if _, err := e.svc.PutSettings(ctx, bad, "luis"); !errors.As(err, &ve) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	// A new folder waits until the blobs move.
	e.svc.o.ActiveFolder = func() string { return filepath.Join(absPath("/data"), "registry") }
	nf := got
	nf.Folder = absPath("/mnt/big/registry")
	got, err = e.svc.PutSettings(ctx, nf, "luis")
	must(t, err)
	if p := e.svc.PendingFolder(got); p != absPath("/mnt/big/registry") {
		t.Fatalf("pending %q", p)
	}
	if !e.aud.has(ActionSettings) {
		t.Fatal("settings change not audited")
	}
}

// absPath makes a slash path a full path on this system (C:\data on Windows).
func absPath(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		panic(err)
	}
	return a
}
