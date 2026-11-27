package runners

import (
	"testing"
)

// runners R5.70: routing ignores names.
func TestRoutingIgnoresNames(t *testing.T) {
	// Matching a need never looks at the name.
	if !Matches(Record{Name: "box", Route: Route{Queue: queueOther}}, Need{Queue: queueOther}) ||
		Matches(Record{Name: "crsp-light-x"}, Need{Queue: queueOther}) {
		t.Fatal("Matches read the name")
	}
}

// queueOther is a queue that only these tests know.
const queueOther Queue = "other"

func TestRouteValidate(t *testing.T) {
	if (Route{Queue: "bogus"}).Validate() == nil || (Route{}).Validate() != nil || (Route{Queue: legacyQueueLight}).Validate() == nil {
		t.Fatal("Validate")
	}
}

// runners R5.71: size class matching.
func TestSizeClassTableAndParse(t *testing.T) {
	tab := SizeTable()
	if len(tab) != 4 || tab[0].Class != SizeSmall || tab[3].Class != SizeXL {
		t.Fatalf("%+v", tab)
	}
	for i := 1; i < len(tab); i++ {
		if tab[i].CPUs <= tab[i-1].CPUs || tab[i].MemoryGiB <= tab[i-1].MemoryGiB {
			t.Fatalf("table must grow: %+v", tab)
		}
	}
	if c, err := ParseSize(" Large "); err != nil || c != SizeLarge {
		t.Fatalf("%v %v", c, err)
	}
	if c, err := ParseSize(""); err != nil || c != DefaultSize {
		t.Fatalf("%v %v", c, err)
	}
	if _, err := ParseSize("huge"); err == nil {
		t.Fatal("unknown class accepted")
	}
	if s, ok := SizeLarge.Spec(); !ok || s.CPUs != 8 || s.MemoryGiB != 16 {
		t.Fatalf("%+v", s)
	}
	if _, ok := SizeClass("x").Spec(); ok {
		t.Fatal("unknown spec")
	}
}

func TestSizeClassMatching(t *testing.T) {
	mk := func(name string, q Queue, c SizeClass) Record {
		return Record{ID: name, Name: name, Route: Route{Queue: q}, SizeClass: c}
	}
	cands := []Record{
		mk("xl-1", QueueCI, SizeXL), mk("large-1", QueueCI, SizeLarge), mk("small-1", QueueCI, SizeSmall),
		mk("medium-b", QueueCI, SizeMedium), mk("medium-a", QueueCI, SizeMedium), mk("light-s", queueOther, SizeSmall),
		{ID: "none", Name: "none-set"}, // empty class is medium
	}
	for _, tc := range []struct {
		need Need
		want string
		ok   bool
	}{
		{Need{Size: SizeSmall}, "small-1", true},
		{Need{Size: SizeMedium}, "medium-a", true}, // smallest fit, then lower name
		{Need{}, "medium-a", true},                 // default is medium
		{Need{Size: SizeLarge}, "large-1", true},
		{Need{Size: SizeXL}, "xl-1", true},
		{Need{Queue: queueOther, Size: SizeSmall}, "light-s", true},
		{Need{Queue: queueOther, Size: SizeMedium}, "", false}, // light runner is too small
		{Need{Pool: "other"}, "", false},
	} {
		got, ok := Select(cands, tc.need)
		if ok != tc.ok || got.Name != tc.want {
			t.Errorf("%+v: got %q %v, want %q %v", tc.need, got.Name, ok, tc.want, tc.ok)
		}
	}
	if _, ok := Select(cands[:3:3][2:], Need{Size: SizeMedium}); ok {
		t.Fatal("a small runner took a medium job")
	}
	if SizeSmall.Fits("bogus") || SizeClass("bogus").Fits(SizeSmall) {
		t.Fatal("unknown class fits")
	}
}
