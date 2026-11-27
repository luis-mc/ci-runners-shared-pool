package runners

import (
	"errors"
	"testing"
)

func TestPresetsMatchTheOwnersTables(t *testing.T) {
	std, _ := Preset(PresetStandard)
	if std != TableSizes() || std.Medium.CPUs != 4 || std.XL.MemoryGiB != 32 {
		t.Fatalf("standard = %+v", std)
	}
	cp, _ := Preset(PresetCompact)
	if cp.Small.CPUs != 1 || cp.Small.MemoryGiB != 2 || cp.XL.CPUs != 8 || cp.XL.MemoryGiB != 16 {
		t.Fatalf("compact = %+v", cp)
	}
	mh, _ := Preset(PresetMemoryHeavy)
	if mh.Small.MemoryGiB != 6 || mh.Medium.MemoryGiB != 12 || mh.Large.MemoryGiB != 24 || mh.XL.MemoryGiB != 48 {
		t.Fatalf("memory-heavy = %+v", mh)
	}
	if _, ok := Preset("nope"); ok {
		t.Fatal("unknown preset")
	}
	for _, n := range PresetNames()[:3] {
		p, _ := Preset(n)
		if p.Validate() != nil || p.Preset() != n {
			t.Errorf("%s: %v %s", n, p.Validate(), p.Preset())
		}
	}
	p := std
	p.Small.CPUs = 3
	if p.Preset() != PresetCustom {
		t.Fatal("edited table is custom")
	}
}

func TestValidateRefusesWithTheOwnersWords(t *testing.T) {
	zero := TableSizes()
	zero.Small.MemoryGiB = 0
	if err := zero.Validate(); err == nil || err.Error() != "Enter whole numbers above 0." {
		t.Fatalf("zero: %v", err)
	}
	bad := TableSizes()
	bad.Large.CPUs = 3
	want := "Large has 3 CPUs; it must have more than medium's 4. Sizes grow from small to xl."
	if err := bad.Validate(); err == nil || err.Error() != want {
		t.Fatalf("order: %v", err)
	}
	mem := TableSizes()
	mem.XL.MemoryGiB = 16
	if err := mem.Validate(); err == nil {
		t.Fatal("equal memory must be refused")
	}
	if err := TableSizes().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerClassMustFitThePool(t *testing.T) {
	c := TableSizes()
	pool := PoolRoom{Machine: "GameRoomPC", CPUs: 6, MemoryGiB: 9}
	want := "Large needs 8 CPUs and 16 GiB. GameRoomPC's pool is 6 CPUs and 9 GiB. Pick medium for this runner, or raise the pool on the machine page."
	if err := c.CheckRunnerFits(SizeLarge, pool); err == nil || err.Error() != want {
		t.Fatalf("got %v", err)
	}
	if err := c.CheckRunnerFits(SizeMedium, pool); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckRunnerFits(SizeXL, PoolRoom{}); err != nil {
		t.Fatal("unknown pool is not checked")
	}
}

func TestNoPoolHoldsASizeIsANote(t *testing.T) {
	c := TableSizes()
	got := c.UnheldSizes([]PoolRoom{{CPUs: 8, MemoryGiB: 16}})
	if len(got) != 1 || got[0] != SizeXL {
		t.Fatalf("got %v", got)
	}
	if err := CheckSave(c, c, nil, nil, nil, false); err != nil {
		t.Fatal("a note is not an error")
	}
}

func TestHintsDoNotBlock(t *testing.T) {
	c := ClassSizes{
		Small: SizeSpec{SizeSmall, 1, 1}, Medium: SizeSpec{SizeMedium, 2, 20},
		Large: SizeSpec{SizeLarge, 3, 30}, XL: SizeSpec{SizeXL, 16, 32},
	}
	if c.Validate() != nil {
		t.Fatal("hints never block")
	}
	if len(c.Hints()) < 4 {
		t.Fatalf("hints = %v", c.Hints())
	}
	if h := TableSizes().Hints(); len(h) != 0 {
		t.Fatalf("standard has hints: %v", h)
	}
}

func TestLoweringAClassInUseNeedsConfirm(t *testing.T) {
	prev := TableSizes()
	next := prev
	next.Large.CPUs = 6
	use := map[SizeClass]int{SizeLarge: 2}
	if err := CheckSave(prev, next, nil, nil, use, false); !errors.Is(err, ErrConfirmLowering) {
		t.Fatalf("got %v", err)
	}
	if err := CheckSave(prev, next, nil, nil, use, true); err != nil {
		t.Fatal(err)
	}
	if err := CheckSave(prev, next, nil, nil, map[SizeClass]int{SizeSmall: 1}, false); err != nil {
		t.Fatal("unused class lowers freely")
	}
}

func TestSaveRefusesARunnerThatNoLongerFits(t *testing.T) {
	prev := TableSizes()
	next := prev
	next.Medium.CPUs, next.Medium.MemoryGiB = 7, 14
	err := CheckSave(prev, next, map[string]SizeClass{"r1": SizeMedium},
		map[string]PoolRoom{"r1": {Machine: "pc", CPUs: 6, MemoryGiB: 9}}, nil, true)
	var e *ErrClassTable
	if !errors.As(err, &e) {
		t.Fatalf("got %v", err)
	}
}

func TestSmallestClassThatFits(t *testing.T) {
	c := TableSizes()
	if s, ok := c.Smallest(3, 4); !ok || s.Class != SizeMedium {
		t.Fatalf("got %v %v", s, ok)
	}
	if s, ok := c.Smallest(1, 1); !ok || s.Class != SizeSmall {
		t.Fatal("small")
	}
	if _, ok := c.Smallest(20, 1); ok {
		t.Fatal("none holds 20 CPUs")
	}
}
