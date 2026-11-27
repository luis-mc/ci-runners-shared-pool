// Package load measures how loaded a machine is (machines R5.15 to R5.21,
// R6.6).
//
// Ported from dev-standards, never imported (decision 0005):
//   - claude/plugins/delivery/scripts/load.py (DL-77, DL-78): the parsers, the
//     raw-counter CPU delta without a sleep, the cgroup v2 reads and the
//     worse-of-host-and-container rule;
//   - claude/plugins/delivery/scripts/delivery_runner_pool.py (DL176, DL177):
//     the median of several samples, the WSL VM as the larger of the vmmem
//     working set and its private bytes (at least what containers use), Docker
//     read through the Windows CLI first and then WSL, and slot sizing from the
//     budget (DL79).
//
// Every OS call sits behind the Probe interface, so tests feed fixed output as
// load.py does with its test seams. This package only measures: it never kills,
// suspends or lowers the priority of a process (R5.21, DL-77).
package load

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	meminfoLine = regexp.MustCompile(`(?m)^(\w+):\s+(\d+)`)
	pageSizeRe  = regexp.MustCompile(`page size of (\d+) bytes`)
	topIdleRe   = regexp.MustCompile(`CPU usage:.*?([\d.]+)% idle`)
)

// Counters are raw busy and total CPU counters; CPU use is the delta between
// two readings, never a sleep (R5.15).
type Counters struct {
	Busy  uint64
	Total uint64
}

func round1(x float64) float64 { return math.Round(x*10) / 10 }

// ParseMeminfo returns memory use % from /proc/meminfo text:
// (MemTotal - MemAvailable) / MemTotal. It also returns MemTotal in bytes.
func ParseMeminfo(text string) (pct float64, totalBytes int64, err error) {
	vals := map[string]int64{}
	for _, m := range meminfoLine.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.ParseInt(m[2], 10, 64)
		vals[m[1]] = n
	}
	total := vals["MemTotal"]
	if total == 0 {
		return 0, 0, errors.New("MemTotal missing")
	}
	avail, ok := vals["MemAvailable"]
	if !ok {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	return round1(100 * float64(total-avail) / float64(total)), total * 1024, nil
}

// ParseProcStat returns busy and total jiffies from the first `cpu` line of
// /proc/stat; idle and iowait count as idle.
func ParseProcStat(text string) (Counters, error) {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		var f [8]uint64
		for i, s := range strings.Fields(line)[1:] {
			if i >= len(f) {
				break
			}
			f[i], _ = strconv.ParseUint(s, 10, 64)
		}
		var total uint64
		for _, v := range f {
			total += v
		}
		return Counters{Busy: total - (f[3] + f[4]), Total: total}, nil
	}
	return Counters{}, errors.New("no cpu line")
}

// CPUBetween is CPU use % between two readings. Equal or backwards totals give 0.
func CPUBetween(a, b Counters) float64 {
	if b.Total <= a.Total || b.Busy < a.Busy {
		return 0
	}
	return round1(100 * float64(b.Busy-a.Busy) / float64(b.Total-a.Total))
}

// ParseVMStat returns memory use % on macOS from `vm_stat` text and
// `sysctl hw.memsize`: free, inactive and speculative pages are available.
func ParseVMStat(text string, memsize int64) (float64, error) {
	if memsize <= 0 {
		return 0, errors.New("memsize missing")
	}
	page := int64(4096)
	if m := pageSizeRe.FindStringSubmatch(text); m != nil {
		page, _ = strconv.ParseInt(m[1], 10, 64)
	}
	pages := func(name string) int64 {
		m := regexp.MustCompile(`(?m)^Pages ` + regexp.QuoteMeta(name) + `:\s+(\d+)`).FindStringSubmatch(text)
		if m == nil {
			return 0
		}
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n
	}
	avail := (pages("free") + pages("inactive") + pages("speculative")) * page
	return round1(math.Max(0, 100*float64(memsize-avail)/float64(memsize))), nil
}

// ParseTopCPU returns CPU use % from the last `CPU usage: ... idle` line of
// macOS `top -l 2`.
func ParseTopCPU(text string) (float64, error) {
	all := topIdleRe.FindAllStringSubmatch(text, -1)
	if len(all) == 0 {
		return 0, errors.New("no CPU usage line")
	}
	idle, err := strconv.ParseFloat(all[len(all)-1][1], 64)
	if err != nil {
		return 0, err
	}
	return round1(100 - idle), nil
}

// ParseCPUStat returns usage_usec from cgroup v2 cpu.stat text.
func ParseCPUStat(text string) (int64, error) {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "usage_usec" {
			return strconv.ParseInt(f[1], 10, 64)
		}
	}
	return 0, errors.New("no usage_usec")
}

// ParseCPUMax returns the CPUs a cgroup may use from cpu.max text ("max
// 100000" or "200000 100000"); ok is false when uncapped.
func ParseCPUMax(text string) (cpus float64, ok bool) {
	f := strings.Fields(text)
	if len(f) < 2 || f[0] == "max" {
		return 0, false
	}
	q, err1 := strconv.ParseInt(f[0], 10, 64)
	p, err2 := strconv.ParseInt(f[1], 10, 64)
	if err1 != nil || err2 != nil || p <= 0 {
		return 0, false
	}
	return float64(q) / float64(p), true
}

// ParseLoadAvg returns the 1-minute load average from /proc/loadavg text.
func ParseLoadAvg(text string) (float64, error) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return 0, errors.New("empty loadavg")
	}
	return strconv.ParseFloat(f[0], 64)
}

// Median returns the median of xs (the mean of the middle two for an even count).
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	for i := 1; i < len(s); i++ { // insertion sort: at most a handful of values
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Over lists the budgets a report exceeds: empty when under, such as
// "memory 91% > 85%". Unknown memory is not "over"; the caller holds new jobs
// when MemoryKnown is false (R5.20).
func Over(r Report, budgetMemPct, budgetCPUPct float64) []string {
	var out []string
	if r.MemoryKnown && r.MemoryPct > budgetMemPct {
		out = append(out, fmt.Sprintf("memory %s%% > %s%%", num(r.MemoryPct), num(budgetMemPct)))
	}
	if r.CPUKnown && r.CPUPct > budgetCPUPct {
		out = append(out, fmt.Sprintf("CPU %s%% > %s%%", num(r.CPUPct), num(budgetCPUPct)))
	}
	return out
}

func num(x float64) string {
	if x == math.Trunc(x) {
		return strconv.FormatFloat(x, 'f', 0, 64)
	}
	return strconv.FormatFloat(x, 'f', 1, 64)
}
