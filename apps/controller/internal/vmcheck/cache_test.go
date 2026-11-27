package vmcheck_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func put(t *testing.T, dir, kind, name string, size int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, kind, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	m := time.Now().Add(-age)
	if err := os.Chtimes(p, m, m); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSharedCacheModeAndLock(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vm-cache")
	if err := vmcheck.EnsureCacheDir(dir); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"images", "bases", "snaps", "use"} {
		st, err := os.Stat(filepath.Join(dir, k))
		if err != nil || !st.IsDir() {
			t.Fatalf("%s: %v", k, err)
		}
		if runtime.GOOS != "windows" && st.Mode()&os.ModeSetgid == 0 {
			t.Errorf("%s has no setgid: %v", k, st.Mode())
		}
	}
	// The lock is exclusive: the second taker waits for the first and sees its work.
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	first, err := vmcheck.Lock(context.Background(), dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		un, err := vmcheck.Lock(context.Background(), dir, "x")
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		order = append(order, "second")
		mu.Unlock()
		un()
	}()
	mu.Lock()
	order = append(order, "first")
	mu.Unlock()
	first()
	wg.Wait()
	if !reflect.DeepEqual(order, []string{"first", "second"}) {
		t.Fatalf("order %v", order)
	}
	// A lock that cannot be had ends with the context.
	held, _ := vmcheck.Lock(context.Background(), dir, "y")
	defer held()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := vmcheck.Lock(ctx, dir, "y"); err == nil {
		t.Fatal("got a held lock")
	}
}

func TestBaseDiskIsMadeOnceThinOnTheImage(t *testing.T) {
	dir := t.TempDir()
	var calls [][]string
	deps := vmcheck.Deps{Run: func(_ context.Context, name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}}
	img := filepath.Join(dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2")
	b1, err := vmcheck.EnsureBase(context.Background(), deps, dir, "t-1", img, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	b2, err := vmcheck.EnsureBase(context.Background(), deps, dir, "t-1", img, strings.Repeat("a", 64))
	if err != nil || b1 != b2 || len(calls) != 1 {
		t.Fatalf("%s %s %v %v", b1, b2, err, calls)
	}
	if want := vmcheck.BaseFile(dir, "t-1", vmcheck.BaseKey(strings.Repeat("a", 64), nil)); b1 != want {
		t.Fatalf("base %s want %s", b1, want)
	}
	a := calls[0]
	if a[0] != "qemu-img" || a[1] != "create" || a[3] != "qcow2" || a[5] != "qcow2" || a[6] != "-b" || !filepath.IsAbs(a[7]) {
		t.Fatalf("call %v", a)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "bases", "*.tmp-*")); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
	// A failed qemu-img leaves no base.
	bad := vmcheck.Deps{Run: func(context.Context, string, ...string) error { return os.ErrInvalid }}
	if _, err := vmcheck.EnsureBase(context.Background(), bad, dir, "t-1", img, strings.Repeat("b", 64)); err == nil {
		t.Fatal("no error")
	}
	if got, _ := filepath.Glob(filepath.Join(dir, "bases", "*")); len(got) != 2 { // the one base and its manifest
		t.Fatalf("bases %v", got)
	}
}

func catOf(t *testing.T, names ...string) vmcheck.Catalogue {
	t.Helper()
	c := vmcheck.Catalogue{}
	for _, n := range names {
		c[n] = vmcheck.Distro{}
	}
	return c
}

func alive(pids ...int) func(int) bool {
	return func(p int) bool {
		for _, x := range pids {
			if x == p {
				return true
			}
		}
		return false
	}
}

func TestPruneKeepsNewestPerDistroAndDropsUnlisted(t *testing.T) {
	dir := t.TempDir()
	h := time.Hour
	newest := put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2", 10, h)
	old := put(t, dir, "images", "t-1-bbbbbbbbbbbbbbbb.qcow2", 10, 48*h)
	other := put(t, dir, "images", "t-2-cccccccccccccccc.qcow2", 10, 99*h) // only one of t-2: kept
	gone := put(t, dir, "images", "old-9-dddddddddddddddd.qcow2", 10, h)   // not in the catalogue
	base := put(t, dir, "bases", "t-1-eeeeeeeeeeeeeeee.qcow2", 5, h)
	oldBase := put(t, dir, "bases", "t-1-ffffffffffffffff.qcow2", 5, 5*h)
	staleTmp := put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2.tmp-1", 1, 3*24*h)
	freshTmp := put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2.tmp-2", 1, h)

	res, err := vmcheck.Prune(context.Background(), dir, catOf(t, "t-1", "t-2"), vmcheck.PruneSpec{Keep: 1}, alive())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{old: true, gone: true, oldBase: true, staleTmp: true}
	if len(res.Removed) != len(want) {
		t.Fatalf("removed %v", res.Removed)
	}
	for _, p := range res.Removed {
		if !want[p] {
			t.Errorf("removed %s", p)
		}
	}
	for _, p := range []string{newest, other, base, freshTmp} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s gone", p)
		}
	}
	for p := range want {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	if res.Freed != 10+10+5+1 {
		t.Errorf("freed %d", res.Freed)
	}
}

