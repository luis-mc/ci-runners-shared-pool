package container

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ErrCPUBudget is returned when the machine's CPU budget has too few free CPUs
// for a container (machines R5.54). The job waits; nothing is started.
var ErrCPUBudget = errors.New("the CPU budget of this machine has no free CPUs for this container")

// CPUAllocator hands out pinned CPU sets from the machine's CPU budget. Two
// holders never get the same CPU. It is safe for concurrent use.
type CPUAllocator struct {
	mu     sync.Mutex
	budget []int          // CPU ids the pool may use, ascending
	owner  map[int]string // cpu id -> holder
}

// NewCPUAllocator makes an allocator over CPUs 0..n-1. n < 1 gives an
// allocator with no budget, which refuses every request.
func NewCPUAllocator(n int) *CPUAllocator {
	a := &CPUAllocator{owner: map[int]string{}}
	for i := 0; i < n; i++ {
		a.budget = append(a.budget, i)
	}
	return a
}

// Allocate gives holder n CPUs and returns them as a docker cpuset string
// (`0-3`, `0,2`). A holder that already has a set gets the same set back, and
// asking again for a different size is an error. When fewer than n CPUs are
// free it returns ErrCPUBudget and holds nothing.
func (a *CPUAllocator) Allocate(holder string, n int) (string, error) {
	if holder == "" || n < 1 {
		return "", errors.New("a cpuset needs a holder and at least 1 CPU")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if have := a.heldLocked(holder); len(have) > 0 {
		if len(have) != n {
			return "", fmt.Errorf("%s already holds %d CPUs, not %d", holder, len(have), n)
		}
		return FormatCPUSet(have), nil
	}
	var free []int
	for _, id := range a.budget {
		if _, taken := a.owner[id]; !taken {
			free = append(free, id)
		}
	}
	if len(free) < n {
		return "", fmt.Errorf("%w: %d wanted, %d free of %d", ErrCPUBudget, n, len(free), len(a.budget))
	}
	got := free[:n]
	for _, id := range got {
		a.owner[id] = holder
	}
	return FormatCPUSet(got), nil
}

// Adopt records that holder already holds the CPUs of a docker cpuset string,
// read back from a live container after a crash. A CPU outside the budget or
// held by someone else is an error and nothing is recorded.
func (a *CPUAllocator) Adopt(holder, set string) error {
	ids, err := ParseCPUSet(set)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	inBudget := map[int]bool{}
	for _, id := range a.budget {
		inBudget[id] = true
	}
	for _, id := range ids {
		if !inBudget[id] {
			return fmt.Errorf("CPU %d of %s is outside the CPU budget", id, holder)
		}
		if o, taken := a.owner[id]; taken && o != holder {
			return fmt.Errorf("CPU %d of %s is already held by %s", id, holder, o)
		}
	}
	for _, id := range ids {
		a.owner[id] = holder
	}
	return nil
}

// Release frees every CPU of holder. It is safe to call twice.
func (a *CPUAllocator) Release(holder string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id, o := range a.owner {
		if o == holder {
			delete(a.owner, id)
		}
	}
}

// Held returns the cpuset string holder has, or "".
func (a *CPUAllocator) Held(holder string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return FormatCPUSet(a.heldLocked(holder))
}

// Free is the number of CPUs of the budget nobody holds.
func (a *CPUAllocator) Free() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.budget) - len(a.owner)
}

