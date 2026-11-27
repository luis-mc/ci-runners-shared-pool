package runnercache_test

import (
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

const rust = "r1/cache/rust-target"

func TestIncrementalFirstThenWholeEntry(t *testing.T) {
	f := newMem()
	f.add(rust+"/keyA/debug/incremental/x", 600, time.Minute)
	f.add(rust+"/keyA/debug/deps/lib", 300, time.Minute)
	// The incremental folder alone brings the key under the cap: deps stays.
	res, err := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 500, Key: 500}, "keyA", false)
	if err != nil || len(res.Actions) != 1 || !f.has(rust+"/keyA/debug/deps/lib") || f.has(rust+"/keyA/debug/incremental") {
		t.Fatalf("incremental first: %+v %v", res, err)
	}
	if res.After != 300 || !strings.Contains(strings.Join(res.Lines(), "\n"), "incremental") {
		t.Fatalf("after %d lines %v", res.After, res.Lines())
	}
	// Still over a lower cap: the whole content of the key in use goes, the folder stays.
	res, err = runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 100, Key: 100}, "keyA", false)
	if err != nil || f.has(rust+"/keyA/debug/deps/lib") || res.After != 0 {
		t.Fatalf("whole content: %+v %v", res, err)
	}
}

func TestEntryInUseKept(t *testing.T) {
	f := newMem()
	f.add(rust+"/old/a", 400, 3*time.Hour)
	f.add(rust+"/mid/a", 400, 2*time.Hour)
	f.add(rust+"/now/a", 400, time.Minute)
	res, err := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 1000}, "old", false)
	if err != nil {
		t.Fatal(err)
	}
	// "old" is in use: the oldest other key goes, not the one in use.
	if !f.has(rust+"/old/a") || f.has(rust+"/mid/a") || !f.has(rust+"/now/a") || res.After != 800 {
		t.Fatalf("%+v", res)
	}
}

func TestNewestOtherKeyGoesLastAndInUseTrimmedAlone(t *testing.T) {
	f := newMem()
	f.add(rust+"/stale/a", 300, 3*time.Hour)
	f.add(rust+"/recent/a", 300, 2*time.Hour)
	f.add(rust+"/cur/debug/incremental/i", 500, time.Minute)
	f.add(rust+"/cur/debug/deps/d", 200, time.Minute)
	res, err := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 250}, "cur", false)
	if err != nil {
		t.Fatal(err)
	}
	if f.has(rust+"/stale") || f.has(rust+"/recent") {
		t.Fatalf("other keys stay: %+v", res)
	}
	if f.has(rust+"/cur/debug/incremental") || !f.has(rust+"/cur/debug/deps/d") || res.After != 200 {
		t.Fatalf("in use key: %+v", res)
	}
}

func TestKeyCapTrimsOneKeyWithoutTouchingTheRest(t *testing.T) {
	f := newMem()
	f.add(rust+"/big/debug/incremental/i", 900, time.Hour)
	f.add(rust+"/big/debug/deps/d", 50, time.Hour)
	f.add(rust+"/small/a", 100, 2*time.Hour)
	res, err := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 5000, Key: 200}, "small", false)
	if err != nil || f.has(rust+"/big/debug/incremental") || !f.has(rust+"/big/debug/deps/d") || !f.has(rust+"/small/a") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNewestKeyStandsForTheOneInUseWhenUnnamed(t *testing.T) {
	f := newMem()
	f.add(rust+"/a/x", 600, 2*time.Hour)
	f.add(rust+"/b/x", 600, time.Minute)
	if _, err := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 700}, "", false); err != nil {
		t.Fatal(err)
	}
	if f.has(rust+"/a/x") || !f.has(rust+"/b/x") {
		t.Fatal("the newest key must stay")
	}
}

func TestRustCapBusyRunnerRemovesNothing(t *testing.T) {
	f := newMem()
	f.add(rust+"/a/x", 600, time.Hour)
	res, _ := runnercache.EnforceRust(f, rust, runnercache.RustLimits{Total: 1}, "", true)
	if res.Skipped == "" || !f.has(rust+"/a/x") {
		t.Fatalf("%+v", res)
	}
}

func TestRustKeyCapClampsToTotalAndEnvCarriesIt(t *testing.T) {
	q := runnercache.Quota{RustGiB: 10, RustKeyGiB: 30}.WithDefaults()
	if q.RustKeyBytes() != 10*runnercache.GiB {
		t.Fatalf("clamp %d", q.RustKeyBytes())
	}
	env := map[string]string{
		runnercache.EnvRustDir: "/x/rust", runnercache.EnvRustCap: "40", runnercache.EnvRustKeyCap: "12", runnercache.EnvRustKey: "k1",
	}
	caps := runnercache.CapsFromEnv(func(k string) string { return env[k] })
	if len(caps) != 1 || !caps[0].Rust || caps[0].KeyBytes != 12*runnercache.GiB || caps[0].Keep != "k1" {
		t.Fatalf("%+v", caps)
	}
}
