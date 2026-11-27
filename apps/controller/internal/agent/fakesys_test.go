package agent_test

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/load"
)

type cmdRule struct {
	prefix []string
	res    load.Result
	ok     bool
}

// fakeProbeBase is a table-driven machine.
type fakeProbeBase struct {
	mu       sync.Mutex
	goos     string
	arch     string
	host     string
	files    map[string]string
	cmds     []cmdRule
	cpus     int
	rw       map[string]bool
	diskTot  int64
	diskFree int64
	calls    [][]string
}

type fakeSys struct{ *fakeProbeBase }

func linuxBox() *fakeProbeBase {
	return &fakeProbeBase{
		goos: "linux", arch: "x64", host: "box-1", cpus: 8, diskTot: 500 << 30, diskFree: 200 << 30,
		files: map[string]string{
			"/proc/meminfo": "MemTotal:       32000000 kB\nMemFree:         1000000 kB\nMemAvailable:   20000000 kB\n",
			"/proc/stat":    "cpu  1000 0 1000 8000 0 0 0 0 0 0\n", "/proc/loadavg": "0.4 0.3 0.2 1/100 5\n",
			"/etc/os-release":            "NAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\n",
			"/proc/sys/kernel/osrelease": "6.8.0-generic\n",
			"/proc/cpuinfo":              "processor : 0\nmodel name : Test CPU @ 3GHz\nphysical id : 0\ncore id : 0\n\nprocessor : 1\nmodel name : Test CPU @ 3GHz\nphysical id : 0\ncore id : 0\n\nprocessor : 2\nphysical id : 0\ncore id : 1\n\nprocessor : 3\nphysical id : 0\ncore id : 1\n",
		},
		rw: map[string]bool{},
	}
}

func (f *fakeProbeBase) GOOS() string { return f.goos }
func (f *fakeProbeBase) ReadFile(p string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.files[p]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeProbeBase) Exists(p string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.files[p]
	return ok
}
func (f *fakeProbeBase) NumCPU() int                        { return f.cpus }
func (f *fakeProbeBase) Now() time.Time                     { return time.Now() }
func (f *fakeProbeBase) WinMemory() (float64, int64, error) { return 0, 0, load.ErrNoWindows }
func (f *fakeProbeBase) WinTimes() (load.Counters, error)   { return load.Counters{}, load.ErrNoWindows }
func (f *fakeProbeBase) Hostname() (string, error)          { return f.host, nil }
func (f *fakeProbeBase) Disk(string) (int64, int64, error)  { return f.diskTot, f.diskFree, nil }
func (f *fakeProbeBase) ReadWritable(p string) bool         { return f.rw[p] }
func (f *fakeProbeBase) Arch() string                       { return f.arch }
func (f *fakeProbeBase) Run(argv []string) (load.Result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), argv...))
	for _, c := range f.cmds {
		if len(argv) >= len(c.prefix) && strings.Join(argv[:len(c.prefix)], " ") == strings.Join(c.prefix, " ") {
			return c.res, c.ok
		}
	}
	return load.Result{}, false
}
func (f *fakeProbeBase) did(prefix ...string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if len(c) >= len(prefix) && strings.Join(c[:len(prefix)], " ") == strings.Join(prefix, " ") {
			return true
		}
	}
	return false
}
