package debugaccess

import (
	"regexp"
	"sort"
	"strings"
)

// shellEnvAllow is the only environment a debug shell gets (R5.12): the names
// a shell needs and the pool's own tool folders. Nothing is copied from the
// runner, so the job's secrets, GITHUB_TOKEN, the JIT config and the agent's
// keys never reach it.
var shellEnvAllow = []string{
	"PATH", "HOME", "TERM", "LANG", "LC_ALL", "TZ", "USER", "SHELL",
	"CI_TOOLS_DIR", "CARGO_TARGET_DIR", "CARGO_HOME", "RUSTUP_HOME", "GOPATH", "GOCACHE", "GOMODCACHE",
	"PNPM_HOME", "npm_config_cache", "PIP_CACHE_DIR", "UV_CACHE_DIR", "DOTNET_CLI_HOME", "NUGET_PACKAGES",
	"GRADLE_USER_HOME", "MAVEN_OPTS",
}

// secretName is a second guard: an allowed name never carries a value whose
// name looks like a secret (no allowed name matches it today).
var secretName = regexp.MustCompile(`(?i)(token|secret|password|passwd|credential|private|key|auth|jit)`)

// AllowedEnvNames lists the allow list, sorted.
func AllowedEnvNames() []string {
	out := append([]string(nil), shellEnvAllow...)
	sort.Strings(out)
	return out
}

// ShellEnv builds the environment of a debug shell from the values the pool
// sets (the runner's pool values, runners R5.53). Every name outside the allow
// list is dropped, a value with a line break is dropped, and the redactor runs
// over each value so a secret shape that slipped into a path never passes.
// TERM defaults to xterm-256color. The result is sorted NAME=value pairs.
func ShellEnv(pool map[string]string, redact func(string) string) []string {
	allow := make(map[string]bool, len(shellEnvAllow))
	for _, n := range shellEnvAllow {
		allow[n] = true
	}
	vals := map[string]string{"TERM": "xterm-256color"}
	for k, v := range pool {
		if !allow[k] || secretName.MatchString(k) || strings.ContainsAny(v, "\r\n\x00") {
			continue
		}
		if redact != nil && redact(v) != v {
			continue
		}
		vals[k] = v
	}
	out := make([]string, 0, len(vals))
	for k, v := range vals {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
