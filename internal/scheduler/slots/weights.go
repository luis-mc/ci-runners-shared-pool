package slots

// Weight is what one job takes of a machine, in units (scheduling R5.69).
// One unit is the share of one job slot.
type Weight struct {
	CPU    int
	Memory int
}

// DefaultWeight is the weight of a job that matches no class: one slot's share.
// A machine whose jobs all carry it admits by count, as before R5.69.
var DefaultWeight = Weight{CPU: 1, Memory: 1}

// JobClass is one declared class of job and its weight. Match lists the job
// keys or shards that belong to the class, as exact names or globs.
type JobClass struct {
	Name   string
	Match  []string
	Weight Weight
}

// DefaultClasses is the one table of job classes and their weights (R5.69).
// The first class that matches wins.
func DefaultClasses() []JobClass {
	return []JobClass{
		{Name: "lint", Match: []string{"lint", "lint-*"}, Weight: Weight{CPU: 1, Memory: 1}},
		{Name: "unit", Match: []string{"unit", "unit-*"}, Weight: Weight{CPU: 1, Memory: 1}},
		{Name: "build", Match: []string{"build", "build-*"}, Weight: Weight{CPU: 1, Memory: 1}},
		{Name: "e2e", Match: []string{"e2e", "e2e-*"}, Weight: Weight{CPU: 2, Memory: 2}},
		{Name: "vm", Match: []string{"vm", "vm-*"}, Weight: Weight{CPU: 2, Memory: 4}},
	}
}

// JobSize is the size of a job from the scan, a measuring or an override. Source
// is "override", "measured" or "scan". Pattern is a job name or a glob.
type JobSize struct {
	Repository string
	Pattern    string
	Weight     Weight
	Class      string
	Source     string
}

// SizeOf finds the first size entry that matches the job.
func (s Settings) SizeOf(j JobNames) (JobSize, bool) {
	for _, e := range s.Sizes {
		if e.Repository != "" && e.Repository != j.Repository {
			continue
		}
		for _, n := range j.names() {
			if matchGlob(e.Pattern, n) {
				return e, true
			}
		}
	}
	return JobSize{}, false
}

// Capacity is the machine's budget in units: one unit of each kind per job slot.
func (s Settings) Capacity() Weight {
	return Weight{CPU: s.JobSlots, Memory: s.JobSlots}
}

// ClassOf names the class of a job. ok is false when no class matches.
func (s Settings) ClassOf(j JobNames) (name string, ok bool) {
	for _, c := range s.Classes {
		for _, pat := range c.Match {
			for _, n := range j.names() {
				if matchGlob(pat, n) {
					return c.Name, true
				}
			}
		}
	}
	return "", false
}

// WeightOf is the weight of a job: its size from the scan, a measuring or an
// override when there is one (R5.88), else its name class's, or DefaultWeight
// for an unknown class. A weight below 1 counts as 1 and one above the machine's
// budget is cut to it, so a job can always run alone. class is "default" for
// an unknown class.
func (s Settings) WeightOf(j JobNames) (w Weight, class string) {
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
	cp := s.Capacity()
	w.CPU = clampUnits(w.CPU, cp.CPU)
	w.Memory = clampUnits(w.Memory, cp.Memory)
	return w, class
}

func clampUnits(v, limit int) int {
	if v < 1 {
		v = 1
	}
	if limit >= 1 && v > limit {
		v = limit
	}
	return v
}

// HeldWeight is the sum of the weights of the ci slots in held.
func (s Settings) HeldWeight(held []Slot) Weight {
	var sum Weight
	for _, h := range held {
		if h.Kind != KindCI {
			continue
		}
		w, _ := s.WeightOf(JobNames{Repository: h.Job.Repository, Job: h.Job.Job, Shard: h.Job.Shard})
		sum.CPU += w.CPU
		sum.Memory += w.Memory
	}
	return sum
}
