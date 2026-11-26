package runners

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Default limits of a job container on a machine that sets no pool size, so an
// uncapped container does not exist (runners R5.63).
const (
	DefaultJobMemoryMB = 4096
	DefaultJobCPUs     = 2.0
	DefaultJobPids     = 4096
	// JobNetwork is the one crsp-made network all job containers of a machine
	// join. It has inter-container communication off.
	JobNetwork = "crsp-jobs"
)

// JobLimits are the three limits of a job container.
type JobLimits struct {
	MemoryMB int
	CPUs     float64
	Pids     int
}

// LimitsFor takes the limits from the machine's pool size (memory in MB, CPUs)
// and fills what is not set with the defaults. A zero pool size gives the
// defaults.
func LimitsFor(poolMemoryMB int, poolCPUs float64) JobLimits {
	l := JobLimits{MemoryMB: poolMemoryMB, CPUs: poolCPUs, Pids: DefaultJobPids}
	if l.MemoryMB <= 0 {
		l.MemoryMB = DefaultJobMemoryMB
	}
	if l.CPUs <= 0 {
		l.CPUs = DefaultJobCPUs
	}
	return l
}

// NetworkCreateArgs are the arguments that make the one job network: no
// traffic between containers, the outside still reachable.
func NetworkCreateArgs() []string {
	return []string{"network", "create", "--driver", "bridge",
		"--opt", "com.docker.network.bridge.enable_icc=false", JobNetwork}
}

// ErrJobContainer is returned for a job container that breaks R5.63.
var ErrJobContainer = errors.New("job container refused")

// CheckJobContainerArgs refuses a `docker run` of a job container that has no
// memory, CPU or pids limit, joins a network other than JobNetwork, or mounts a
// host folder outside allowedHostPaths (the job's work folder, the read-only
// crsp binary and the cache folders the user turned on). Named volumes and
// tmpfs are not host folders. It adds to container.CheckRunArgs.
func CheckJobContainerArgs(args []string, allowedHostPaths []string) error {
	refuse := func(why string) error { return fmt.Errorf("%w: %s", ErrJobContainer, why) }
	var mem, cpus, pids bool
	network := ""
	for i := 0; i < len(args); i++ {
		flag, val, hasVal := strings.Cut(args[i], "=")
		next := func() string {
			if hasVal {
				return val
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch flag {
		case "--memory", "-m":
			v := next()
			mem = v != "" && v != "0"
		case "--cpus", "--cpuset-cpus":
			cpus = next() != ""
		case "--pids-limit":
			v, err := strconv.Atoi(next())
			pids = err == nil && v > 0
		case "--network", "--net":
			network = next()
		case "-v", "--volume":
			src := volumeSource(next())
			if isHostPath(src) && !allowed(src, allowedHostPaths) {
				return refuse("host folder " + src + " is not mounted")
			}
		case "--mount":
			var typ, src string
			for _, kv := range strings.Split(next(), ",") {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "type":
					typ = v
				case "source", "src":
					src = v
				}
			}
			if (typ == "bind" || typ == "") && isHostPath(src) && !allowed(src, allowedHostPaths) {
				return refuse("host folder " + src + " is not mounted")
			}
		}
	}
	switch {
	case !mem:
		return refuse("no memory limit")
	case !cpus:
		return refuse("no CPU limit")
	case !pids:
		return refuse("no process-count limit")
	case network != JobNetwork:
		return refuse("the job joins " + strconv.Quote(network) + ", not " + JobNetwork)
	}
	return nil
}

func isHostPath(s string) bool {
	if s == "" {
		return false
	}
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, `\`) || strings.HasPrefix(s, "./") ||
		(len(s) > 2 && s[1] == ':' && (s[2] == '\\' || s[2] == '/')) || strings.HasPrefix(s, "~")
}

func allowed(p string, list []string) bool {
	for _, a := range list {
		if a == p {
			return true
		}
	}
	return false
}

// volumeSource is the source of a `-v src:dst[:opts]` value, keeping the colon
// of a Windows drive letter in the source.
func volumeSource(v string) string {
	if len(v) > 2 && v[1] == ':' && (v[2] == '\\' || v[2] == '/') {
		rest := v[2:]
		i := strings.Index(rest, ":")
		if i < 0 {
			return v
		}
		return v[:2+i]
	}
	src, _, _ := strings.Cut(v, ":")
	return src
}
