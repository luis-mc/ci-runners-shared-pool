package insights

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/metrics"
	"sync"
	"time"
)

// What one analysis pass may cost before crsp warns (owner decisions D18, D19).
const (
	// MaxCPUPercent is the average CPU over one pass, in percent of one core.
	MaxCPUPercent = 5.0
	// MaxRAMBytes is the peak memory during one pass: 300 MB.
	MaxRAMBytes = 300 * 1000 * 1000
	// MaxStoreBytes is the insights store on disk: 1 GB.
	MaxStoreBytes = 1000 * 1000 * 1000
)

// KindCost is the warning finding about the cost of the analysis itself. The engine owns it: it is open while
// the last measured pass was over a limit and resolves when a pass is back under.
const KindCost Kind = "insights.cost"

// Usage is what one pass cost.
type Usage struct {
	CPUPercent   float64 // average over the pass, percent of one core
	PeakRAMBytes int64   // peak memory of the process during the pass
	StoreBytes   int64   // the insights store on disk after the pass
	Took         time.Duration
}

// Over lists the limits the usage is over, in words, in a fixed order.
func (u Usage) Over() []string {
	var out []string
	if u.CPUPercent > MaxCPUPercent {
		out = append(out, fmt.Sprintf("CPU %.1f%% (limit %.0f%%)", u.CPUPercent, MaxCPUPercent))
	}
	if u.PeakRAMBytes > MaxRAMBytes {
		out = append(out, fmt.Sprintf("memory %d MB (limit %d MB)", u.PeakRAMBytes/1e6, int64(MaxRAMBytes)/1e6))
	}
	if u.StoreBytes > MaxStoreBytes {
		out = append(out, fmt.Sprintf("disk %.2f GB (limit %.0f GB)", float64(u.StoreBytes)/1e9, float64(MaxStoreBytes)/1e9))
	}
	return out
}

// Meter measures one pass: Begin starts it, the returned function ends it and gives the usage.
type Meter interface {
	Begin() (end func() Usage)
}

// CostFinding is the warning finding for a usage over a limit, or false when it is under all of them. It
// recommends a slower update period and names the choice (Settings, CI data). It saves no minutes (R5.9).
func CostFinding(u Usage, current Period) (Finding, bool) {
	over := u.Over()
	if len(over) == 0 {
		return Finding{}, false
	}
	current = current.Norm()
	fix := "Choose a slower update period in Settings: analyse after every 5th CI run, or once an hour."
	if current.Mode == ModeEveryNthRun {
		fix = "Choose a slower update period in Settings: once an hour."
	}
	if current.Mode == ModeHourly {
		fix = "The update period is already once an hour. Lower the days or the size of the CI data kept in Settings, or ask for a bigger machine."
	}
	title := "Analysing CI runs costs more than it should: "
	for i, o := range over {
		if i > 0 {
			title += ", "
		}
		title += o
	}
	ev, _ := json.Marshal(map[string]any{
		"cpuPercent": round1(u.CPUPercent), "peakRamBytes": u.PeakRAMBytes, "storeBytes": u.StoreBytes,
		"tookSeconds": round1(u.Took.Seconds()), "period": string(current.Mode), "everyN": current.N,
		"limits": map[string]any{"cpuPercent": MaxCPUPercent, "ramBytes": int64(MaxRAMBytes), "storeBytes": int64(MaxStoreBytes)},
	})
	return Finding{Kind: KindCost, Scope: ScopePool, Subject: "analysis", Area: AreaPool, Title: title, Fix: fix, Evidence: ev}, true
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

// ProcessMeter measures this process: CPU from the Go runtime's CPU time (user and system, not idle) over the
// pass, peak memory sampled every 200 ms, and the store size from StoreSize. The CPU is the whole process, so
// it is an upper bound of the pass itself.
type ProcessMeter struct {
	// StoreSize returns the insights store size on disk in bytes; nil reports 0.
	StoreSize func() int64
	// Clock is the time source; the system clock when nil.
	Clock Clock
}

var cpuSamples = []string{"/cpu/classes/total:cpu-seconds", "/cpu/classes/idle:cpu-seconds", "/memory/classes/total:bytes", "/memory/classes/heap/released:bytes"}

func readRuntime() (busyCPU float64, mem int64) {
	s := make([]metrics.Sample, len(cpuSamples))
	for i, n := range cpuSamples {
		s[i].Name = n
	}
	metrics.Read(s)
	f := func(i int) float64 {
		if s[i].Value.Kind() == metrics.KindFloat64 {
			return s[i].Value.Float64()
		}
		return 0
	}
	u := func(i int) int64 {
		if s[i].Value.Kind() == metrics.KindUint64 {
			return int64(s[i].Value.Uint64())
		}
		return 0
	}
	return f(0) - f(1), u(2) - u(3)
}

// Begin implements Meter.
func (m ProcessMeter) Begin() func() Usage {
	clock := m.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	start := clock.Now()
	cpu0, mem0 := readRuntime()
	var mu sync.Mutex
	peak := mem0
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_, mem := readRuntime()
				mu.Lock()
				if mem > peak {
					peak = mem
				}
				mu.Unlock()
			}
		}
	}()
	return func() Usage {
		close(stop)
		<-done
		took := clock.Now().Sub(start)
		cpu1, mem1 := readRuntime()
		mu.Lock()
		if mem1 > peak {
			peak = mem1
		}
		p := peak
		mu.Unlock()
		u := Usage{PeakRAMBytes: p, Took: took}
		if took > 0 && cpu1 > cpu0 {
			u.CPUPercent = (cpu1 - cpu0) / took.Seconds() * 100
		}
		if m.StoreSize != nil {
			u.StoreBytes = m.StoreSize()
		}
		return u
	}
}

// DirSize is the size in bytes of the files under dir; a missing dir is 0. Unreadable entries are skipped.
func DirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			n += info.Size()
		}
		return nil
	})
	return n
}

// FileSize is the size of one file (the SQLite file and its journal, say); a missing file is 0.
func FileSize(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
