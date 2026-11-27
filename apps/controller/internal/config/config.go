// Package config holds the one validated configuration schema (operations
// R5.8 to R5.10, STACKSPEC 15).
//
// The controller and the agent read one file, config.toml, checked against one
// schema. A CRSP_* environment variable overrides the matching key. The check
// refuses unknown keys, wrong types, values out of range, placeholder values
// and any key that holds a secret. All problems are reported together.
//
// Secrets are not configuration: the GitHub App private key and the webhook
// secret live in the OS secret store, never in the file, env, the database or
// logs.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// Environment variable names. Each overrides the TOML key of the same meaning.
const (
	EnvConfigFile = "CRSP_CONFIG"
	EnvDataDir    = "CRSP_DATA_DIR"
	EnvListenAddr = "CRSP_LISTEN_ADDR"
	EnvAgentAddr  = "CRSP_AGENT_ADDR"
	EnvLogLevel   = "CRSP_LOG_LEVEL"
)

// Defaults for the console and agent link ports (owner decision, 2026-11-14).
const (
	DefaultListenAddr = "127.0.0.1:8460"
	DefaultAgentAddr  = "127.0.0.1:8461"
)

// Config is the validated configuration of a running process.
type Config struct {
	// File is the config file that was read, or "" when none exists.
	File string
	// DataDir holds the database, backups/, logs/ and updates/.
	DataDir string
	// ListenAddr is the console and API address (HTTPS only), host:port.
	ListenAddr string
	// ListenFromSetting says ListenAddr is the address chosen in the console
	// (operations R5.90), not the environment, the file or the default.
	ListenFromSetting bool
	// AgentAddr is the agent link address (gRPC over TLS), host:port.
	AgentAddr string
	// LogLevel is the minimum level written to the structured log.
	LogLevel slog.Level
	// Cache is the declared cache sizing per runner (runners R5.39): tool
	// cache quota, Rust build cache cap and the disk guard threshold.
	Cache runnercache.Quota
	// DemandQuiet is how long the demand source may see no demand for a
	// repository that has queued jobs for the pool before the watchdog restarts
	// it and the doctor warns (github-integration R5.37, doctor `demand.quiet`).
	DemandQuiet time.Duration
	// ScaleSetDemand turns on the runner scale-set API as the demand source,
	// with polling as the automatic fallback (github-integration R5.40). Off by
	// default: the scale-set API is a public preview.
	ScaleSetDemand bool
	// RunnerSweepDays is the age past which an offline registration that crsp
	// made is deleted by itself; 0 turns the sweep off (github-integration
	// R5.33). Default 7.
	RunnerSweepDays int
	// DiskFloorGiB is the free disk a machine needs before it starts a job or
	// a VM, in GiB; 0 turns the floor off (runners R5.83). Default 10.
	DiskFloorGiB int
	// Addresses are the external addresses (GitHub, runner downloads), from
	// the CRSP_GITHUB_URL family of variables (operations OPS35).
	Addresses addresses.Set
}

// DefaultRunnerSweepDays is the default of `runner_sweep_days`.
const DefaultRunnerSweepDays = 7

// DefaultDemandQuietMinutes is the default of `demand_quiet_minutes`.
const DefaultDemandQuietMinutes = 10

// fileSchema is the one schema of config.toml. A key not listed here is refused.
type fileSchema struct {
	DataDir    string `toml:"data_dir"`
	ListenAddr string `toml:"listen_addr"`
	AgentAddr  string `toml:"agent_addr"`
	LogLevel   string `toml:"log_level"`
	// Cache sizing per runner, in GiB; 0 or absent takes the default.
	CacheToolGiB    int `toml:"cache_tool_gib"`
	CacheRustGiB    int `toml:"cache_rust_gib"`
	CacheMinFreePct int `toml:"cache_min_free_pct"`
	// Cap of one Rust cache key, the pool-wide cache budget and the size alert, in GiB.
	CacheRustKeyGiB int `toml:"cache_rust_key_gib"`
	CacheBudgetGiB  int `toml:"cache_budget_gib"`
	CacheAlertGiB   int `toml:"cache_alert_gib"`
	// Minutes of quiet demand before the watchdog acts; 0 or absent takes 10.
	DemandQuietMinutes int `toml:"demand_quiet_minutes"`
	// scaleset_demand: scale-set demand with a polling fallback (off by default).
	ScaleSetDemand bool `toml:"scaleset_demand"`
	// runner_sweep_days: days an offline crsp registration may stay before the
	// sweep deletes it; absent takes 7, 0 turns the sweep off.
	RunnerSweepDays *int `toml:"runner_sweep_days"`
	// Free GiB a machine needs before a job or VM starts; absent takes 10, 0 or less turns it off.
	DiskFloorGiB *int `toml:"disk_floor_gib"`
}