func (a *CPUAllocator) heldLocked(holder string) []int {
	var ids []int
	for id, o := range a.owner {
		if o == holder {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// FormatCPUSet writes ascending CPU ids as a docker cpuset: runs become
// ranges (`0-3,6`). No ids give "".
func FormatCPUSet(ids []int) string {
	var parts []string
	for i := 0; i < len(ids); {
		j := i
		for j+1 < len(ids) && ids[j+1] == ids[j]+1 {
			j++
		}
		if j > i {
			parts = append(parts, strconv.Itoa(ids[i])+"-"+strconv.Itoa(ids[j]))
		} else {
			parts = append(parts, strconv.Itoa(ids[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// ParseCPUSet reads a docker cpuset string (`0-3,6`) into CPU ids. An empty
// string gives none.
func ParseCPUSet(s string) ([]int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var ids []int
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 0 {
			return nil, fmt.Errorf("%q is not a cpuset", s)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return nil, fmt.Errorf("%q is not a cpuset", s)
			}
		}
		for id := a; id <= b; id++ {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// CPUsFor is how many whole CPUs a container pinned by cpuset needs for a CPU
// limit: the limit rounded up, at least 1. 0 or less gives 0 (no pinning).
func CPUsFor(cpus float64) int {
	if cpus <= 0 {
		return 0
	}
	return int(math.Ceil(cpus - 1e-9))
}

// ResourceArgs are the `docker run` flags of the caps of a container: the
// memory cap with --memory-swap equal to it (no swap, so the cap is the cap),
// and --cpuset-cpus when the container is pinned (machines R5.54).
func ResourceArgs(memoryMB int, cpuset string) []string {
	var a []string
	if memoryMB > 0 {
		m := strconv.Itoa(memoryMB) + "m"
		a = append(a, "--memory", m, "--memory-swap", m)
	}
	if cpuset != "" {
		a = append(a, "--cpuset-cpus", cpuset)
	}
	return a
}

// pin takes the cpuset of a pool container from the CPU budget, when the
// backend has one and the container has a CPU limit (machines R5.54). The
// holder is the container name. A full budget is ErrCPUBudget.
func (b *Backend) pin(p *PoolSpec) error {
	if b.cpus == nil {
		return nil
	}
	n := CPUsFor(p.Limits.CPUs)
	if n == 0 {
		return nil
	}
	set, err := b.cpus.Allocate(PoolName(p.Owner, p.Repo), n)
	if err != nil {
		return err
	}
	p.CPUSet = set
	return nil
}

// unpin gives the cpuset of a pool container back.
func (b *Backend) unpin(pool string) {
	if b.cpus != nil {
		b.cpus.Release(pool)
	}
}

// releaseIfGone frees the cpuset of a pool container the engine no longer
// has. A container that is there keeps its CPUs, also while stopped, because
// `docker start` gives it the same set. An engine that does not answer frees
// nothing.
func (b *Backend) releaseIfGone(ctx context.Context, pool string) {
	if b.cpus == nil || b.cpus.Held(pool) == "" {
		return
	}
	exists, _, _, err := b.poolState(ctx, pool)
	if err == nil && !exists {
		b.cpus.Release(pool)
	}
}

// RecoverCPUSets rebuilds the allocations after the agent restarted or
// crashed: it reads the cpuset of every pool container the engine has and
// adopts it, so a new container never gets a CPU a live one holds. A container
// whose set cannot be adopted is returned in the error text and keeps running.
func (b *Backend) RecoverCPUSets(ctx context.Context) error {
	if b.cpus == nil {
		return nil
	}
	res, err := b.run(ctx, "ps", "-a", "--filter", "label="+LabelPool, "--format", `{{.Names}}`)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("list pools: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	var bad []string
	for _, name := range strings.Fields(res.Stdout) {
		r, err := b.run(ctx, "inspect", "--format", `{{.HostConfig.CpusetCpus}}`, name)
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			bad = append(bad, name+": "+b.redact(strings.TrimSpace(r.Stderr)))
			continue
		}
		if err := b.cpus.Adopt(name, strings.TrimSpace(r.Stdout)); err != nil {
			bad = append(bad, err.Error())
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("cpusets not recovered: %s", strings.Join(bad, "; "))
	}
	return nil
}

// Holders are the names that hold CPUs now: the pool containers a job may run in.
func (a *CPUAllocator) Holders() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, h := range a.owner {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}
