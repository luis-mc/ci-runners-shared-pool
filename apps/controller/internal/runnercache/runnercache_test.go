package runnercache_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

var base = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// put makes <dir>/<entry>/blob of size bytes, last used age ago.
func put(t *testing.T, dir, entry string, size int, age time.Duration) {
	t.Helper()
	d := filepath.Join(dir, entry)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(d, "blob")
	if err := os.WriteFile(f, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := base.Add(-age)
	for _, p := range []string{f, d} {
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
}

func names(es []runnercache.Entry) string {
	var n []string
	for _, e := range es {
		n = append(n, filepath.Base(e.Path))
	}
	return strings.Join(n, ",")
}

func TestQuotaDefaultsAndValidate(t *testing.T) {
	q := runnercache.Quota{}.WithDefaults()
	if q.ToolGiB != 20 || q.RustGiB != 40 || q.MinFreePct != 10 {
		t.Fatalf("defaults %+v", q)
	}
	if q.ToolBytes() != 20<<30 || q.RustBytes() != 40<<30 {
		t.Fatalf("bytes %d %d", q.ToolBytes(), q.RustBytes())
	}
	if err := q.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []runnercache.Quota{{ToolGiB: -1, RustGiB: 1, MinFreePct: 10}, {ToolGiB: 1, RustGiB: 1, MinFreePct: 60}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestScanCountsBytesPerEntry(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a", 100, time.Hour)
	put(t, dir, "b", 250, time.Minute)
	es, err := runnercache.Scan(runnercache.OSFS{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if runnercache.Total(es) != 350 || len(es) != 2 {
		t.Fatalf("entries %+v", es)
	}
	none, err := runnercache.Scan(runnercache.OSFS{}, filepath.Join(dir, "missing"))
	if err != nil || len(none) != 0 {
		t.Fatalf("missing dir: %v %v", none, err)
	}
}

func TestEvictionOrderOldestFirstUntilItFits(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "new", 100, time.Minute)
	put(t, dir, "oldest", 100, 3*time.Hour)
	put(t, dir, "old", 100, time.Hour)
	res, err := runnercache.Enforce(runnercache.OSFS{}, dir, 150, false)
	if err != nil {
		t.Fatal(err)
	}
	if names(res.Removed) != "oldest,old" || res.Before != 300 || res.After != 100 {
		t.Fatalf("removed %s, %d -> %d", names(res.Removed), res.Before, res.After)
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Fatal("newest entry was removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "oldest")); !os.IsNotExist(err) {
		t.Fatal("oldest entry is still there")
	}
}

func TestEvictionStopsAsSoonAsItFits(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a", 100, 2*time.Hour)
	put(t, dir, "b", 100, time.Hour)
	res, _ := runnercache.Enforce(runnercache.OSFS{}, dir, 100, false)
	if names(res.Removed) != "a" {
		t.Fatalf("removed %s", names(res.Removed))
	}
}

func TestEvictionTieBreaksByPath(t *testing.T) {
	es := []runnercache.Entry{{Path: "/c", Used: base}, {Path: "/a", Used: base}, {Path: "/b", Used: base}}
	if got := names(runnercache.Order(es)); got != "a,b,c" {
		t.Fatalf("order %s", got)
	}
}

func TestUnderQuotaRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a", 100, time.Hour)
	res, _ := runnercache.Enforce(runnercache.OSFS{}, dir, 100, false)
	if len(res.Removed) != 0 || res.After != 100 {
		t.Fatalf("%+v", res)
	}
}

func TestNeverEvictsWhileARunnerIsBusy(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "a", 500, time.Hour)
	res, err := runnercache.Enforce(runnercache.OSFS{}, dir, 100, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 || res.Skipped != "runner busy" {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err != nil {
		t.Fatal("entry removed during a job")
	}
}

func TestTouchKeepsAReusedEntry(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "reused", 100, 5*time.Hour)
	put(t, dir, "idle", 100, time.Hour)
	if err := runnercache.Touch(filepath.Join(dir, "reused", "blob"), base); err != nil {
		t.Fatal(err)
	}
	res, _ := runnercache.Enforce(runnercache.OSFS{}, dir, 100, false)
	if names(res.Removed) != "idle" {
		t.Fatalf("removed %s", names(res.Removed))
	}
}

func TestSweepAppliesToolQuotaAndRustCapSeparately(t *testing.T) {
	folder := t.TempDir()
	d := runnercache.DirsFor(folder)
	put(t, d.Tools, "t1", 300, 2*time.Hour)
	put(t, d.Tools, "t2", 300, time.Hour)
	put(t, d.Rust, "debug", 900, time.Hour)
	// 1 GiB units are too big for a test: shrink with a fake quota through Enforce.
	tr, _ := runnercache.Enforce(runnercache.OSFS{}, d.Tools, 400, false)
	rr, _ := runnercache.Enforce(runnercache.OSFS{}, d.Rust, 1000, false)
	if names(tr.Removed) != "t1" || len(rr.Removed) != 0 {
		t.Fatalf("tools %s rust %s", names(tr.Removed), names(rr.Removed))
	}
	res, err := runnercache.Sweep(runnercache.OSFS{}, folder, runnercache.Quota{}, false)
	if err != nil || len(res.Tools.Removed)+len(res.Rust.Removed) != 0 {
		t.Fatalf("default quota removed something: %+v %v", res, err)
	}
}

func disk(freePct float64) runnercache.Disk {
	return runnercache.Disk{Total: 1000, Free: int64(freePct * 10)}
}

func TestGuardHoldsUnderTenPercentAndResumesWithMargin(t *testing.T) {
	var events []string
	g := runnercache.NewGuard(0)
	g.OnChange = func(held bool, reason string) {
		if held {
			events = append(events, "hold")
		} else {
			events = append(events, "resume")
		}
	}
	steps := []struct {
		free float64
		held bool
	}{{50, false}, {10, false}, {9.9, true}, {11, true}, {11.9, true}, {12, false}, {30, false}, {5, true}}
	for _, s := range steps {
		if held, _ := g.Check(disk(s.free)); held != s.held {
			t.Fatalf("at %.1f%% free held=%v, want %v", s.free, held, s.held)
		}
	}
	if strings.Join(events, ",") != "hold,resume,hold" {
		t.Fatalf("alerts %v", events)
	}
}

func TestGuardReasonAndUnknownDisk(t *testing.T) {
	g := runnercache.NewGuard(10)
	if _, why := g.Check(disk(4)); !strings.Contains(why, "4.0% free < 10%") {
		t.Fatalf("reason %q", why)
	}
	g2 := runnercache.NewGuard(10)
	if held, _ := g2.Check(runnercache.Disk{}); held {
		t.Fatal("unknown disk held a job")
	}
}

func TestEnvExportsToolsDirAndQuotas(t *testing.T) {
	join := func(p ...string) string { return strings.Join(p, `\`) }
	kv := runnercache.EnvJoin(`C:\runners\r1`, runnercache.Quota{ToolGiB: 5}, join)
	want := map[string]string{
		"CI_TOOLS_DIR":            `C:\runners\r1\cache\tools`,
		"CARGO_TARGET_DIR":        `C:\runners\r1\cache\rust-target`,
		"CI_TOOLS_QUOTA_GIB":      "5",
		"CRSP_RUST_CACHE_CAP_GIB": "40",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("%s = %q, want %q", k, kv[k], v)
		}
	}
	r1, r2 := filepath.Join(string(filepath.Separator)+"srv", "r1"), filepath.Join(string(filepath.Separator)+"srv", "r2")
	a := runnercache.Env(r1, runnercache.Quota{})["CI_TOOLS_DIR"]
	b := runnercache.Env(r2, runnercache.Quota{})["CI_TOOLS_DIR"]
	if a == b || a != filepath.Join(r1, "cache", "tools") {
		t.Fatalf("not per runner: %q %q", a, b)
	}
}

func TestCapsFromEnv(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	caps := runnercache.CapsFromEnv(get(map[string]string{
		"CI_TOOLS_DIR": "/r/cache/tools", "CI_TOOLS_QUOTA_GIB": "20",
		"CARGO_TARGET_DIR": "/r/cache/rust-target", "CRSP_RUST_CACHE_CAP_GIB": "40",
	}))
	if len(caps) != 2 || caps[0].Bytes != 20<<30 || caps[1].Dir != "/r/cache/rust-target" || caps[1].Bytes != 40<<30 {
		t.Fatalf("%+v", caps)
	}
	// a runner the product did not set up, or a bad quota, evicts nothing
	if c := runnercache.CapsFromEnv(get(nil)); len(c) != 0 {
		t.Fatalf("%+v", c)
	}
	if c := runnercache.CapsFromEnv(get(map[string]string{"CI_TOOLS_DIR": "/x", "CI_TOOLS_QUOTA_GIB": "abc"})); len(c) != 0 {
		t.Fatalf("%+v", c)
	}
}
