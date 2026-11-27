package runners

import (
	"fmt"
	"sort"
	"strings"
)

// SizeClass is the declared size of a runner's jobs (runners R5.71, handover
// lessons 2 and 8). The numbers are declared, never measured live.
type SizeClass string

// The size classes, smallest first.
const (
	SizeSmall  SizeClass = "small"
	SizeMedium SizeClass = "medium"
	SizeLarge  SizeClass = "large"
	SizeXL     SizeClass = "xl"
)

// DefaultSize is the class of a runner that was given none.
const DefaultSize = SizeMedium

// SizeSpec is what one class stands for.
type SizeSpec struct {
	Class     SizeClass
	CPUs      int
	MemoryGiB int
}

// sizeTable is the one place that says what each class means. Smallest first;
// the order is the order of "fits".
var sizeTable = []SizeSpec{
	{SizeSmall, 2, 4},
	{SizeMedium, 4, 8},
	{SizeLarge, 8, 16},
	{SizeXL, 16, 32},
}

// SizeTable returns a copy of the table, smallest first.
func SizeTable() []SizeSpec { return append([]SizeSpec(nil), sizeTable...) }

// ParseSize reads a class name without case. An empty name is the default.
func ParseSize(s string) (SizeClass, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return DefaultSize, nil
	}
	for _, e := range sizeTable {
		if string(e.Class) == s {
			return e.Class, nil
		}
	}
	return "", fmt.Errorf("size class %q is not one of small, medium, large, xl", s)
}

// Spec gives the CPU and memory of a class; ok is false for an unknown class.
func (c SizeClass) Spec() (SizeSpec, bool) {
	for _, e := range sizeTable {
		if e.Class == c {
			return e, true
		}
	}
	return SizeSpec{}, false
}

// rank is the place of a class in the table, or -1.
func (c SizeClass) rank() int {
	for i, e := range sizeTable {
		if e.Class == c {
			return i
		}
	}
	return -1
}

// Fits is true when a runner of class c can run a job that needs class need:
// the same class or a larger one.
func (c SizeClass) Fits(need SizeClass) bool {
	a, b := c.rank(), need.rank()
	return a >= 0 && b >= 0 && a >= b
}

// SizeOf is the class of a record; an empty class is the default.
func SizeOf(r Record) SizeClass {
	if r.SizeClass == "" {
		return DefaultSize
	}
	return r.SizeClass
}

// Need is what a job asks of a runner.
type Need struct {
	Queue Queue
	Pool  string
	Size  SizeClass // empty means the default class
}

// Matches is true when the runner serves the need: same queue and pool, and a
// class that fits. It reads the route and the size class of the record, never
// the name (runners R5.70, R5.71).
func Matches(r Record, n Need) bool {
	rt := RouteOf(r)
	want := n.Queue
	if want == "" {
		want = QueueCI
	}
	size := n.Size
	if size == "" {
		size = DefaultSize
	}
	return rt.Queue == want && rt.Pool == n.Pool && SizeOf(r).Fits(size)
}

// Select picks the runner for a need among candidates: the smallest class that
// fits, so a large runner is not used for a small job while a small one is
// free. Ties go to the lower name so the answer is stable. ok is false when
// none matches.
func Select(cands []Record, n Need) (Record, bool) {
	var fit []Record
	for _, r := range cands {
		if Matches(r, n) {
			fit = append(fit, r)
		}
	}
	if len(fit) == 0 {
		return Record{}, false
	}
	sort.SliceStable(fit, func(i, j int) bool {
		ri, rj := SizeOf(fit[i]).rank(), SizeOf(fit[j]).rank()
		if ri != rj {
			return ri < rj
		}
		return fit[i].Name < fit[j].Name
	})
	return fit[0], true
}
