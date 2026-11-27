package vmcheck_test

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/vmcheck"
)

func TestWarnsWhenCapIsShort(t *testing.T) {
	if w := vmcheck.MemoryWarning(1024, 2048, true); w != "" {
		t.Fatalf("enough memory warned: %s", w)
	}
	w := vmcheck.MemoryWarning(1024, 2047, true)
	if !strings.Contains(w, "needs 1024 MiB") || !strings.Contains(w, "2047 MiB free") || !strings.Contains(w, "2048 MiB is needed") {
		t.Fatalf("warning %q", w)
	}
	if vmcheck.MemoryWarning(1024, 0, false) != "" {
		t.Fatal("an unknown size must not warn")
	}
}

func TestFreeMemoryParsers(t *testing.T) {
	if mb, ok := vmcheck.ParseMemAvailableMB("MemTotal: 1 kB\nMemAvailable:    4194304 kB\n"); !ok || mb != 4096 {
		t.Fatalf("%d %v", mb, ok)
	}
	for _, bad := range []string{"", "MemAvailable:\n", "MemAvailable: x kB\n"} {
		if _, ok := vmcheck.ParseMemAvailableMB(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if mb, ok := vmcheck.CgroupFreeMB("3221225472\n", "1073741824\n"); !ok || mb != 2048 {
		t.Fatalf("%d %v", mb, ok)
	}
	if mb, ok := vmcheck.CgroupFreeMB("1048576", "2097152"); !ok || mb != 0 {
		t.Fatalf("over the cap: %d %v", mb, ok)
	}
	if _, ok := vmcheck.CgroupFreeMB("max\n", "5"); ok {
		t.Fatal("no limit is not a size")
	}
}
