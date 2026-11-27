//go:build darwin

package load

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type osProbe struct{}

// NewProbe returns the probe of this operating system.
func NewProbe() Probe { return osProbe{} }

func run(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err == nil
}

func (osProbe) Memory() (float64, bool) {
	vm, ok := run("vm_stat")
	if !ok {
		return 0, false
	}
	ms, ok := run("sysctl", "-n", "hw.memsize")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(ms), 10, 64)
	if err != nil {
		return 0, false
	}
	return ParseVMStat(vm, n)
}

// CPURaw is not available on macOS: the CPU figure comes from top (below), as
// the load average per core, so a measurement never sleeps.
func (osProbe) CPURaw() (Raw, bool) { return Raw{}, false }

func (osProbe) LoadAvgPerCore() (float64, bool) {
	if out, ok := run("top", "-l", "2", "-n", "0", "-s", "0"); ok {
		if pct, ok := ParseTopCPU(out); ok {
			return pct, true
		}
	}
	out, ok := run("sysctl", "-n", "vm.loadavg")
	if !ok {
		return 0, false
	}
	f := strings.Fields(strings.Trim(out, "{} \n"))
	if len(f) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return LoadAvgCPU(n, runtime.NumCPU())
}
