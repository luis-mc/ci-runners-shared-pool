// Package poolsize holds the pure rules for how big a machine's pool may be
// (machines R5.54 and R5.58): the reader of the `.wslconfig` file, the CPU
// split that becomes CI_MAX_PARALLEL, and the check of an owner's pool size
// against the load budget. It reads no file and runs no command; callers pass
// the figures in, so every rule is tested without a machine.
package poolsize

import (
	"strconv"
	"strings"
)

const (
	// GiB is one gibibyte in bytes.
	GiB = int64(1) << 30
	// DefaultWSLMemoryShare is the share of host memory WSL2 takes when
	// `.wslconfig` has no `memory=` (Microsoft documents 50%).
	DefaultWSLMemoryShare = 0.5
)

// WSLConfig is what the `[wsl2]` section of `.wslconfig` declares. A field
// the file does not set is zero (or false) and its Set flag is false.
type WSLConfig struct {
	MemoryBytes int64
	MemorySet   bool
	Processors  int
	ProcSet     bool
	// NestedVirtualization is `nestedVirtualization=true` (R5.53).
	NestedVirtualization bool
}

// ParseWSLConfig reads `.wslconfig` text. Only the `[wsl2]` section counts;
// keys are case-insensitive, `#` and `;` start comments, and a value that does
// not parse is ignored rather than guessed (R5.54: never a guess).
func ParseWSLConfig(text string) WSLConfig {
	var c WSLConfig
	inWSL2 := false
	for _, ln := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		ln = strings.TrimSpace(strings.TrimPrefix(ln, "\xef\xbb\xbf"))
		if ln == "" || ln[0] == '#' || ln[0] == ';' {
			continue
		}
		if ln[0] == '[' {
			inWSL2 = strings.EqualFold(strings.TrimSpace(strings.Trim(ln, "[]")), "wsl2")
			continue
		}
		if !inWSL2 {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		if i := strings.IndexAny(v, "#;"); i >= 0 {
			v = v[:i]
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "memory":
			if n, ok := ParseSize(v); ok {
				c.MemoryBytes, c.MemorySet = n, true
			}
		case "processors":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.Processors, c.ProcSet = n, true
			}
		case "nestedvirtualization":
			c.NestedVirtualization = strings.EqualFold(v, "true")
		}
	}
	return c
}

// ParseSize reads `8GB`, `512MB`, `4096` (bytes) and `6 GiB`. WSL treats GB as
// a binary gigabyte, so does this.
func ParseSize(s string) (int64, bool) {
	s = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"TIB", 1 << 40}, {"TB", 1 << 40}, {"GIB", GiB}, {"GB", GiB}, {"MIB", 1 << 20}, {"MB", 1 << 20}, {"KIB", 1 << 10}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

// Declared is what the WSL VM may use: the declared values, or WSL's own
// defaults (half of host memory, every logical CPU) when the file is silent.
// It never uses a live reading (R5.54).
func (c WSLConfig) Declared(hostMemBytes int64, hostCPUs int) (memBytes int64, cpus int) {
	memBytes = int64(float64(hostMemBytes) * DefaultWSLMemoryShare)
	if c.MemorySet {
		memBytes = c.MemoryBytes
	}
	cpus = hostCPUs
	if c.ProcSet {
		cpus = c.Processors
	}
	return memBytes, cpus
}

// Refusal is nil when a declared WSL value fits the machine, or the reason it
// does not (R5.54: a value above what the machine can give is refused).
func (c WSLConfig) Refusal(hostMemBytes int64, hostCPUs int) string {
	if c.MemorySet && hostMemBytes > 0 && c.MemoryBytes > hostMemBytes {
		return ".wslconfig memory is above the memory of the machine"
	}
	if c.ProcSet && hostCPUs > 0 && c.Processors > hostCPUs {
		return ".wslconfig processors is above the CPUs of the machine"
	}
	return ""
}
