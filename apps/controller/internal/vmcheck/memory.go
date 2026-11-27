package vmcheck

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MemoryHeadroomMB is what the runner needs free on top of the guest's memory
// (runners R5.55).
const MemoryHeadroomMB = 1024

// MemoryWarning is "" when the free memory covers the guest plus the headroom
// or is not known, else the plain warning. It warns and never refuses: the
// load budget of scheduling R5.40 already counts the guest, and a refusal of a
// boot the owner asked for is theirs to choose (caches open question).
func MemoryWarning(guestMB int, freeMB int64, known bool) string {
	need := int64(guestMB) + MemoryHeadroomMB
	if !known || freeMB >= need {
		return ""
	}
	return fmt.Sprintf("warning: the guest needs %d MiB and this runner has %d MiB free; %d MiB is needed (guest plus %d MiB for the host). The boot goes on and may be killed for lack of memory.",
		guestMB, freeMB, need, MemoryHeadroomMB)
}

// ParseMemAvailableMB reads MemAvailable of /proc/meminfo.
func ParseMemAvailableMB(meminfo string) (int64, bool) {
	for _, l := range strings.Split(meminfo, "\n") {
		if rest, ok := strings.CutPrefix(l, "MemAvailable:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				return 0, false
			}
			kb, err := strconv.ParseInt(f[0], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb / 1024, true
		}
	}
	return 0, false
}

// CgroupFreeMB is the free memory of a cgroup v2 from its memory.max and
// memory.current; "max" (no limit) is not known.
func CgroupFreeMB(maxText, currentText string) (int64, bool) {
	mx, err := strconv.ParseInt(strings.TrimSpace(maxText), 10, 64)
	if err != nil {
		return 0, false
	}
	cur, err := strconv.ParseInt(strings.TrimSpace(currentText), 10, 64)
	if err != nil {
		return 0, false
	}
	return max(mx-cur, 0) / (1024 * 1024), true
}

// RealFreeMemMB is the smaller of the runner's cgroup headroom and the
// machine's available memory; not known where neither can be read.
func RealFreeMemMB() (int64, bool) {
	var free int64
	var known bool
	take := func(v int64, ok bool) {
		if ok && (!known || v < free) {
			free, known = v, true
		}
	}
	if mi, err := os.ReadFile("/proc/meminfo"); err == nil {
		take(ParseMemAvailableMB(string(mi)))
	}
	mx, e1 := os.ReadFile("/sys/fs/cgroup/memory.max")
	cur, e2 := os.ReadFile("/sys/fs/cgroup/memory.current")
	if e1 == nil && e2 == nil {
		take(CgroupFreeMB(string(mx), string(cur)))
	}
	return free, known
}
