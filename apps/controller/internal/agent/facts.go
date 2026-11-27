package agent

import (
	"bufio"
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// DetectFacts reports what the machine is (machines R5.10): host name, OS name
// and version, kernel, CPU model, cores and threads, architecture, memory,
// total and free disk on dataDir's volume, and each GPU. A fact that cannot be
// read is left empty; detection never fails the agent.
func DetectFacts(_ context.Context, sys Sys, dataDir string, now time.Time) *agentv1.Facts {
	f := &agentv1.Facts{Arch: sys.Arch(), DetectedAtUnixMs: now.UnixMilli(), Threads: int32(sys.NumCPU())} //nolint:gosec // small
	f.Cores = f.Threads
	if h, err := sys.Hostname(); err == nil {
		f.HostName = h
	}
	if total, free, err := sys.Disk(dataDir); err == nil {
		f.DiskTotalBytes, f.DiskFreeBytes = total, free
	}
	switch sys.GOOS() {
	case "linux":
		f.Os = "linux"
		if txt, err := sys.ReadFile("/etc/os-release"); err == nil {
			f.OsName, f.OsVersion = ParseOSRelease(txt)
		}
		if txt, err := sys.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
			f.KernelVersion = strings.TrimSpace(txt)
		}
		if txt, err := sys.ReadFile("/proc/cpuinfo"); err == nil {
			f.CpuModel, f.Cores, f.Threads = ParseCPUInfo(txt, f.Threads)
		}
		if txt, err := sys.ReadFile("/proc/meminfo"); err == nil {
			f.MemoryBytes = ParseMemTotal(txt)
		}
		f.Gpus = detectLinuxGPUs(sys)
	case "windows":
		f.Os = "windows"
		if r, ok := sys.Run([]string{"powershell", "-NoProfile", "-Command",
			"$o=Get-CimInstance Win32_OperatingSystem;$c=Get-CimInstance Win32_Processor|Select-Object -First 1;" +
				"$m=(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory;" +
				"$o.Caption;$o.Version;$o.BuildNumber;$c.Name;$c.NumberOfCores;$c.NumberOfLogicalProcessors;$m"}); ok && r.Code == 0 {
			applyWindowsFacts(f, r.Out)
		}
		if r, ok := sys.Run([]string{"powershell", "-NoProfile", "-Command",
			"Get-CimInstance Win32_VideoController|ForEach-Object{$_.AdapterCompatibility+'|'+$_.Name+'|'+$_.AdapterRAM}"}); ok && r.Code == 0 {
			f.Gpus = ParseWindowsGPUs(r.Out)
		}
		applyWSLConfig(f, sys)
	case "darwin":
		f.Os = "macos"
		if r, ok := sys.Run([]string{"sw_vers", "-productName"}); ok && r.Code == 0 {
			f.OsName = strings.TrimSpace(r.Out)
		}
		if r, ok := sys.Run([]string{"sw_vers", "-productVersion"}); ok && r.Code == 0 {
			f.OsVersion = strings.TrimSpace(r.Out)
		}
		if r, ok := sys.Run([]string{"uname", "-r"}); ok && r.Code == 0 {
			f.KernelVersion = strings.TrimSpace(r.Out)
		}
		if r, ok := sys.Run([]string{"sysctl", "-n", "machdep.cpu.brand_string"}); ok && r.Code == 0 {
			f.CpuModel = strings.TrimSpace(r.Out)
		}
		if r, ok := sys.Run([]string{"sysctl", "-n", "hw.physicalcpu"}); ok && r.Code == 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(r.Out)); err == nil {
				f.Cores = int32(n) //nolint:gosec // small
			}
		}
		if r, ok := sys.Run([]string{"sysctl", "-n", "hw.memsize"}); ok && r.Code == 0 {
			f.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(r.Out), 10, 64)
		}
		if r, ok := sys.Run([]string{"system_profiler", "SPDisplaysDataType"}); ok && r.Code == 0 {
			f.Gpus = ParseMacGPUs(r.Out)
		}
	}
	if f.OsName == "" {
		f.OsName = f.Os
	}
	return f
}

// ParseOSRelease reads NAME and VERSION_ID from /etc/os-release text.
func ParseOSRelease(txt string) (name, version string) {
	sc := bufio.NewScanner(strings.NewReader(txt))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "NAME":
			name = v
		case "VERSION_ID":
			version = v
		case "PRETTY_NAME":
			if name == "" {
				name = v
			}
		}
	}
	return name, version
}

