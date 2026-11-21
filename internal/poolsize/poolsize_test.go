package poolsize

import (
	"strings"
	"testing"
)

func box() Inputs {
	// 32 GiB, 16 CPUs, 6 GiB used outside WSL: memory room 27.2-6 = 21.2 GiB, CPU budget 9.
	return Inputs{TotalBytes: 32 * GiB, TotalCPUs: 16, UsedOutsideBytes: 6 * GiB, BudgetMemPct: 85, BudgetCPUPct: 60}
}

// R5.54
func TestParseWSLConfigReadsOnlyWSL2(t *testing.T) {
	c := ParseWSLConfig("\xef\xbb\xbf[experimental]\nmemory=1GB\n[wsl2]\n# comment\nMemory = 12GB ; note\nprocessors=8\nnestedVirtualization=true\n")
	if !c.MemorySet || c.MemoryBytes != 12*GiB || c.Processors != 8 || !c.NestedVirtualization {
		t.Fatalf("got %+v", c)
	}
	if c := ParseWSLConfig("[wsl2]\nmemory=lots\nprocessors=0\n"); c.MemorySet || c.ProcSet {
		t.Fatalf("a bad value must be ignored: %+v", c)
	}
}

func TestDeclaredUsesDefaultsWhenSilent(t *testing.T) {
	m, c := WSLConfig{}.Declared(32*GiB, 16)
	if m != 16*GiB || c != 16 {
		t.Fatalf("defaults %d %d", m, c)
	}
	m, c = ParseWSLConfig("[wsl2]\nmemory=8GB\nprocessors=4").Declared(32*GiB, 16)
	if m != 8*GiB || c != 4 {
		t.Fatalf("declared %d %d", m, c)
	}
}

func TestDeclaredAboveMachineIsRefused(t *testing.T) {
	c := ParseWSLConfig("[wsl2]\nmemory=64GB\n")
	if r := c.Refusal(32*GiB, 16); !strings.Contains(r, "memory") {
		t.Fatalf("refusal %q", r)
	}
	if r := ParseWSLConfig("[wsl2]\nprocessors=32").Refusal(32*GiB, 16); !strings.Contains(r, "processors") {
		t.Fatalf("refusal %q", r)
	}
	if ParseWSLConfig("[wsl2]\nmemory=8GB").Refusal(32*GiB, 16) != "" {
		t.Fatal("a fitting value is not refused")
	}
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"8GB": 8 * GiB, "512MB": 512 << 20, "6 GiB": 6 * GiB, "4096": 4096, "1.5GB": GiB * 3 / 2} {
		if got, ok := ParseSize(in); !ok || got != want {
			t.Errorf("%s = %d %v", in, got, ok)
		}
	}
	if _, ok := ParseSize("x"); ok {
		t.Error("x parsed")
	}
}

// R5.54, runners R5.53
func TestCPUSplitAndEnv(t *testing.T) {
	if PerRunner(8, 2) != 4 || PerRunner(3, 4) != 1 || PerRunner(8, 0) != 8 {
		t.Fatal("split")
	}
	if Env(4)[EnvMaxParallel] != "4" || Env(0)[EnvMaxParallel] != "1" {
		t.Fatal("env")
	}
	if CPUSet(4) != "0-3" || CPUSet(1) != "0" {
		t.Fatal("cpuset")
	}
}

// R5.58
func TestPoolSizeThatFitsIsAccepted(t *testing.T) {
	r := Check(box(), Request{MemGiB: 16, CPUs: 8})
	if !r.OK || r.Decided != "fits" {
		t.Fatalf("%+v", r)
	}
	if r.TotalGiB != 32 || r.UsedOutsideGiB != 6 || r.CPUBudget != 9 || r.RoomGiB < 21 || r.RoomGiB > 21.3 {
		t.Fatalf("inputs not shown: %+v", r)
	}
}

func TestMemoryBudgetRefusalNamesNearestSize(t *testing.T) {
	r := Check(box(), Request{MemGiB: 24, CPUs: 8})
	if r.OK || r.Short != "memory" || len(r.Nearest) == 0 || !strings.Contains(r.Nearest[0], "21 GiB fits 9 CPUs") {
		t.Fatalf("%+v", r)
	}
}

func TestCPUBudgetRefusal(t *testing.T) {
	r := Check(box(), Request{MemGiB: 20, CPUs: 12})
	if r.OK || r.Short != "cpu" || !strings.Contains(r.Reason, "60%") {
		t.Fatalf("%+v", r)
	}
}

func TestRatioRefusalSaysTheNeed(t *testing.T) {
	r := Check(box(), Request{MemGiB: 10, CPUs: 7})
	if r.OK || r.Short != "ratio" || r.Reason != "10 GiB fits 6 CPUs; 7 CPUs needs 10.5 GiB" {
		t.Fatalf("%+v", r)
	}
}

