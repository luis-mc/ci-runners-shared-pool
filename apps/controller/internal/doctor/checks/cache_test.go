package checks

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
)

// cacheFleet is a fleet that also lists the measured caches.
type cacheFleet struct {
	fleet
	usages []doctor.CacheUsage
	err    error
}

func (c *cacheFleet) Caches(context.Context) ([]doctor.CacheUsage, error) { return c.usages, c.err }

// Doctor R5.34: a cache over its cap warns; within the cap is ok; no cap is left out.
func TestCacheOverQuotaWarns(t *testing.T) {
	fl := &cacheFleet{fleet: fleet{machines: []doctor.Machine{healthyMachine()}}, usages: []doctor.CacheUsage{
		{MachineID: "m1", RunnerName: "r-over", Kind: "rust_cache", Size: 50 * gib, Cap: 40 * gib},
		{MachineID: "m1", RunnerName: "r-fine", Kind: "tool_cache", Size: 5 * gib, Cap: 20 * gib},
		{MachineID: "m1", RunnerName: "r-nocap", Kind: "rust_cache", Size: 99 * gib},
		{MachineID: "other", RunnerName: "r-gone", Kind: "rust_cache", Size: 99 * gib, Cap: 1},
	}}
	fs := run(t, doctor.Env{Fleet: fl}, "cache.cap")
	if len(fs) != 2 {
		t.Fatalf("want 2 findings, got %+v", fs)
	}
	got := map[string]doctor.Severity{}
	for _, f := range fs {
		got[strings.SplitN(f.Subject.Name, " ", 2)[0]] = f.Severity
	}
	if got["r-over"] != doctor.Warn || got["r-fine"] != doctor.OK {
		t.Fatalf("%v", got)
	}
}

// Doctor R5.34: free disk under 10% on a machine that holds caches warns.
func TestDiskUnderTenPercentWarns(t *testing.T) {
	mk := func(id string, free int64) doctor.Machine {
		m := healthyMachine()
		m.ID, m.Name, m.DiskFreeBytes, m.DiskTotalBytes = id, id, i64(free), i64(500*gib)
		return m
	}
	fl := &cacheFleet{fleet: fleet{machines: []doctor.Machine{mk("low", 49*gib), mk("edge", 50*gib), mk("nocache", 1*gib)}},
		usages: []doctor.CacheUsage{{MachineID: "low", Size: 10 * gib}, {MachineID: "edge", Size: 10 * gib}}}
	fs := run(t, doctor.Env{Fleet: fl}, "cache.disk")
	got := map[string]doctor.Severity{}
	for _, f := range fs {
		got[f.Subject.Name] = f.Severity
	}
	if len(got) != 2 || got["low"] != doctor.Warn || got["edge"] != doctor.OK {
		t.Fatalf("%v", got)
	}
}

func TestCacheChecksSkipWithoutCacheData(t *testing.T) {
	for _, fl := range []doctor.Fleet{
		&fleet{machines: []doctor.Machine{healthyMachine()}},
		&cacheFleet{fleet: fleet{machines: []doctor.Machine{healthyMachine()}}, err: errors.New("down")},
	} {
		for _, id := range []string{"cache.cap", "cache.disk"} {
			fs := run(t, doctor.Env{Fleet: fl}, id)
			if len(fs) != 1 || fs[0].Severity != doctor.Skip {
				t.Errorf("%s: %+v", id, fs)
			}
		}
	}
}
