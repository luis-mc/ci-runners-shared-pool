package load

import "time"

// Result is the outcome of a command the probe ran.
type Result struct {
	Code int
	Out  string
}

// Probe is every OS call the measurement makes. Production uses OSProbe; tests
// feed fixed output.
type Probe interface {
	// GOOS is "linux", "windows" or "darwin".
	GOOS() string
	// ReadFile reads a small text file (/proc/meminfo, cgroup files).
	ReadFile(path string) (string, error)
	// Exists reports whether a path exists.
	Exists(path string) bool
	// Run runs argv and returns its result. ok is false when it cannot run at
	// all (not installed); a non-zero exit is ok with Code != 0.
	Run(argv []string) (res Result, ok bool)
	// NumCPU is the logical CPU count.
	NumCPU() int
	// WinMemory is GlobalMemoryStatusEx: use % and total bytes (Windows only).
	WinMemory() (pct float64, totalBytes int64, err error)
	// WinTimes is GetSystemTimes as busy and total counters (Windows only).
	WinTimes() (Counters, error)
	// Now is the clock.
	Now() time.Time
}
