package load

import (
	"regexp"
	"strconv"
	"strings"
)

// Refresh reads the slow figures into the cache the samples use: the WSL VM
// (Windows), container memory through Docker (Windows: the docker CLI first,
// then WSL; Linux and macOS never call wsl, R5.18) and macOS CPU through top.
// The agent calls it every 30 seconds in its own goroutine, never inside a
// Sample (R6.6).
func (s *Sampler) Refresh() {
	e := extras{at: s.p.Now()}
	switch s.p.GOOS() {
	case "windows":
		e.wsBytes, e.privBytes = WSLVMParts(s.p)
		cont := ContainerUse(s.p)
		e.containers = int64(cont.Pool + cont.Other)
		e.dockerNote = cont.Note
		e.ownBytes = ownUse(s.p)
	case "darwin":
		if res, ok := s.p.Run([]string{"top", "-l", "2", "-n", "0", "-s", "1"}); ok && res.Code == 0 {
			if v, err := ParseTopCPU(res.Out); err == nil {
				e.macCPU, e.macCPUKnown = v, true
			}
		}
		e.poolContainers = int64(ContainerUse(s.p).Pool)
		e.ownBytes = ownUse(s.p)
	default:
		e.poolContainers = int64(ContainerUse(s.p).Pool)
		e.ownBytes = ownUse(s.p)
	}
	s.mu.Lock()
	s.extras = e
	s.mu.Unlock()
}

// WSLVMParts reads the working set and private bytes of vmmem and vmmemWSL
// (Windows). Both are 0 when there is no VM or it cannot be read (DL177).
func WSLVMParts(p Probe) (workingSet, private int64) {
	if p.GOOS() != "windows" {
		return 0, 0
	}
	res, ok := p.Run([]string{"powershell", "-NoProfile", "-Command",
		"$p = Get-Process -Name vmmem,vmmemWSL -ErrorAction SilentlyContinue; " +
			"($p | Measure-Object WorkingSet64 -Sum).Sum; ($p | Measure-Object PrivateMemorySize64 -Sum).Sum"})
	if !ok || res.Code != 0 {
		return 0, 0
	}
	var nums []int64
	for _, ln := range strings.Split(res.Out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			n, err := strconv.ParseInt(ln, 10, 64)
			if err != nil {
				n = 0
			}
			nums = append(nums, n)
		}
	}
	for len(nums) < 2 {
		nums = append(nums, 0)
	}
	return nums[0], nums[1]
}

// WSLVM is the memory held by the WSL VM: the larger of the working set and
// the private bytes (R5.17, DL177). Windows trims the working set, so it alone
// reads too low.
func WSLVM(p Probe) int64 {
	ws, priv := WSLVMParts(p)
	if priv > ws {
		return priv
	}
	return ws
}

// DockerRun runs `docker <args>`. On Windows it tries the docker CLI on PATH
// first and falls back to `wsl docker` only when that cannot run (R5.18).
// Elsewhere it never calls wsl.
func DockerRun(p Probe, args ...string) (Result, bool) {
	res, ok := p.Run(append([]string{"docker"}, args...))
	if p.GOOS() == "windows" && (!ok || res.Code != 0) {
		if r2, ok2 := p.Run(append([]string{"wsl", "docker"}, args...)); ok2 && r2.Code == 0 {
			return r2, true
		}
	}
	return res, ok
}

// DockerCPUs is the CPUs Docker sees, or 0 when it cannot be read.
func DockerCPUs(p Probe) int {
	res, ok := DockerRun(p, "info", "--format", "{{.NCPU}}")
	if !ok || res.Code != 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.ReplaceAll(res.Out, "\x00", "")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// Containers is memory use of containers in bytes.
type Containers struct {
	// Pool is memory of this pool's containers; Other of every other container.
	Pool, Other float64
	Others      []string
	// Note says why the figure is unknown ("docker cannot be read").
	Note string
}

// PoolLabel marks containers CRSP started.
const PoolLabel = "dev.luismc.crsp"

var memUsage = regexp.MustCompile(`^\s*([0-9.]+)\s*([KMG]i?B|B)`)

// ContainerUse reads `docker ps` and `docker stats` (Windows: the docker CLI
// first, then inside WSL). Zeros and a Note when Docker cannot be read (R5.18).
func ContainerUse(p Probe) Containers {
	var out Containers
	names, ok := DockerRun(p, "ps", "--format", `{{.Names}}`+"\t"+`{{.Label "`+PoolLabel+`"}}`)
	if !ok || names.Code != 0 {
		out.Note = "docker cannot be read"
		return out
	}
	stats, sok := DockerRun(p, "stats", "--no-stream", "--format", "{{.Name}}\t{{.MemUsage}}")
	used := map[string]float64{}
	if sok && stats.Code == 0 {
		for _, ln := range strings.Split(strings.ReplaceAll(stats.Out, "\x00", ""), "\n") {
			cols := strings.Split(strings.TrimRight(ln, "\r"), "\t")
			if len(cols) < 2 {
				continue
			}
			m := memUsage.FindStringSubmatch(cols[1])
			if m == nil {
				continue
			}
			v, _ := strconv.ParseFloat(m[1], 64)
			mult := map[byte]float64{'B': 1, 'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30}[m[2][0]]
			used[cols[0]] = v * mult
		}
	}
	for _, ln := range strings.Split(strings.ReplaceAll(names.Out, "\x00", ""), "\n") {
		cols := strings.Split(strings.TrimRight(ln, "\r"), "\t")
		if len(cols) == 0 || cols[0] == "" {
			continue
		}
		if len(cols) > 1 && strings.TrimSpace(cols[1]) != "" {
			out.Pool += used[cols[0]]
		} else {
			out.Other += used[cols[0]]
			out.Others = append(out.Others, cols[0])
		}
	}
	return out
}
