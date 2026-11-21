package jobsample

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

// Proc is one process of the table.
type Proc struct {
	PID, PPID int
	// CPUTicks is utime + stime in clock ticks; RSSBytes is the resident set.
	CPUTicks int64
	RSSBytes int64
}

// ProcTable lists the processes of the machine.
type ProcTable interface {
	List() ([]Proc, error)
}

// ProcessTreeSampler sums CPU and resident memory over the process tree under Target.RootPID.
type ProcessTreeSampler struct {
	Table ProcTable
	// TicksPerSecond is the kernel clock (100 on Linux).
	TicksPerSecond int64

	mu   sync.Mutex
	prev map[string]cpuMark // usec holds ticks converted to microseconds
}

// NewProcessTreeSampler returns a sampler over table.
func NewProcessTreeSampler(table ProcTable) *ProcessTreeSampler {
	return &ProcessTreeSampler{Table: table, TicksPerSecond: 100, prev: map[string]cpuMark{}}
}

// Source implements Sampler.
func (*ProcessTreeSampler) Source() Source { return ProcessTree }

// Interval implements Sampler: 5 s, see TreeInterval.
func (*ProcessTreeSampler) Interval() time.Duration { return TreeInterval }

// Sample implements Sampler. A process that exits between samples takes its CPU out of the sum, so a drop
// reads as 0 and never as a negative.
func (p *ProcessTreeSampler) Sample(ctx context.Context, t Target, now time.Time) (Sample, string, error) {
	if err := ctx.Err(); err != nil {
		return Sample{}, "", err
	}
	if t.RootPID <= 0 {
		return Sample{At: now}, "no runner process for this job", nil
	}
	procs, err := p.Table.List()
	if err != nil {
		return Sample{At: now}, errNoProc.Error(), nil
	}
	cpuTicks, mem, found := sumTree(procs, t.RootPID)
	if !found {
		return Sample{At: now}, "runner process not found", nil
	}
	usec := cpuTicks * 1_000_000 / p.TicksPerSecond
	key := t.RunnerID + "/" + strconv.Itoa(t.RootPID)
	p.mu.Lock()
	prev, had := p.prev[key]
	p.prev[key] = cpuMark{usec: usec, at: now}
	p.mu.Unlock()
	s := Sample{At: now, MemoryBytes: mem, Known: true}
	if had {
		s.CPUMillis = cpuMillis(usec-prev.usec, now.Sub(prev.at))
	}
	return s, "", nil
}

// Forget drops the state of a job that ended.
func (p *ProcessTreeSampler) Forget(t Target) {
	p.mu.Lock()
	delete(p.prev, t.RunnerID+"/"+strconv.Itoa(t.RootPID))
	p.mu.Unlock()
}

// sumTree adds CPU ticks and memory of root and all its descendants.
func sumTree(procs []Proc, root int) (ticks, mem int64, found bool) {
	kids := make(map[int][]int, len(procs))
	byPID := make(map[int]*Proc, len(procs))
	for i := range procs {
		pr := &procs[i]
		byPID[pr.PID] = pr
		kids[pr.PPID] = append(kids[pr.PPID], pr.PID)
	}
	if byPID[root] == nil {
		return 0, 0, false
	}
	stack := []int{root}
	seen := map[int]bool{root: true}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pr := byPID[pid]
		ticks += pr.CPUTicks
		mem += pr.RSSBytes
		for _, k := range kids[pid] {
			if !seen[k] {
				seen[k] = true
				stack = append(stack, k)
			}
		}
	}
	return ticks, mem, true
}

// ProcFS reads /proc (Linux). On another system List fails and samples are unknown with a reason.
type ProcFS struct {
	Root     string // "/proc"
	PageSize int64
}

// List implements ProcTable.
func (f ProcFS) List() ([]Proc, error) {
	root := f.Root
	if root == "" {
		root = "/proc"
	}
	page := f.PageSize
	if page == 0 {
		page = int64(os.Getpagesize())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]Proc, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(root + "/" + e.Name() + "/stat")
		if err != nil {
			continue // exited meanwhile
		}
		if p, ok := parseStat(raw, pid, page); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// parseStat reads ppid, utime, stime and rss from /proc/<pid>/stat; the command name may hold spaces.
func parseStat(raw []byte, pid int, page int64) (Proc, bool) {
	i := bytes.LastIndexByte(raw, ')')
	if i < 0 {
		return Proc{}, false
	}
	f := bytes.Fields(raw[i+1:])
	// f[0] is state (field 3); ppid field 4, utime 14, stime 15, rss 24.
	if len(f) < 22 {
		return Proc{}, false
	}
	ppid, e1 := strconv.Atoi(string(f[1]))
	ut, e2 := strconv.ParseInt(string(f[11]), 10, 64)
	st, e3 := strconv.ParseInt(string(f[12]), 10, 64)
	rss, e4 := strconv.ParseInt(string(f[21]), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return Proc{}, false
	}
	return Proc{PID: pid, PPID: ppid, CPUTicks: ut + st, RSSBytes: rss * page}, true
}
