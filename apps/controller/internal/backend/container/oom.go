package container

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

// ExitMemoryCap is the exit code of a process the kernel killed for the
// memory cap (128 + SIGKILL).
const ExitMemoryCap = 137

// MemoryCapText is what the job log, the console data and the doctor say for a
// job or runner container that exited 137 (runners R5.85, handover lesson 15).
const MemoryCapText = "stopped by the memory cap (exit 137)"

// ExitText says why a container process ended, or "" when the code needs no
// words. Exit 137 is attributed to the memory cap.
func ExitText(code int) string {
	if code == ExitMemoryCap {
		return MemoryCapText
	}
	return ""
}

// OOMFacts is what the engine and the cgroup say about a pool container.
type OOMFacts struct {
	Killed   bool // State.OOMKilled
	ExitCode int
	CapMB    int // the memory cap, 0 when none
	Events   int // oom_kill of the cgroup's memory.events, -1 when not read
}

// IsMemoryCap is true when the container, or a process in it, was killed for
// the memory cap.
func (f OOMFacts) IsMemoryCap() bool {
	return f.Killed || f.Events > 0 || f.ExitCode == ExitMemoryCap
}

// Describe is the one line for a log: the text, the cap and the evidence.
func (f OOMFacts) Describe(pool string) string {
	cap := "no memory cap"
	if f.CapMB > 0 {
		cap = "memory cap " + strconv.Itoa(f.CapMB) + " MiB"
	}
	ev := ""
	if f.Events > 0 {
		ev = ", " + strconv.Itoa(f.Events) + " oom_kill in memory.events"
	}
	return fmt.Sprintf("%s: %s (%s, OOMKilled=%t%s)", pool, MemoryCapText, cap, f.Killed, ev)
}

// ReadOOM reads State.OOMKilled, the exit code and the memory cap of a pool
// container with docker inspect, and oom_kill of its cgroup. It reads only.
func (b *Backend) ReadOOM(ctx context.Context, pool string) (OOMFacts, error) {
	f := OOMFacts{Events: -1}
	res, err := b.run(ctx, "inspect", "--format", "{{.State.OOMKilled}} {{.State.ExitCode}} {{.HostConfig.Memory}}", pool)
	if err != nil {
		return f, err
	}
	if res.ExitCode != 0 {
		if noSuchContainer(res.Stderr) {
			return f, nil
		}
		return f, fmt.Errorf("inspect %s: %s", pool, b.redact(strings.TrimSpace(res.Stderr)))
	}
	fs := strings.Fields(res.Stdout)
	if len(fs) != 3 {
		return f, fmt.Errorf("inspect %s: unexpected answer", pool)
	}
	f.Killed = fs[0] == "true"
	f.ExitCode, _ = strconv.Atoi(fs[1])
	if mem, err := strconv.ParseInt(fs[2], 10, 64); err == nil && mem > 0 {
		f.CapMB = int(mem / (1024 * 1024))
	}
	// The cgroup counts kills of a job process while the container lives.
	if ev, err := b.run(ctx, "exec", pool, "cat", "/sys/fs/cgroup/memory.events"); err == nil && ev.ExitCode == 0 {
		f.Events = ParseOOMKill(ev.Stdout)
	}
	return f, nil
}

// ParseOOMKill reads the oom_kill count of a memory.events file, -1 when the
// line is not there.
func ParseOOMKill(events string) int {
	for _, l := range strings.Split(events, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), " ")
		if ok && k == "oom_kill" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}
	return -1
}

// reportOOM logs the OOM-kill event with the cap value and returns the line
// for the job log, or "" when the memory cap was not the cause.
func (b *Backend) reportOOM(ctx context.Context, pool string) string {
	f, err := b.ReadOOM(ctx, pool)
	if err != nil || !f.IsMemoryCap() {
		return ""
	}
	line := f.Describe(pool)
	b.log("oom", line)
	return line
}

// stoppedBy is the usage field for a runner whose pool was killed for memory.
func (b *Backend) stoppedBy(ctx context.Context, h backend.Handle) string {
	pool, _, ok := splitRef(h.Ref)
	if !ok {
		return ""
	}
	if f, err := b.ReadOOM(ctx, pool); err == nil && f.IsMemoryCap() {
		return MemoryCapText
	}
	return ""
}
