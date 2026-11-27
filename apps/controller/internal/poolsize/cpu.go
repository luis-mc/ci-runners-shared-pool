package poolsize

import "strconv"

// EnvMaxParallel is the job environment name that carries a runner's CPU
// count (runners R5.53).
const EnvMaxParallel = "CI_MAX_PARALLEL"

// PerRunner splits a CPU limit across runners: each gets the whole share, at
// least 1, so `nproc` in a container matches the cap (machines R5.54).
func PerRunner(cpus, runners int) int {
	if runners < 1 {
		runners = 1
	}
	n := cpus / runners
	if n < 1 {
		n = 1
	}
	return n
}

// Env is the job environment value for a runner's CPU count: `CI_MAX_PARALLEL`.
func Env(cpus int) map[string]string {
	if cpus < 1 {
		cpus = 1
	}
	return map[string]string{EnvMaxParallel: strconv.Itoa(cpus)}
}

// CPUSet is the cpuset a container job gets, the first n logical CPUs of the
// runner's range: `0-3` for 4, `0` for 1 (docker `--cpuset-cpus`).
func CPUSet(n int) string {
	if n <= 1 {
		return "0"
	}
	return "0-" + strconv.Itoa(n-1)
}