func TestPruneNeverTouchesADiskARunningVMUses(t *testing.T) {
	dir := t.TempDir()
	h := time.Hour
	newest := put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2", 10, h)
	inUse := put(t, dir, "images", "t-1-bbbbbbbbbbbbbbbb.qcow2", 10, 48*h)
	unused := put(t, dir, "images", "t-1-cccccccccccccccc.qcow2", 10, 49*h)
	unlisted := put(t, dir, "bases", "gone-1-dddddddddddddddd.qcow2", 5, h)
	// Process 4242 runs and holds inUse and unlisted; process 777 is dead.
	if err := os.MkdirAll(filepath.Join(dir, "use"), 0o750); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(dir, "use", "4242-aa.lease")
	dead := filepath.Join(dir, "use", "777-bb.lease")
	if err := os.WriteFile(live, []byte(inUse+"\n"+unlisted+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dead, []byte(unused+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := vmcheck.Prune(context.Background(), dir, catOf(t, "t-1"), vmcheck.PruneSpec{Keep: 1}, alive(4242))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Removed, []string{unused}) {
		t.Fatalf("removed %v", res.Removed)
	}
	if len(res.InUse) != 2 {
		t.Fatalf("in use %v", res.InUse)
	}
	for _, p := range []string{newest, inUse, unlisted, live} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s gone", p)
		}
	}
	if _, err := os.Stat(dead); err == nil {
		t.Error("a dead process's lease stays")
	}
}

func TestPruneDryRunRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2", 10, time.Hour)
	old := put(t, dir, "images", "t-1-bbbbbbbbbbbbbbbb.qcow2", 10, 9*time.Hour)
	res, err := vmcheck.Prune(context.Background(), dir, catOf(t, "t-1"), vmcheck.PruneSpec{DryRun: true}, alive())
	if err != nil || len(res.Removed) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("dry run removed a file")
	}
}

func TestSizeCapEvictsOldestFirstKeepsNewestImageAndWaitsForRunningVMs(t *testing.T) {
	dir := t.TempDir()
	h := time.Hour
	img1 := put(t, dir, "images", "t-1-aaaaaaaaaaaaaaaa.qcow2", 100, h)
	img2 := put(t, dir, "images", "t-2-bbbbbbbbbbbbbbbb.qcow2", 100, 2*h)
	b1 := put(t, dir, "bases", "t-1-cccccccccccccccc.qcow2", 50, 10*h)
	b2 := put(t, dir, "bases", "t-2-dddddddddddddddd.qcow2", 50, 20*h)
	cat := catOf(t, "t-1", "t-2")
	spec := vmcheck.PruneSpec{Keep: 5, CapBytes: 260}

	// A VM job runs: the cap does nothing.
	if err := os.MkdirAll(filepath.Join(dir, "use"), 0o750); err != nil {
		t.Fatal(err)
	}
	lease := filepath.Join(dir, "use", "9-x.lease")
	if err := os.WriteFile(lease, []byte(img1+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := vmcheck.Prune(context.Background(), dir, cat, spec, alive(9))
	if err != nil || len(res.Removed) != 0 || !res.Busy || res.Over != 40 {
		t.Fatalf("busy: %+v %v", res, err)
	}
	if err := os.Remove(lease); err != nil {
		t.Fatal(err)
	}
	// Idle: 300 bytes, cap 260: the oldest base goes, the rest fits.
	res, err = vmcheck.Prune(context.Background(), dir, cat, spec, alive())
	if err != nil || !reflect.DeepEqual(res.Removed, []string{b2}) || res.Over != 0 {
		t.Fatalf("idle: %+v %v", res, err)
	}
	// A cap nothing can meet removes bases but never the newest image of a distro.
	res, err = vmcheck.Prune(context.Background(), dir, cat, vmcheck.PruneSpec{Keep: 5, CapBytes: 10}, alive())
	if err != nil || !reflect.DeepEqual(res.Removed, []string{b1}) || res.Over != 190 {
		t.Fatalf("tight: %+v %v", res, err)
	}
	for _, p := range []string{img1, img2} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s gone", p)
		}
	}
}

func TestUseBaseTakesALeaseOnImageAndBase(t *testing.T) {
	dir := t.TempDir()
	deps := vmcheck.Deps{Run: func(context.Context, string, ...string) error { return nil }}
	img := vmcheck.ImageFile(dir, "t-1", strings.Repeat("a", 64))
	base, release, err := vmcheck.UseBase(context.Background(), deps, dir, "t-1", img, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	in, busy := vmcheck.InUse(dir, vmcheck.PidAlive)
	if !busy || !in[img] || !in[base] {
		t.Fatalf("%v %v", in, busy)
	}
	release()
	if in, busy = vmcheck.InUse(dir, vmcheck.PidAlive); busy || len(in) != 0 {
		t.Fatalf("after release %v %v", in, busy)
	}
}
