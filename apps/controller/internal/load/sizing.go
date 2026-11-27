package load

import (
	"strconv"
	"strings"
	"time"
)

// SizingSamples and SizingGap are how memory is read for pool sizing: the
// median of 5 samples taken 1 s apart, so a short spike of another program
// does not shrink the room (machines R5.58, scheduling R5.47).
const (
	SizingSamples = 5
	SizingGap     = time.Second
	// PublishedMaxAge is how old a published load figure may be and still count.
	PublishedMaxAge = 60 * time.Second
)

// Sizing is the memory picture the pool-size check needs.
type Sizing struct {
	TotalBytes int64
	// UsedOutsideBytes is host memory used by everything but the WSL VM
	// (median of the samples; the larger of that and a fresh published figure).
	UsedOutsideBytes int64
	// WSLVMBytes is what the VM holds: the larger of working set and private
	// bytes, never less than its containers use (R5.17, R5.47).
	WSLVMBytes int64
	// Published is true when a fresh published figure won.
	Published bool
}

// hostUsed reads used and total host memory once, without the WSL VM
// adjustment of the sampler.
func hostUsed(p Probe) (used, total int64, ok bool) {
	switch p.GOOS() {
	case "windows":
		pct, tot, err := p.WinMemory()
		if err != nil || tot <= 0 {
			return 0, 0, false
		}
		return int64(float64(tot) * pct / 100), tot, true
	case "darwin":
		res, rok := p.Run([]string{"sysctl", "-n", "hw.memsize"})
		if !rok || res.Code != 0 {
			return 0, 0, false
		}
		ms, err := strconv.ParseInt(strings.TrimSpace(res.Out), 10, 64)
		if err != nil || ms <= 0 {
			return 0, 0, false
		}
		vm, vok := p.Run([]string{"vm_stat"})
		if !vok || vm.Code != 0 {
			return 0, 0, false
		}
		pct, perr := ParseVMStat(vm.Out, ms)
		if perr != nil {
			return 0, 0, false
		}
		return int64(float64(ms) * pct / 100), ms, true
	default:
		txt, err := p.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, 0, false
		}
		pct, tot, perr := ParseMeminfo(txt)
		if perr != nil {
			return 0, 0, false
		}
		return int64(float64(tot) * pct / 100), tot, true
	}
}

// SizingRead takes SizingSamples host-memory samples SizingGap apart (sleep is
// injected; production passes a context-aware sleep), takes the median, and
// removes the WSL VM to get the memory used outside it. A published load
// figure (published, from the agent's report) that is fresh and larger wins.
// ok is false when host memory cannot be read.
func SizingRead(p Probe, sleep func(time.Duration), published *Report) (Sizing, bool) {
	var used []float64
	var total int64
	for i := 0; i < SizingSamples; i++ {
		if i > 0 && sleep != nil {
			sleep(SizingGap)
		}
		u, t, ok := hostUsed(p)
		if !ok {
			continue
		}
		used, total = append(used, float64(u)), t
	}
	if len(used) == 0 {
		return Sizing{}, false
	}
	ws, priv := WSLVMParts(p)
	vm := wslVM(ws, priv, 0)
	if p.GOOS() == "windows" {
		cont := ContainerUse(p)
		vm = wslVM(ws, priv, int64(cont.Pool+cont.Other))
	}
	s := Sizing{TotalBytes: total, WSLVMBytes: vm}
	// The host figure counts the VM at its working set; replace it with the VM figure.
	outside := int64(Median(used)) - ws
	if outside < 0 {
		outside = 0
	}
	if published != nil && published.MemoryKnown && p.Now().Sub(published.SampledAt) <= PublishedMaxAge {
		if pub := int64(float64(total) * published.MemoryPct / 100); pub-vm > outside {
			outside, s.Published = pub-vm, true
		}
	}
	s.UsedOutsideBytes = outside
	return s, true
}