// ParseCPUInfo reads the model, the physical core count and the thread count
// from /proc/cpuinfo text. fallbackThreads is used when the file lists none.
func ParseCPUInfo(txt string, fallbackThreads int32) (model string, cores, threads int32) {
	type core struct{ phys, id string }
	seen := map[core]bool{}
	var phys string
	procs := int32(0)
	sc := bufio.NewScanner(strings.NewReader(txt))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "model name":
			if model == "" {
				model = v
			}
		case "processor":
			procs++
		case "physical id":
			phys = v
		case "core id":
			seen[core{phys, v}] = true
		}
	}
	threads = procs
	if threads == 0 {
		threads = fallbackThreads
	}
	cores = int32(len(seen)) //nolint:gosec // small
	if cores == 0 {
		cores = threads
	}
	return model, cores, threads
}

var memTotalRe = regexp.MustCompile(`(?m)^MemTotal:\s+(\d+)`)

// ParseMemTotal returns MemTotal in bytes from /proc/meminfo text.
func ParseMemTotal(txt string) int64 {
	m := memTotalRe.FindStringSubmatch(txt)
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseInt(m[1], 10, 64)
	return n * 1024
}

func applyWindowsFacts(f *agentv1.Facts, out string) {
	var l []string
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			l = append(l, ln)
		}
	}
	for len(l) < 7 {
		l = append(l, "")
	}
	f.OsName, f.OsVersion, f.KernelVersion, f.CpuModel = l[0], l[1], l[2], l[3]
	if n, err := strconv.Atoi(l[4]); err == nil {
		f.Cores = int32(n) //nolint:gosec // small
	}
	if n, err := strconv.Atoi(l[5]); err == nil {
		f.Threads = int32(n) //nolint:gosec // small
	}
	f.MemoryBytes, _ = strconv.ParseInt(l[6], 10, 64)
}

// ParseWindowsGPUs reads "vendor|model|adapter ram" lines.
func ParseWindowsGPUs(out string) []*agentv1.Gpu {
	var gpus []*agentv1.Gpu
	for _, ln := range strings.Split(out, "\n") {
		p := strings.Split(strings.TrimSpace(ln), "|")
		if len(p) < 2 || p[1] == "" {
			continue
		}
		g := &agentv1.Gpu{Vendor: p[0], Model: p[1]}
		if len(p) > 2 {
			g.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(p[2]), 10, 64)
		}
		gpus = append(gpus, g)
	}
	return gpus
}

var macChipRe = regexp.MustCompile(`(?m)^\s*Chipset Model:\s*(.+)$`)
var macVendorRe = regexp.MustCompile(`(?m)^\s*Vendor:\s*(.+)$`)

// ParseMacGPUs reads GPUs from `system_profiler SPDisplaysDataType` text.
func ParseMacGPUs(out string) []*agentv1.Gpu {
	var gpus []*agentv1.Gpu
	models := macChipRe.FindAllStringSubmatch(out, -1)
	vendors := macVendorRe.FindAllStringSubmatch(out, -1)
	for i, m := range models {
		g := &agentv1.Gpu{Model: strings.TrimSpace(m[1])}
		if i < len(vendors) {
			g.Vendor = strings.TrimSpace(vendors[i][1])
		}
		gpus = append(gpus, g)
	}
	return gpus
}

var lspciRe = regexp.MustCompile(`(?i)(VGA compatible controller|3D controller|Display controller):\s*(.+)`)

func detectLinuxGPUs(sys Sys) []*agentv1.Gpu {
	var gpus []*agentv1.Gpu
	if r, ok := sys.Run([]string{"nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"}); ok && r.Code == 0 {
		for _, ln := range strings.Split(r.Out, "\n") {
			name, mem, found := strings.Cut(strings.TrimSpace(ln), ",")
			if !found || name == "" {
				continue
			}
			mib, _ := strconv.ParseInt(strings.TrimSpace(mem), 10, 64)
			gpus = append(gpus, &agentv1.Gpu{Vendor: "NVIDIA", Model: strings.TrimSpace(name), MemoryBytes: mib << 20})
		}
		if len(gpus) > 0 {
			return gpus
		}
	}
	if r, ok := sys.Run([]string{"lspci"}); ok && r.Code == 0 {
		for _, m := range lspciRe.FindAllStringSubmatch(r.Out, -1) {
			desc := strings.TrimSpace(m[2])
			vendor, model := "", desc
			for _, v := range []string{"NVIDIA", "AMD", "Intel", "Advanced Micro Devices"} {
				if strings.Contains(desc, v) {
					vendor = v
					break
				}
			}
			gpus = append(gpus, &agentv1.Gpu{Vendor: vendor, Model: model})
		}
	}
	return gpus
}
