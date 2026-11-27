package load

import (
	"bufio"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MemoryPct is (total - available) / total in percent (R5.40).
func MemoryPct(total, available float64) (float64, bool) {
	if total <= 0 || available < 0 || available > total {
		return 0, false
	}
	return round1((total - available) / total * 100), true
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

// ParseMeminfo reads /proc/meminfo: MemAvailable, else MemFree + Buffers + Cached.
func ParseMeminfo(text string) (pct float64, ok bool) {
	vals := map[string]float64{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, rest, found := strings.Cut(sc.Text(), ":")
		if !found {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseFloat(f[0], 64); err == nil {
			vals[strings.TrimSpace(k)] = n
		}
	}
	total, has := vals["MemTotal"]
	if !has {
		return 0, false
	}
	avail, has := vals["MemAvailable"]
	if !has {
		free, a := vals["MemFree"]
		if !a {
			return 0, false
		}
		avail = free + vals["Buffers"] + vals["Cached"]
	}
	return MemoryPct(total, avail)
}

var vmStatPageSize = regexp.MustCompile(`page size of (\d+) bytes`)

// ParseVMStat reads macOS vm_stat output with hw.memsize: free, inactive and
// speculative pages count as available (R5.40).
func ParseVMStat(text string, memsize int64) (pct float64, ok bool) {
	page := int64(4096)
	if m := vmStatPageSize.FindStringSubmatch(text); m != nil {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
			page = n
		}
	}
	var avail int64
	found := false
	for _, line := range strings.Split(text, "\n") {
		k, rest, cut := strings.Cut(line, ":")
		if !cut {
			continue
		}
		switch strings.TrimSpace(k) {
		case "Pages free", "Pages inactive", "Pages speculative":
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(rest), "."), 10, 64)
			if err == nil {
				avail += n * page
				found = true
			}
		}
	}
	if !found {
		return 0, false
	}
	return MemoryPct(float64(memsize), float64(avail))
}

// Raw is a pair of raw CPU counters: busy and total.
type Raw struct {
	Busy, Total uint64
}

// ParseProcStat reads the first cpu line of /proc/stat; idle and iowait count as idle (R5.41).
func ParseProcStat(text string) (Raw, bool) {
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || f[0] != "cpu" {
			continue
		}
		var v []uint64
		for _, x := range f[1:] {
			n, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				return Raw{}, false
			}
			v = append(v, n)
		}
		var total uint64
		for _, n := range v {
			total += n
		}
		idle := v[3]
		if len(v) > 4 {
			idle += v[4]
		}
		if idle > total {
			return Raw{}, false
		}
		return Raw{Busy: total - idle, Total: total}, true
	}
	return Raw{}, false
}

// WindowsRaw turns GetSystemTimes values into counters: kernel time includes idle (R5.41).
func WindowsRaw(idle, kernel, user uint64) (Raw, bool) {
	total := kernel + user
	if idle > total {
		return Raw{}, false
	}
	return Raw{Busy: total - idle, Total: total}, true
}

var topCPU = regexp.MustCompile(`CPU usage:.*?([\d.]+)% idle`)

// ParseTopCPU reads the last "CPU usage" line of `top -l 2` on macOS (R5.41).
func ParseTopCPU(text string) (float64, bool) {
	ms := topCPU.FindAllStringSubmatch(text, -1)
	if len(ms) == 0 {
		return 0, false
	}
	idle, err := strconv.ParseFloat(ms[len(ms)-1][1], 64)
	if err != nil || idle < 0 || idle > 100 {
		return 0, false
	}
	return round1(100 - idle), true
}

// CPUBetween is the CPU use between two raw samples. ok is false when the
// counters did not move or went backwards (R5.41).
func CPUBetween(prev, cur Raw) (float64, bool) {
	if cur.Total <= prev.Total || cur.Busy < prev.Busy {
		return 0, false
	}
	pct := float64(cur.Busy-prev.Busy) / float64(cur.Total-prev.Total) * 100
	if pct > 100 {
		pct = 100
	}
	return round1(pct), true
}

// LoadAvgCPU is the 1-minute load average per core, capped at 100% (R5.41).
func LoadAvgCPU(load1 float64, cores int) (float64, bool) {
	if cores < 1 || load1 < 0 {
		return 0, false
	}
	return round1(math.Min(100, load1/float64(cores)*100)), true
}

// Process is one row of a process list.
type Process struct {
	Name   string
	CPU    float64
	Memory float64
}

// ParsePS reads `ps -eo comm=,pcpu=,pmem=` output (names only are kept in the logs, R5.46).
func ParsePS(text string) []Process {
	var out []Process
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		n := len(f)
		cpu, e1 := strconv.ParseFloat(f[n-2], 64)
		mem, e2 := strconv.ParseFloat(f[n-1], 64)
		if e1 != nil || e2 != nil {
			continue
		}
		name := strings.Join(f[:n-2], " ")
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, Process{Name: name, CPU: cpu, Memory: mem})
	}
	return out
}

// ParsePowerShellCSV reads `Get-Process | ConvertTo-Csv` rows of name, CPU seconds and working set bytes.
func ParsePowerShellCSV(text string) []Process {
	var out []Process
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) < 3 {
			continue
		}
		cpu, _ := strconv.ParseFloat(strings.Trim(f[1], `"`), 64)
		mem, _ := strconv.ParseFloat(strings.Trim(f[2], `"`), 64)
		out = append(out, Process{Name: strings.Trim(f[0], `"`), CPU: cpu, Memory: mem})
	}
	return out
}

// TopNames names the top n processes by CPU and by memory, names only (R5.46).
func TopNames(ps []Process, n int) (byCPU, byMemory []string) {
	pick := func(less func(a, b Process) bool) []string {
		c := append([]Process(nil), ps...)
		sort.SliceStable(c, func(i, j int) bool { return less(c[i], c[j]) })
		seen := map[string]bool{}
		var names []string
		for _, p := range c {
			if len(names) == n {
				break
			}
			if p.Name != "" && !seen[p.Name] {
				seen[p.Name] = true
				names = append(names, p.Name)
			}
		}
		return names
	}
	return pick(func(a, b Process) bool { return a.CPU > b.CPU }), pick(func(a, b Process) bool { return a.Memory > b.Memory })
}
