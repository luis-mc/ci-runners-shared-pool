//go:build linux

package load

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

type osProbe struct{}

// NewProbe returns the probe of this operating system.
func NewProbe() Probe { return osProbe{} }

func (osProbe) Memory() (float64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	return ParseMeminfo(string(b))
}

func (osProbe) CPURaw() (Raw, bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return Raw{}, false
	}
	return ParseProcStat(string(b))
}

func (osProbe) LoadAvgPerCore() (float64, bool) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return LoadAvgCPU(n, runtime.NumCPU())
}