func TestFloors(t *testing.T) {
	for _, q := range []Request{{MemGiB: 3, CPUs: 2}, {MemGiB: 8, CPUs: 1}} {
		if r := Check(box(), q); r.OK || r.Short != "floor" {
			t.Fatalf("%+v", r)
		}
	}
	if r := Check(box(), Request{MemGiB: 4, CPUs: 2}); !r.OK {
		t.Fatalf("the floor itself fits: %+v", r)
	}
}

func TestGuestsAndOutsideUseShrinkTheRoom(t *testing.T) {
	in := box()
	in.GuestBytes = 8 * GiB
	if r := Check(in, Request{MemGiB: 16, CPUs: 8}); r.OK || r.Short != "memory" {
		t.Fatalf("guests must count: %+v", r)
	}
}

func TestDockerCeiling(t *testing.T) {
	in := box()
	in.DockerCeilingBytes = 8 * GiB
	if r := Check(in, Request{MemGiB: 12, CPUs: 6}); r.OK || r.Short != "docker" {
		t.Fatalf("%+v", r)
	}
}

func TestShrinkNeedsExplicitConfirm(t *testing.T) {
	q := Request{MemGiB: 8, CPUs: 4, CurMemGiB: 16, CurCPUs: 8}
	r := Check(box(), q)
	if r.OK || !r.NeedsShrinkConfirm {
		t.Fatalf("a shrink must wait for a confirm: %+v", r)
	}
	q.ConfirmShrink = true
	if r = Check(box(), q); !r.OK {
		t.Fatalf("confirmed shrink: %+v", r)
	}
	// Lowering only one dimension is a shrink too; growing is not.
	if r = Check(box(), Request{MemGiB: 16, CPUs: 4, CurMemGiB: 16, CurCPUs: 8}); !r.NeedsShrinkConfirm {
		t.Fatal("CPU-only shrink")
	}
	if r = Check(box(), Request{MemGiB: 20, CPUs: 8, CurMemGiB: 16, CurCPUs: 8}); !r.OK {
		t.Fatalf("growth: %+v", r)
	}
}

func TestRefusedSizeIsNotAShrinkQuestion(t *testing.T) {
	r := Check(box(), Request{MemGiB: 3, CPUs: 2, CurMemGiB: 16, CurCPUs: 8})
	if r.NeedsShrinkConfirm || r.OK {
		t.Fatalf("%+v", r)
	}
}

func TestDefaultBudgets(t *testing.T) {
	in := box()
	in.BudgetMemPct, in.BudgetCPUPct = 0, 0
	if r := Check(in, Request{MemGiB: 16, CPUs: 8}); !r.OK || r.CPUBudget != 9 {
		t.Fatalf("%+v", r)
	}
}

// LOOK22: the two recommended sizes.
func TestRecommendHalfAndThreeQuarters(t *testing.T) {
	// The owner's figures: room 10.5 GiB, CPU budget 19.
	if got := Recommend(10.5, 19, 0.5); got != (Recommended{MemGiB: 5, CPUs: 3}) {
		t.Fatalf("half: %+v", got)
	}
	if got := Recommend(10.5, 19, 0.75); got != (Recommended{MemGiB: 7, CPUs: 4}) {
		t.Fatalf("three quarters: %+v", got)
	}
	// A small CPU budget is the cap: 4 CPUs at half is 2.
	if got := Recommend(40, 4, 0.5); got != (Recommended{MemGiB: 20, CPUs: 2}) {
		t.Fatalf("cpu share: %+v", got)
	}
	// A big room and budget: the CPU share wins when memory allows it.
	if got := Recommend(40, 8, 0.75); got != (Recommended{MemGiB: 30, CPUs: 6}) {
		t.Fatalf("big: %+v", got)
	}
}

func TestRecommendKeepsTheFloors(t *testing.T) {
	if got := Recommend(3, 1, 0.5); got != (Recommended{MemGiB: MinGiB, CPUs: MinCPUs}) {
		t.Fatalf("floors: %+v", got)
	}
	if got := Recommend(0, 0, 0.75); got != (Recommended{MemGiB: MinGiB, CPUs: MinCPUs}) {
		t.Fatalf("empty room: %+v", got)
	}
}

func TestCheckFillsRecommendedSizes(t *testing.T) {
	// box(): room 21.2, CPU budget 9.
	r := Check(box(), Request{MemGiB: 8, CPUs: 4})
	if r.Half != (Recommended{MemGiB: 10, CPUs: 4}) || r.ThreeQuarters != (Recommended{MemGiB: 15, CPUs: 6}) {
		t.Fatalf("half %+v three quarters %+v", r.Half, r.ThreeQuarters)
	}
	// Refused sizes carry them too, so the console can offer them.
	r = Check(box(), Request{MemGiB: 64, CPUs: 4})
	if r.OK || r.Half.MemGiB != 10 {
		t.Fatalf("refused: %+v", r)
	}
}

