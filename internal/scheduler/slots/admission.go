package slots

// ClassCeiling is one size class: its name, CPUs and GiB of memory.
type ClassCeiling struct {
	Class     string
	CPUs      int
	MemoryGiB int
}

// Admission is what the gate needs for admission by pool room (scheduling
// R5.90 to R5.94). It is on when the machine's pool size is known.
type Admission struct {
	On                        bool
	PoolCPUs, PoolMemoryGiB   int
	Classes                   []ClassCeiling // small, medium, large, xl
	AfterPasses, AfterMinutes int
}

// Ceiling finds a class by name.
func (a Admission) Ceiling(class string) (ClassCeiling, bool) {
	for _, c := range a.Classes {
		if c.Class == class {
			return c, true
		}
	}
	return ClassCeiling{}, false
}

// Smallest names the smallest class that holds the CPUs and GiB; "" when even
// the largest is too small.
func (a Admission) Smallest(cpus, gib int) string {
	for _, c := range a.Classes {
		if c.CPUs >= cpus && c.MemoryGiB >= gib {
			return c.Class
		}
	}
	return ""
}

// Next names the class after class, or "" for the largest.
func (a Admission) Next(class string) string {
	for i, c := range a.Classes {
		if c.Class == class && i+1 < len(a.Classes) {
			return a.Classes[i+1].Class
		}
	}
	return ""
}

// RequirementOf is what a job needs of the pool: the size from the sources of
// R5.80 (override, measured, scan, name classes, then 1 CPU and 1 GiB), never
// below 1 and not cut to the machine's job-slot budget, because admission
// counts real CPUs and GiB. class is the sizing class name ("default" when none).
func (s Settings) RequirementOf(j JobNames) (w Weight, class string) {
	w, class = DefaultWeight, "default"
	if e, ok := s.SizeOf(j); ok {
		w, class = e.Weight, e.Class
	} else if name, ok := s.ClassOf(j); ok {
		class = name
		for _, c := range s.Classes {
			if c.Name == name {
				w = c.Weight
				break
			}
		}
	}
	if w.CPU < 1 {
		w.CPU = 1
	}
	if w.Memory < 1 {
		w.Memory = 1
	}
	return w, class
}
