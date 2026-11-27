package repos

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// SizeFloor is the room a machine needs in its pool size to serve a size name.
type SizeFloor struct {
	CPUs      int
	MemoryGiB float64
}

// MachineSize is what one machine offers: its OS, its labels and its pool size
// (machines R5.58). A machine whose pool size is not set offers its whole size.
type MachineSize struct {
	OS        string // "linux", "windows" or "macos"
	Labels    []string
	CPUs      int
	MemoryGiB float64
}

// SizeCapacity lists the machines with their pool size, for the size variables
// (scheduling R5.67). Nil in Config means no size variable is written.
type SizeCapacity interface {
	MachineSizes(ctx context.Context) ([]MachineSize, error)
}

// DefaultSizeFloors maps each size name to a floor, read from the one size
// class table (runners R5.71, HND12): a label names a class, a class is a
// floor of CPUs and memory.
func DefaultSizeFloors() map[string]SizeFloor {
	out := map[string]SizeFloor{}
	for _, e := range runners.SizeTable() {
		out[string(e.Class)] = SizeFloor{CPUs: e.CPUs, MemoryGiB: float64(e.MemoryGiB)}
	}
	return out
}

// SizeNames lists the names of floors, smallest floor first.
func SizeNames(floors map[string]SizeFloor) []string {
	names := make([]string, 0, len(floors))
	for n := range floors {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := floors[names[i]], floors[names[j]]
		if a.CPUs != b.CPUs {
			return a.CPUs < b.CPUs
		}
		if a.MemoryGiB != b.MemoryGiB {
			return a.MemoryGiB < b.MemoryGiB
		}
		return names[i] < names[j]
	})
	return names
}

// SizeClassForLabel maps a size label (the end of CRSP_RUNS_ON_LINUX_LARGE) to its
// size class. An unknown label is refused and the message lists the known ones.
func SizeClassForLabel(label string) (runners.SizeClass, error) {
	l := strings.ToLower(strings.TrimSpace(label))
	if l == "" {
		return "", auth.Invalid("size label is empty; use one of %s", knownSizes(DefaultSizeFloors()))
	}
	c, err := runners.ParseSize(l)
	if err != nil {
		return "", auth.Invalid("size label %q is not known; use one of %s", label, knownSizes(DefaultSizeFloors()))
	}
	return c, nil
}

func knownSizes(floors map[string]SizeFloor) string { return strings.Join(SizeNames(floors), ", ") }

// CheckSizeFloors refuses a size map with a bad name or a floor below 1.
func CheckSizeFloors(floors map[string]SizeFloor) error {
	for _, n := range SizeNames(floors) {
		if !sizeNameRE.MatchString(n) {
			return auth.Invalid("size name %q is not allowed: use lowercase letters and digits, starting with a letter", n)
		}
		if f := floors[n]; f.CPUs < 1 || f.MemoryGiB <= 0 {
			return auth.Invalid("size %q needs at least 1 CPU and some memory; got %d CPUs and %g GiB", n, f.CPUs, f.MemoryGiB)
		}
	}
	return nil
}

var (
	sizeNameRE = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	sizeVarRE  = regexp.MustCompile(`^CRSP_RUNS_ON_(LINUX|WINDOWS|MACOS)_([A-Z][A-Z0-9]*)$`)
)

// SizeVariableName is the variable of one OS and size, such as CRSP_RUNS_ON_LINUX_LARGE.
func SizeVariableName(os, size string) string {
	return "CRSP_RUNS_ON_" + strings.ToUpper(os) + "_" + strings.ToUpper(size)
}

// ParseSizeVariable splits a size variable into its OS and size name, both
// lowercase. ok is false for any other name, including the plain CRSP_RUNS_ON_<OS>.
func ParseSizeVariable(name string) (os, size string, ok bool) {
	m := sizeVarRE.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), strings.ToLower(m[2]), true
}

func isSizeVar(name string) bool { _, _, ok := ParseSizeVariable(name); return ok }

// sizeFloors is the owner's map, or the default table.
func (s *Service) sizeFloors() map[string]SizeFloor {
	if s.cfg.SizeFloors != nil {
		if f := s.cfg.SizeFloors(); len(f) > 0 {
			return f
		}
	}
	return DefaultSizeFloors()
}

// sizeVariables builds the size variables for the OSes in base: for each size
// the labels of the machines that have the room, never a machine too small. A
// size that no machine can serve is left out (R5.67).
func (s *Service) sizeVariables(ctx context.Context, pol Policy, base map[string]bool, hosted map[string]string, ephemeral bool) (map[string][]string, error) {
	if s.cfg.Sizes == nil {
		return nil, nil
	}
	ms, err := s.cfg.Sizes.MachineSizes(ctx)
	if err != nil {
		return nil, err
	}
	floors := s.sizeFloors()
	out := map[string][]string{}
	for _, v := range variableOrder {
		if !base[v.OS] {
			continue
		}
		for _, name := range SizeNames(floors) {
			f := floors[name]
			var labels []string
			served := false
			for _, m := range ms {
				if strings.ToLower(m.OS) != v.OS || m.CPUs < f.CPUs || m.MemoryGiB < f.MemoryGiB {
					continue
				}
				served = true
				labels = append(labels, m.Labels...)
			}
			if !served {
				continue
			}
			list := withEphemeral(labelList(v.OS, labels, pol.Labels), ephemeral && hosted[v.OS] == "")
			if h := hosted[v.OS]; h != "" {
				list = []string{h}
			}
			out[SizeVariableName(v.OS, name)] = list
		}
	}
	return out, nil
}

// UnservedSizes lists the size variables no machine can serve, for the doctor
// ("CRSP_RUNS_ON_LINUX_XL: no machine has 16 CPUs and 32 GiB in its pool size").
func (s *Service) UnservedSizes(ctx context.Context) ([]string, error) {
	if s.cfg.Sizes == nil {
		return nil, nil
	}
	ms, err := s.cfg.Sizes.MachineSizes(ctx)
	if err != nil {
		return nil, err
	}
	floors := s.sizeFloors()
	oses := map[string]bool{}
	for _, m := range ms {
		oses[strings.ToLower(m.OS)] = true
	}
	var out []string
	for _, v := range variableOrder {
		if !oses[v.OS] {
			continue
		}
		for _, name := range SizeNames(floors) {
			f := floors[name]
			ok := false
			for _, m := range ms {
				if strings.ToLower(m.OS) == v.OS && m.CPUs >= f.CPUs && m.MemoryGiB >= f.MemoryGiB {
					ok = true
				}
			}
			if !ok {
				out = append(out, fmt.Sprintf("%s: no machine has %d CPUs and %g GiB in its pool size", SizeVariableName(v.OS, name), f.CPUs, f.MemoryGiB))
			}
		}
	}
	return out, nil
}
