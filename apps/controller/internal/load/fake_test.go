package load

import (
	"errors"
	"strings"
	"time"
)

// fakeProbe feeds fixed output, as load.py's test seams do. Files and commands
// are tables; a command not in the table cannot run.
type fakeProbe struct {
	os       string
	files    map[string]string
	cmds     []fakeCmd
	cpus     int
	now      time.Time
	calls    [][]string
	winMem   []float64 // pct per call
	winTotal int64
	winTimes []Counters
	winErr   error
}

type fakeCmd struct {
	prefix []string
	res    Result
	ok     bool
}

func (f *fakeProbe) GOOS() string { return f.os }
func (f *fakeProbe) ReadFile(p string) (string, error) {
	if v, ok := f.files[p]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeProbe) Exists(p string) bool { _, ok := f.files[p]; return ok }
func (f *fakeProbe) NumCPU() int          { return f.cpus }
func (f *fakeProbe) Now() time.Time       { return f.now }
func (f *fakeProbe) Run(argv []string) (Result, bool) {
	f.calls = append(f.calls, append([]string(nil), argv...))
	for _, c := range f.cmds {
		if len(argv) >= len(c.prefix) && strings.Join(argv[:len(c.prefix)], " ") == strings.Join(c.prefix, " ") {
			return c.res, c.ok
		}
	}
	return Result{}, false
}
func (f *fakeProbe) WinMemory() (float64, int64, error) {
	if f.winErr != nil || len(f.winMem) == 0 {
		return 0, 0, errors.New("no memory")
	}
	v := f.winMem[0]
	if len(f.winMem) > 1 {
		f.winMem = f.winMem[1:]
	}
	return v, f.winTotal, nil
}
func (f *fakeProbe) WinTimes() (Counters, error) {
	if len(f.winTimes) == 0 {
		return Counters{}, errors.New("no times")
	}
	v := f.winTimes[0]
	if len(f.winTimes) > 1 {
		f.winTimes = f.winTimes[1:]
	}
	return v, nil
}
func (f *fakeProbe) calledWSL() bool {
	for _, c := range f.calls {
		if c[0] == "wsl" {
			return true
		}
	}
	return false
}
func (f *fakeProbe) tick(d time.Duration) { f.now = f.now.Add(d) }

// Fixtures ported from dev-standards claude/plugins/delivery/tests/test_load.py.
const (
	meminfo = "MemTotal:       32000000 kB\nMemFree:         1000000 kB\nMemAvailable:    4800000 kB\nCached: 3000000 kB\n"
	statA   = "cpu  1000 0 1000 8000 0 0 0 0 0 0\ncpu0 1 1 1 1\n"
	statB   = "cpu  1500 0 1500 9000 0 0 0 0 0 0\n"
	vmStat  = "Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free:     100000.\nPages active:   300000.\n" +
		"Pages inactive:  200000.\nPages speculative: 20000.\nPages wired down: 150000.\n"
	top = "Processes: 400\nCPU usage: 20.0% user, 10.0% sys, 70.0% idle\nCPU usage: 40.0% user, 25.0% sys, 35.0% idle\n"
)

func linux(files map[string]string) *fakeProbe {
	return &fakeProbe{os: "linux", files: files, cpus: 4, now: time.Unix(1_000_000, 0)}
}
