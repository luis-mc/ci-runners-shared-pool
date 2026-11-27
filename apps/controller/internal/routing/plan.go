package routing

import (
	"sort"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsize"
)

// Job is one routed job of a repository: its key, size class and OS.
type Job struct {
	Key   string // the job key of the workflow
	Class string // size class name: small, medium, large, xl
	OS    string // linux, windows or macos
}

// SizeClassName maps a scan class to the size class a runner is declared with
// (runners R5.71): light is small, normal is medium, heavy is large.
func SizeClassName(c jobsize.Class) string {
	switch c {
	case jobsize.Light:
		return "small"
	case jobsize.Heavy:
		return "large"
	default:
		return "medium"
	}
}

// Assignment is the label of one job.
type Assignment struct {
	Job    Job
	Var    string
	Label  string
	Shared bool // a class label shared with other jobs of the class
}

// Plan is the label set of one repository.
type Plan struct {
	Jobs   []Assignment
	Labels []string // distinct labels, sorted
	// ByClass is true when the per-job labels did not fit under the cap and
	// the jobs share one label per size class.
	ByClass bool
	// Dropped lists the jobs left without a label because even the class
	// labels did not fit; they keep the fallback.
	Dropped []string
}

// classOrder ranks size classes, smallest first; unknown names sort last.
var classOrder = map[string]int{"small": 0, "medium": 1, "large": 2, "xl": 3}

// MakePlan builds the labels of a repository's jobs. reserved is how many
// labels a runner already carries (self-hosted, OS, architecture, the pool
// name, read-only ones); labels over cap are never planned (the 101st add
// fails with HTTP 500). names maps job key to variable (VarNames over every
// routed job, so the patch agrees; nil takes the names of jobs). One label per job while reserved + jobs fits; then one
// label per size class; when even that does not fit, the largest classes go
// first and their jobs are dropped.
func MakePlan(repository string, jobs []Job, names map[string]string, reserved, cap int) Plan {
	if cap <= 0 {
		cap = LabelCap
	}
	room := cap - reserved
	if room < 0 {
		room = 0
	}
	js := append([]Job(nil), jobs...)
	sort.Slice(js, func(i, j int) bool { return js[i].Key < js[j].Key })
	if names == nil {
		keys := make([]string, len(js))
		for i, j := range js {
			keys[i] = j.Key
		}
		names = VarNames(keys)
	}

	p := Plan{}
	distinct := map[string]bool{}
	for _, j := range js {
		distinct[JobLabel(repository, j.Key)] = true
	}
	if len(distinct) <= room {
		for _, j := range js {
			p.Jobs = append(p.Jobs, Assignment{Job: j, Var: names[j.Key], Label: JobLabel(repository, j.Key)})
		}
	} else {
		p.ByClass = true
		classes := map[string]bool{}
		for _, j := range js {
			classes[j.Class] = true
		}
		var order []string
		for c := range classes {
			order = append(order, c)
		}
		sort.Slice(order, func(a, b int) bool {
			ra, oka := classOrder[order[a]]
			rb, okb := classOrder[order[b]]
			if !oka {
				ra = 99
			}
			if !okb {
				rb = 99
			}
			if ra != rb {
				return ra < rb
			}
			return order[a] < order[b]
		})
		keep := map[string]bool{}
		for i, c := range order {
			if i < room {
				keep[c] = true
			}
		}
		for _, j := range js {
			if !keep[j.Class] {
				p.Dropped = append(p.Dropped, j.Key)
				continue
			}
			p.Jobs = append(p.Jobs, Assignment{Job: j, Var: names[j.Key], Label: ClassLabel(repository, j.Class), Shared: true})
		}
	}
	seen := map[string]bool{}
	for _, a := range p.Jobs {
		if !seen[a.Label] {
			seen[a.Label] = true
			p.Labels = append(p.Labels, a.Label)
		}
	}
	sort.Strings(p.Labels)
	return p
}

// RunnerInfo is a runner a job label may go on.
type RunnerInfo struct {
	ID         string // the crsp runner id
	GitHubID   int64  // the runner id at GitHub; 0 for an ephemeral runner not made yet
	Persistent bool
	OS         string
	// CPUs and MemoryGiB are the room of its pool size (machines R5.58).
	CPUs      int
	MemoryGiB float64
}

// Floor is the room a size class needs.
type Floor struct {
	CPUs      int
	MemoryGiB float64
}

// Carriers picks the runners that carry the label of a job: those of its OS
// whose pool room reaches the floor of its size class. A class with no floor
// is served by no runner. The result is sorted by runner id.
func Carriers(j Job, runners []RunnerInfo, floors map[string]Floor) []RunnerInfo {
	f, ok := floors[j.Class]
	if !ok {
		return nil
	}
	var out []RunnerInfo
	for _, r := range runners {
		if r.OS == j.OS && r.CPUs >= f.CPUs && r.MemoryGiB >= f.MemoryGiB {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out
}
