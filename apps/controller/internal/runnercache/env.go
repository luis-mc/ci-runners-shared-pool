package runnercache

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Names of the values a runner exports to its jobs.
const (
	EnvToolsDir  = "CI_TOOLS_DIR"
	EnvToolQuota = "CI_TOOLS_QUOTA_GIB"
	EnvRustCap   = "CRSP_RUST_CACHE_CAP_GIB"
	EnvRustDir   = "CARGO_TARGET_DIR"
	EnvRustupDir = "RUSTUP_HOME"
	EnvCargoDir  = "CARGO_HOME"
	// EnvRustKeyCap is the cap of one Rust cache key, in GiB.
	EnvRustKeyCap = "CRSP_RUST_CACHE_KEY_CAP_GIB"
	// EnvRustKey names the top-level folder of the Rust cache the running job
	// uses; eviction keeps that entry (caches R5.45).
	EnvRustKey = "CRSP_RUST_CACHE_KEY"
	// EnvBudget and EnvAlert are the pool-wide budget and the size alert, in GiB.
	EnvBudget = "CRSP_CACHE_BUDGET_GIB"
	EnvAlert  = "CRSP_CACHE_ALERT_GIB"
)

// Dirs are the cache folders of one runner, all under its own folder, so one
// runner never fills another's quota.
type Dirs struct{ Tools, Rust, Rustup, Cargo string }

// DirsFor gives the cache folders of the runner whose folder is runnerFolder.
func DirsFor(runnerFolder string) Dirs { return DirsJoin(runnerFolder, filepath.Join) }

// DirsJoin is DirsFor with the path join of the runner's OS, for a Windows
// runner folder planned on another OS.
func DirsJoin(runnerFolder string, join func(...string) string) Dirs {
	c := join(runnerFolder, "cache")
	t := join(c, "tools")
	// Tool homes live inside the tool cache folder, so they count in its quota
	// (runners R5.56, caches R5.36).
	return Dirs{Tools: t, Rust: join(c, "rust-target"), Rustup: join(t, "rustup"), Cargo: join(t, "cargo")}
}

// Env is what a persistent runner's .env gets for its caches: CI_TOOLS_DIR (the
// per-runner tool cache folder), CARGO_TARGET_DIR (the Rust build cache folder
// the cap applies to) and the declared quotas.
func Env(runnerFolder string, q Quota) map[string]string {
	return EnvJoin(runnerFolder, q, filepath.Join)
}

// EnvJoin is Env with the path join of the runner's OS.
func EnvJoin(runnerFolder string, q Quota, join func(...string) string) map[string]string {
	q = q.WithDefaults()
	d := DirsJoin(runnerFolder, join)
	return map[string]string{
		EnvToolsDir:   d.Tools,
		EnvRustDir:    d.Rust,
		EnvRustupDir:  d.Rustup,
		EnvCargoDir:   d.Cargo,
		EnvToolQuota:  itoa(q.ToolGiB),
		EnvRustCap:    itoa(q.RustGiB),
		EnvRustKeyCap: itoa(q.RustKeyGiB),
		EnvBudget:     itoa(q.BudgetGiB),
		EnvAlert:      itoa(q.AlertGiB),
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// Cap is one cache folder with its quota in bytes.
type Cap struct {
	Name  string
	Dir   string
	Bytes int64
	// Rust marks the Rust build cache, which has the extra rules of caches
	// R5.45: KeyBytes caps one key and Keep names the key the job uses.
	Rust     bool
	KeyBytes int64
	Keep     string
}

// CapsFromEnv reads the caches a job sees in its environment: the tool cache
// (CI_TOOLS_DIR with CI_TOOLS_QUOTA_GIB) and the Rust build cache
// (CARGO_TARGET_DIR with CRSP_RUST_CACHE_CAP_GIB). A cache with no folder or
// no usable quota is left out, so a runner that was not set up by the
// product evicts nothing.
func CapsFromEnv(getenv func(string) string) []Cap {
	var out []Cap
	for _, c := range []struct{ name, dir, quota string }{
		{"tool cache", EnvToolsDir, EnvToolQuota},
		{"Rust build cache", EnvRustDir, EnvRustCap},
	} {
		dir := getenv(c.dir)
		gib, err := strconv.Atoi(strings.TrimSpace(getenv(c.quota)))
		if dir == "" || err != nil || gib <= 0 {
			continue
		}
		cp := Cap{Name: c.name, Dir: dir, Bytes: int64(gib) * GiB}
		if c.dir == EnvRustDir {
			cp.Rust = true
			cp.Keep = strings.TrimSpace(getenv(EnvRustKey))
			cp.KeyBytes = Quota{RustGiB: gib}.WithDefaults().RustKeyBytes()
			if k, err := strconv.Atoi(strings.TrimSpace(getenv(EnvRustKeyCap))); err == nil && k > 0 {
				cp.KeyBytes = Quota{RustGiB: gib, RustKeyGiB: k}.RustKeyBytes()
			}
		}
		out = append(out, cp)
	}
	return out
}

// Names of the package-manager values a persistent runner points into its own
// dependency folder (runners R5.90).
const (
	EnvRunnerToolCache = "RUNNER_TOOL_CACHE"
	EnvAgentToolsDir   = "AGENT_TOOLSDIRECTORY"
	EnvNpmCache        = "npm_config_cache"
	EnvPnpmStore       = "npm_config_store_dir"
	EnvGoModCache      = "GOMODCACHE"
	EnvGoCache         = "GOCACHE"
	EnvPipCache        = "PIP_CACHE_DIR"
)

// DependencyRoot is the one folder that holds every dependency cache of a
// persistent runner. It is inside the runner's own folder: no other runner
// reads or fills it (runners R5.90).
func DependencyRoot(runnerFolder string, join func(...string) string) string {
	return join(runnerFolder, "cache")
}

// DependencyEnvJoin is what a persistent runner's .env gets so every tool keeps
// its downloads in the runner's own dependency folder: the Actions tool cache,
// the npm and pnpm stores, the Go module and build caches, the pip cache and
// the Playwright browsers. The cargo registry lives in CARGO_HOME, which
// EnvJoin already puts inside the tool cache.
func DependencyEnvJoin(runnerFolder string, join func(...string) string) map[string]string {
	d := DirsJoin(runnerFolder, join)
	root := DependencyRoot(runnerFolder, join)
	return map[string]string{
		EnvRunnerToolCache: d.Tools,
		EnvAgentToolsDir:   d.Tools,
		EnvNpmCache:        join(root, "npm"),
		EnvPnpmStore:       join(root, "pnpm-store"),
		EnvGoModCache:      join(root, "go", "mod"),
		EnvGoCache:         join(root, "go", "build"),
		EnvPipCache:        join(root, "pip"),
		EnvBrowsersPath:    join(root, "playwright"),
	}
}
