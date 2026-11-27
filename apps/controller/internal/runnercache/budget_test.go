package runnercache_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

func TestBudgetEvictsOldestAcrossRunnersAndSkipsBusy(t *testing.T) {
	f := newMem()
	f.add("r1/cache/tools/node/a", 300, 5*time.Hour) // oldest overall
	f.add("r2/cache/rust-target/k/a", 300, 4*time.Hour)
	f.add("r3/cache/rust-target/k/a", 300, 6*time.Hour) // oldest of all, but r3 has a job running
	f.add("r1/cache/rust-target/k/a", 300, time.Hour)
	runners := []runnercache.BudgetRunner{{Name: "r1", Folder: "r1"}, {Name: "r2", Folder: "r2"}, {Name: "r3", Folder: "r3", Busy: true}}
	res, err := runnercache.EnforceBudget(f, runners, 700)
	if err != nil {
		t.Fatal(err)
	}
	if !f.has("r3/cache/rust-target/k/a") {
		t.Fatal("a cache in use by a running job was touched")
	}
	if f.has("r1/cache/tools/node") || f.has("r2/cache/rust-target/k") || !f.has("r1/cache/rust-target/k/a") {
		t.Fatalf("%+v", res)
	}
	if res.Before != 1200 || res.After != 600 || res.Over || len(res.Removed) != 2 {
		t.Fatalf("%+v", res)
	}
}

func TestBudgetStaysOverWhenOnlyBusyCachesRemain(t *testing.T) {
	f := newMem()
	f.add("r1/cache/rust-target/k/a", 900, time.Hour)
	res, err := runnercache.EnforceBudget(f, []runnercache.BudgetRunner{{Name: "r1", Folder: "r1", Busy: true}}, 100)
	if err != nil || !f.has("r1/cache/rust-target/k/a") || !res.Over || len(res.Lines()) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestBudgetUnderOrOffRemovesNothing(t *testing.T) {
	f := newMem()
	f.add("r1/cache/tools/a/x", 100, time.Hour)
	for _, b := range []int64{0, 100, 1000} {
		res, err := runnercache.EnforceBudget(f, []runnercache.BudgetRunner{{Name: "r1", Folder: "r1"}}, b)
		if err != nil || len(res.Removed) != 0 {
			t.Fatalf("budget %d: %+v %v", b, res, err)
		}
	}
	if n, _ := runnercache.BudgetTotal(f, []runnercache.BudgetRunner{{Folder: "r1"}}); n != 100 {
		t.Fatalf("total %d", n)
	}
}

func TestSizeAlertPastThresholdForFileAndFolder(t *testing.T) {
	f := newMem()
	f.add(rust+"/k/big.rlib", 500, time.Hour)
	f.add(rust+"/k/small", 10, time.Hour)
	f.add(rust+"/other/x", 50, time.Hour)
	got, err := runnercache.FindOversize(f, rust, 400)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	var file, folder bool
	for _, o := range got {
		file = file || (!o.Folder && o.Path == filepath.FromSlash(rust+"/k/big.rlib"))
		folder = folder || (o.Folder && o.Path == filepath.FromSlash(rust+"/k"))
	}
	if !file || !folder {
		t.Fatalf("%+v", got)
	}
	if none, _ := runnercache.FindOversize(f, rust, 0); none != nil {
		t.Fatal("threshold 0 is off")
	}
}
