package hooks

import (
	"fmt"
	"strings"
)

// Hook wrapper file names written next to each persistent runner (R5.59).
const (
	JobStartedPS1   = "job-started.ps1"
	JobCompletedPS1 = "job-completed.ps1"
	JobStartedSH    = "job-started.sh"
	JobCompletedSH  = "job-completed.sh"
)

// Wrappers returns the hook wrapper files for a runner, keyed by file name.
// On Windows they are .ps1 files, never .cmd. Each calls the product binary by
// its full path (installed for all users, never one in a user profile), passes
// its arguments through and returns the binary's exit code (R5.59).
func Wrappers(binary string, windows bool) map[string]string {
	if windows {
		ps := func(hook string) string {
			return fmt.Sprintf("# ACTIONS_RUNNER_HOOK for a self-hosted Windows runner (scheduling R5.59).\n"+
				"& '%s' hook %s @args\nexit $LASTEXITCODE\n", strings.ReplaceAll(binary, "'", "''"), hook)
		}
		return map[string]string{JobStartedPS1: ps("job-started"), JobCompletedPS1: ps("job-completed")}
	}
	sh := func(hook string) string {
		return fmt.Sprintf("#!/bin/sh\n# ACTIONS_RUNNER_HOOK for a self-hosted Linux or macOS runner (scheduling R5.59).\n"+
			"exec '%s' hook %s \"$@\"\n", strings.ReplaceAll(binary, "'", `'\''`), hook)
	}
	return map[string]string{JobStartedSH: sh("job-started"), JobCompletedSH: sh("job-completed")}
}

// RunnerEnv sets the two hooks as the first lines of a runner's .env file,
// replacing any earlier hook lines and keeping the other lines in order (R5.59).
func RunnerEnv(existing, startedPath, completedPath string) string {
	var rest []string
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, "ACTIONS_RUNNER_HOOK_JOB_STARTED=") || strings.HasPrefix(line, "ACTIONS_RUNNER_HOOK_JOB_COMPLETED=") {
			continue
		}
		rest = append(rest, line)
	}
	for len(rest) > 0 && rest[len(rest)-1] == "" {
		rest = rest[:len(rest)-1]
	}
	out := []string{"ACTIONS_RUNNER_HOOK_JOB_STARTED=" + startedPath, "ACTIONS_RUNNER_HOOK_JOB_COMPLETED=" + completedPath}
	return strings.Join(append(out, rest...), "\n") + "\n"
}
