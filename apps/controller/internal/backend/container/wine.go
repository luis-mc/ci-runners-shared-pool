package container

import (
	"context"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// WinePrefixTimeout is how long `wineboot --init` may run (runners R5.52).
const WinePrefixTimeout = 180 * time.Second

// RunnerWinePrefix is the Wine prefix of one runner: inside the runner's own
// folder, so two runners never share one and removing the runner removes it
// (runners R5.42).
func RunnerWinePrefix(runnerFolder string) string {
	return path.Join(strings.ReplaceAll(runnerFolder, "\\", "/"), "wine-prefix")
}

// WineEnv is what a job gets for Wine: WINEPREFIX and the old name
// POOL_WINE_PREFIX with the same path (R5.52). TMPDIR is never in it, and
// UnsetWineEnv lists what the job environment must drop (R5.42).
func WineEnv(prefix string) map[string]string {
	return map[string]string{"WINEPREFIX": prefix, "POOL_WINE_PREFIX": prefix}
}

// UnsetWineEnv lists the variables removed from the environment of every Wine
// tool: a set TMPDIR breaks Wine's start.
func UnsetWineEnv() []string { return []string{"TMPDIR"} }

// WinePrefixResult says what PrepareWinePrefix did.
type WinePrefixResult struct {
	Skipped bool // the prefix already held system.reg
	Made    bool // wineboot ran and finished
	// NotNeeded: the image has no windows-packaging layer.
	NotNeeded bool
	// Line is the Wine line the prefix was checked or made for.
	Line    string
	Problem string // why it did not finish; the job starts anyway
}

// PrepareWinePrefix makes the prefix once (runners R5.52): `wineboot --init`
// under `xvfb-run -a` as the job user with WINEDEBUG=-all and a 180 second
// bound, then `wineserver -w`. A prefix that holds system.reg is skipped. A
// failure is returned as Problem and never as an error: the job starts anyway.
// TMPDIR is unset with `env -u`, so a TMPDIR of the agent never reaches Wine.
func PrepareWinePrefix(ctx context.Context, cmd sys.Commands, fs sys.FS, prefix, jobUser string) WinePrefixResult {
	if fs.Exists(path.Join(prefix, "system.reg")) {
		return WinePrefixResult{Skipped: true}
	}
	return makeWinePrefix(ctx, cmd, fs, prefix, jobUser)
}

// makeWinePrefix runs the boot and the wait, with no skip.
func makeWinePrefix(ctx context.Context, cmd sys.Commands, fs sys.FS, prefix, jobUser string) WinePrefixResult {
	if err := fs.MkdirAll(prefix, 0o755); err != nil {
		return WinePrefixResult{Problem: "cannot make " + prefix + ": " + err.Error()}
	}
	bctx, cancel := context.WithTimeout(ctx, WinePrefixTimeout)
	defer cancel()
	res, err := cmd.Run(bctx, WineCommand(prefix, jobUser, WineBootArgv()...))
	if err != nil {
		return WinePrefixResult{Problem: "wineboot did not finish: " + err.Error()}
	}
	if res.ExitCode != 0 {
		return WinePrefixResult{Problem: "wineboot exited " + strconv.Itoa(res.ExitCode)}
	}
	// wineserver -w waits for the prefix to settle; it has the same bound.
	if res, err = cmd.Run(bctx, WineCommand(prefix, jobUser, WineServerArgv()...)); err != nil || res.ExitCode != 0 {
		return WinePrefixResult{Problem: "wineserver -w did not finish cleanly"}
	}
	return WinePrefixResult{Made: true}
}

// WineCommand is one Wine tool as the job user, with TMPDIR unset by `env -u`
// and the Wine variables in the environment (R5.42, R5.52).
func WineCommand(prefix, jobUser string, argv ...string) sys.Command {
	full := append([]string{"-u", jobUser}, argv...)
	return sys.Command{Name: "runuser", Args: full, Env: WineProcessEnv(prefix)}
}

// WineProcessEnv is the environment of the prefix tools: the prefix, the old
// name and quiet logs. It never holds TMPDIR.
func WineProcessEnv(prefix string) map[string]string {
	env := WineEnv(prefix)
	env["WINEDEBUG"] = "-all"
	return env
}

func wineArgv(rest ...string) []string {
	return append([]string{"env", "-u", "TMPDIR"}, rest...)
}

// WineBootArgv, WineServerArgv and WineVersionArgv are the three Wine calls.
func WineBootArgv() []string    { return wineArgv("xvfb-run", "-a", "wineboot", "--init") }
func WineServerArgv() []string  { return wineArgv("wineserver", "-w") }
func WineVersionArgv() []string { return wineArgv("wine", "--version") }

// WindowsPackagingLayer is the image layer that holds Wine (R5.50).
const WindowsPackagingLayer = "windows-packaging"

// WinePrefixStamp is the file in the prefix that records the Wine line it was
// made with (R5.52).
const WinePrefixStamp = ".crsp-wine-line"

// NeedsWinePrefix is true when the pool image has the windows-packaging layer:
// only then is a prefix made (R5.52).
func NeedsWinePrefix(layers []string) bool {
	for _, l := range layers {
		if l == WindowsPackagingLayer {
			return true
		}
	}
	return false
}

var wineLineRe = regexp.MustCompile(`^wine-(\d+)\.(\d+)`)

// WineLine reads the line ("9.0") from the output of `wine --version`, such as
// "wine-9.0 (Ubuntu 9.0~repack-4build3)". A patch level is not part of the
// line, so a patch release of Wine keeps the prefix.
func WineLine(versionOutput string) (string, bool) {
	m := wineLineRe.FindStringSubmatch(strings.TrimSpace(versionOutput))
	if m == nil {
		return "", false
	}
	return m[1] + "." + m[2], true
}

// LaneWinePrefix is the prefix folder of a cache lane on the host. A lane
// belongs to one repository (caches R5.43), so two repositories never share a
// prefix. Where a lane is set it wins over RunnerWinePrefix (open question 30).
func LaneWinePrefix(l runnercache.LaneDirs) string { return l.Wine }

// LaneWineJobEnv is what a job in a container gets for Wine: the lane's Wine
// folder as seen inside the container, under both names (R5.52, R5.53).
func LaneWineJobEnv() map[string]string { return WineEnv(LaneInside.Wine) }

// WinePrefixSpec says what EnsureWinePrefix works on.
type WinePrefixSpec struct {
	Prefix  string   // the lane's Wine folder
	JobUser string   // the job user
	Layers  []string // the layers of the pool image
}

// EnsureWinePrefix prepares the prefix of a lane for the first job that needs
// the windows-packaging layer, keeps it, and rebuilds it only when the Wine
// line changed (R5.52). A prefix with no stamp has an unknown line and is
// rebuilt once. A failure is a Problem and never stops the job. The stamp is
// written only after a good boot, so a failed rebuild is tried again.
func EnsureWinePrefix(ctx context.Context, cmd sys.Commands, fs sys.FS, spec WinePrefixSpec) WinePrefixResult {
	if !NeedsWinePrefix(spec.Layers) {
		return WinePrefixResult{NotNeeded: true}
	}
	res, err := cmd.Run(ctx, WineCommand(spec.Prefix, spec.JobUser, WineVersionArgv()...))
	if err != nil || res.ExitCode != 0 {
		return WinePrefixResult{Problem: "wine --version did not run"}
	}
	line, ok := WineLine(res.Stdout)
	if !ok {
		return WinePrefixResult{Problem: "cannot read the Wine line from " + strings.TrimSpace(res.Stdout)}
	}
	stampPath := path.Join(spec.Prefix, WinePrefixStamp)
	if fs.Exists(path.Join(spec.Prefix, "system.reg")) {
		if old, err := fs.ReadFile(stampPath); err == nil && strings.TrimSpace(string(old)) == line {
			return WinePrefixResult{Skipped: true, Line: line}
		}
		if err := fs.RemoveAll(spec.Prefix); err != nil {
			return WinePrefixResult{Line: line, Problem: "cannot remove the old prefix: " + err.Error()}
		}
	}
	out := makeWinePrefix(ctx, cmd, fs, spec.Prefix, spec.JobUser)
	out.Line = line
	if out.Made {
		if err := fs.WriteFile(stampPath, []byte(line+"\n"), 0o644); err != nil {
			out.Problem = "the prefix is made but its stamp is not written: " + err.Error()
		}
	}
	return out
}