// machines R5.68, OPS61: room = total x budget - host p95 - safety margin.
func TestMeasuredRoomTable(t *testing.T) {
	for _, c := range []struct {
		name      string
		total     float64
		pct       int
		p95, want float64
	}{
		{"idle 32 GiB PC", 32, 85, 3, 32*0.85 - 3 - SafetyMarginGiB},
		{"busy 32 GiB PC", 32, 85, 10.2, 32*0.85 - 10.2 - SafetyMarginGiB},
		{"16 GiB PC", 16, 85, 6, 16*0.85 - 6 - SafetyMarginGiB},
		{"budget 70", 32, 70, 4, 32*0.70 - 4 - SafetyMarginGiB},
		{"zero budget uses the default", 32, 0, 4, 32*0.85 - 4 - SafetyMarginGiB},
		{"p95 eats the budget", 16, 85, 13, 0},
		{"p95 above the machine", 16, 85, 20, 0},
		{"no host use", 64, 85, 0, 64*0.85 - SafetyMarginGiB},
	} {
		if got := MeasuredRoom(c.total, c.pct, c.p95); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("%s: room %.3f, want %.3f", c.name, got, c.want)
		}
	}
}

// The recommended sizes take a share of the measured room when 24 h are
// measured and of today's fixed room before that; the room right now is never
// exceeded (the hard check is unchanged).
func TestRecommendedBasisTable(t *testing.T) {
	for _, c := range []struct {
		name      string
		in        func(Inputs) Inputs
		basis     string
		presetGiB float64
		half      int
	}{
		{"measuring keeps today's fixed share", func(i Inputs) Inputs { i.HostSpanHours = 10; return i }, BasisMeasuring, 21.2, 10},
		{"idle PC measured gets more than fixed", func(i Inputs) Inputs {
			i.HostReady, i.HostP95Bytes = true, 2*GiB
			i.UsedOutsideBytes = 2 * GiB
			return i
		}, BasisMeasured, 24.2, 12},
		{"busy PC measured gets less than fixed", func(i Inputs) Inputs { i.HostReady, i.HostP95Bytes = true, 12*GiB; return i }, BasisMeasured, 14.2, 7},
		{"a spike now caps the preset at the live room", func(i Inputs) Inputs { i.HostReady, i.HostP95Bytes = true, 2*GiB; return i }, BasisMeasured, 21.2, 10},
		{"p95 above the budget leaves the floor", func(i Inputs) Inputs { i.HostReady, i.HostP95Bytes = true, 30*GiB; return i }, BasisMeasured, 0, MinGiB},
	} {
		res := Check(c.in(box()), Request{MemGiB: 8, CPUs: 4})
		if res.Basis != c.basis || res.PresetRoomGiB < c.presetGiB-1e-9 || res.PresetRoomGiB > c.presetGiB+1e-9 || res.Half.MemGiB != c.half {
			t.Errorf("%s: basis %s preset %.2f half %d", c.name, res.Basis, res.PresetRoomGiB, res.Half.MemGiB)
		}
	}
}

// Custom is checked against the measured figure: leaving the PC less than its
// own p95 is a warning, never a block.
func TestLeavesLessThanHostIsAWarningNotABlock(t *testing.T) {
	in := box()
	in.HostReady, in.HostP95Bytes, in.UsedOutsideBytes = true, 12*GiB, 2*GiB
	// 32 GiB machine, 12 GiB host p95: a 21 GiB pool leaves 11 GiB, under the p95, yet the live room (27.2-2) allows it.
	res := Check(in, Request{MemGiB: 21, CPUs: 8})
	if !res.OK || !res.LeavesLessThanHost || res.LeavesGiB != 11 || res.HostP95GiB != 12 {
		t.Fatalf("%+v", res)
	}
	if res := Check(in, Request{MemGiB: 18, CPUs: 8}); !res.OK || res.LeavesLessThanHost {
		t.Fatalf("a pool that leaves 14 GiB must not warn: %+v", res)
	}
	// Still blocked above the live room.
	if res := Check(in, Request{MemGiB: 26, CPUs: 8}); res.OK || res.Short != "memory" {
		t.Fatalf("%+v", res)
	}
	// While measuring there is no warning to give.
	in.HostReady = false
	if res := Check(in, Request{MemGiB: 21, CPUs: 8}); res.LeavesLessThanHost || res.Basis != BasisMeasuring {
		t.Fatalf("%+v", res)
	}
}

func TestWSLCapIsShown(t *testing.T) {
	in := box()
	in.WSLPool, in.DockerCeilingBytes = true, 28*GiB
	in.HostReady, in.HostP95Bytes = true, 8*GiB
	res := Check(in, Request{MemGiB: 8, CPUs: 4})
	if !res.WSLPool || !res.WSLCapSet || res.WSLCapGiB != 28 || !res.CapAbovePC {
		t.Fatalf("%+v", res)
	}
	in.DockerCeilingBytes = 20 * GiB
	if res := Check(in, Request{MemGiB: 8, CPUs: 4}); res.CapAbovePC {
		t.Fatalf("a cap of 20 GiB fits 32 - 8: %+v", res)
	}
	in.DockerCeilingBytes = 0
	if res := Check(in, Request{MemGiB: 8, CPUs: 4}); res.WSLCapSet || res.CapAbovePC {
		t.Fatalf("no cap set: %+v", res)
	}
}