// Keys are the keys config.toml accepts, for docs and the generated reference.
var Keys = []string{"data_dir", "listen_addr", "agent_addr", "log_level", "cache_tool_gib", "cache_rust_gib", "cache_min_free_pct", "cache_rust_key_gib", "cache_budget_gib", "cache_alert_gib", "demand_quiet_minutes", "scaleset_demand", "runner_sweep_days", "disk_floor_gib"}

var (
	placeholder = regexp.MustCompile(`(?i)^\s*(<[^>]*>|\$\{[^}]*\}|change[-_ ]?me|replace[-_ ]?me|todo|tbd|xxx+|your[-_ ].*|example)\s*$`)
	secretKey   = regexp.MustCompile(`(?i)(private[-_]?key|secret|token|password|passwd|credential|api[-_]?key)`)
)

// Paths are the per-OS data folder and config file (operations R5.10).
type Paths struct {
	DataDir    string
	ConfigFile string
}

// DefaultPaths returns the service folders for goos ("windows", "linux",
// "darwin"). getenv supplies ProgramData on Windows.
func DefaultPaths(goos string, getenv func(string) string) Paths {
	switch goos {
	case "windows":
		base := strings.TrimSpace(getenv("ProgramData"))
		if base == "" {
			base = `C:\ProgramData`
		}
		dir := base + `\crsp`
		return Paths{DataDir: dir, ConfigFile: dir + `\config.toml`}
	case "darwin":
		dir := "/Library/Application Support/crsp"
		return Paths{DataDir: dir, ConfigFile: dir + "/config.toml"}
	default:
		return Paths{DataDir: "/var/lib/crsp", ConfigFile: "/etc/crsp/config.toml"}
	}
}

// Load reads and validates the configuration for this OS. getenv is os.Getenv
// in production. A missing config file is not an error; a broken one is.
func Load(getenv func(string) string) (Config, error) {
	return LoadFor(runtime.GOOS, getenv, os.ReadFile)
}

// LoadFor is Load with the OS and the file reader chosen by the caller.
func LoadFor(goos string, getenv func(string) string, readFile func(string) ([]byte, error)) (Config, error) {
	paths := DefaultPaths(goos, getenv)
	var errs []error
	get := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if v != "" && placeholder.MatchString(v) {
			errs = append(errs, fmt.Errorf("%s: %q is a placeholder; set a real value or unset it", name, v))
			return ""
		}
		return v
	}

	file := get(EnvConfigFile)
	explicit := file != ""
	if !explicit {
		file = paths.ConfigFile
	}

	var fs fileSchema
	data, err := readFile(file)
	switch {
	case err == nil:
		var ferrs []error
		fs, ferrs = parseFile(file, data)
		errs = append(errs, ferrs...)
	case errors.Is(err, os.ErrNotExist) && !explicit:
		file = ""
	default:
		errs = append(errs, fmt.Errorf("config file %s: %w", file, err))
		file = ""
	}

	cfg := Config{File: file, LogLevel: slog.LevelInfo}
	pick := func(env, fileVal, def string) string {
		if v := get(env); v != "" {
			return v
		}
		if fileVal != "" {
			return fileVal
		}
		return def
	}
	cfg.DataDir = pick(EnvDataDir, fs.DataDir, paths.DataDir)
	cfg.ListenAddr = pick(EnvListenAddr, fs.ListenAddr, DefaultListenAddr)
	cfg.AgentAddr = pick(EnvAgentAddr, fs.AgentAddr, DefaultAgentAddr)

	if !isAbs(cfg.DataDir, goos) {
		errs = append(errs, fmt.Errorf("data_dir: %q must be an absolute path", cfg.DataDir))
	}
	if err := validateAddr(cfg.ListenAddr); err != nil {
		errs = append(errs, fmt.Errorf("listen_addr: %w", err))
	}
	if err := validateAddr(cfg.AgentAddr); err != nil {
		errs = append(errs, fmt.Errorf("agent_addr: %w", err))
	}
	if cfg.ListenAddr == cfg.AgentAddr {
		errs = append(errs, errors.New("listen_addr and agent_addr must differ"))
	}
	if v := pick(EnvLogLevel, fs.LogLevel, ""); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			errs = append(errs, fmt.Errorf("log_level: %q is not one of debug, info, warn, error", v))
		}
	}

	cfg.Cache = runnercache.Quota{ToolGiB: fs.CacheToolGiB, RustGiB: fs.CacheRustGiB, MinFreePct: fs.CacheMinFreePct,
		RustKeyGiB: fs.CacheRustKeyGiB, BudgetGiB: fs.CacheBudgetGiB, AlertGiB: fs.CacheAlertGiB}.WithDefaults()
	if err := cfg.Cache.Validate(); err != nil {
		errs = append(errs, fmt.Errorf("cache: %w", err))
	}

	switch q := fs.DemandQuietMinutes; {
	case q == 0:
		cfg.DemandQuiet = DefaultDemandQuietMinutes * time.Minute
	case q < 1 || q > 1440:
		errs = append(errs, fmt.Errorf("demand_quiet_minutes: %d must be from 1 to 1440", q))
	default:
		cfg.DemandQuiet = time.Duration(q) * time.Minute
	}

	cfg.ScaleSetDemand = fs.ScaleSetDemand
	switch d := fs.RunnerSweepDays; {
	case d == nil:
		cfg.RunnerSweepDays = DefaultRunnerSweepDays
	case *d < 0 || *d > 365:
		errs = append(errs, fmt.Errorf("runner_sweep_days: %d must be from 0 (off) to 365", *d))
	default:
		cfg.RunnerSweepDays = *d
	}

	cfg.DiskFloorGiB = runnercache.DefaultDiskFloorGiB
	if fs.DiskFloorGiB != nil {
		cfg.DiskFloorGiB = max(*fs.DiskFloorGiB, 0)
	}

	if addr, err := addresses.Load(getenv); err != nil {
		errs = append(errs, err)
	} else {
		cfg.Addresses = addr
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// parseFile checks data against the schema and returns every problem found.
func parseFile(name string, data []byte) (fileSchema, []error) {
	var errs []error
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return fileSchema{}, []error{fmt.Errorf("%s: %w", name, tomlError(err))}
	}
	// Secrets first, so the message is specific and never echoes the value.
	for _, k := range secretKeys(raw, "") {
		errs = append(errs, fmt.Errorf("%s: key %q holds a secret; secrets live in the OS secret store, never in config", name, k))
	}
	var fs fileSchema
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := dec.Decode(&fs); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			for _, d := range strict.Errors {
				key := strings.Join(d.Key(), ".")
				if secretKey.MatchString(key) {
					continue // already reported above
				}
				errs = append(errs, fmt.Errorf("%s: unknown key %q (allowed: %s)", name, key, strings.Join(Keys, ", ")))
			}
		} else {
			errs = append(errs, fmt.Errorf("%s: %w", name, tomlError(err)))
		}
	}
	for _, f := range []struct{ key, val string }{
		{"data_dir", fs.DataDir}, {"listen_addr", fs.ListenAddr}, {"agent_addr", fs.AgentAddr}, {"log_level", fs.LogLevel},
	} {
		if f.val != "" && placeholder.MatchString(f.val) {
			errs = append(errs, fmt.Errorf("%s: %s: %q is a placeholder; set a real value or remove the key", name, f.key, f.val))
		}
	}
	// A placeholder must not reach the validators below as a real value.
	if placeholder.MatchString(fs.DataDir) {
		fs.DataDir = ""
	}
	if placeholder.MatchString(fs.ListenAddr) {
		fs.ListenAddr = ""
	}
	if placeholder.MatchString(fs.AgentAddr) {
		fs.AgentAddr = ""
	}
	if placeholder.MatchString(fs.LogLevel) {
		fs.LogLevel = ""
	}
	return fs, errs
}

// tomlError keeps a decode error's position and drops the document excerpt,
// which could echo a secret value.
func tomlError(err error) error {
	var de *toml.DecodeError
	if errors.As(err, &de) {
		line, col := de.Position()
		return fmt.Errorf("%s (line %d, column %d)", de.Error(), line, col)
	}
	return err
}

func secretKeys(m map[string]any, prefix string) []string {
	var out []string
	for k, v := range m {
		full := prefix + k
		if secretKey.MatchString(k) {
			out = append(out, full)
			continue
		}
		if sub, ok := v.(map[string]any); ok {
			out = append(out, secretKeys(sub, full+".")...)
		}
	}
	sort.Strings(out)
	return out
}

func isAbs(p, goos string) bool {
	if goos == "windows" {
		return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') || strings.HasPrefix(p, `\\`)
	}
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/")
}

func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port", addr)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q has an invalid port", addr)
	}
	return nil
}

// SecretKeys returns the keys of a TOML document that look like secrets. A
// document that does not parse returns nil; the config check reports that.
func SecretKeys(data []byte) []string {
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return secretKeys(raw, "")
}
