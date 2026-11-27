package hooks

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerprocs"
)

// The always-clean rule of a persistent runner (runners R5.91): after every job,
// and again right before the next job starts, the pool removes the runner's
// _work workspace, the temp folder of the runner account, the /tmp entries the
// runner user owns and every process the job left running. It is not a setting.
// What the setting Keep downloaded dependencies decides is only the dependency
// folder of the runner (runners R5.90).

// Hook phases the cleaner runs in.
const (
	PhaseStarted   = "job-started"
	PhaseCompleted = "job-completed"
)

// ProcInfo is one process of the machine as the cleaner sees it. The hook only
// decides which are leftovers; internal/runnerprocs lists and ends them.
type ProcInfo = runnerprocs.Proc

// runnerProcessNames are the processes of the runner itself, which are never
// ended (compared in lower case, without a .exe suffix).
var runnerProcessNames = map[string]bool{
	"runner.listener": true, "runner.worker": true, "runsvc.sh": true, "runnerservice": true,
	"runsvc": true, "conhost": true,
}

// Cleaner is one clean-up of a persistent runner. The zero value is not usable:
// NewCleaner fills the real parts, and tests replace them.
type Cleaner struct {
	Getenv func(string) string
	Phase  string
	// Self is the account the hook runs as. An empty Self or the root account
	// ("0") turns off the parts that sweep shared places (/tmp, processes).
	Self string
	// Pid is the hook's own process; it and its ancestors are never ended.
	Pid int64
	// Procs lists the machine's processes; End ends one (both from internal/runnerprocs).
	Procs func() ([]ProcInfo, error)
	End   func(pid int64) error
	// Owner gives the account of a file entry, "" when unknown.
	Owner func(fs.FileInfo) string
	// Shared are folders that other accounts also write (/tmp): only entries
	// the runner account owns are removed there.
	Shared []string
	// Remove deletes one path; nil is os.RemoveAll (tests make it fail).
	Remove func(string) error
	// Sleep waits between retries of a removal; nil is real.
	Sleep func(time.Duration)
	Now   func() time.Time
	Log   func(string)
}

// CleanResult is what one clean-up did.
type CleanResult struct {
	Removed []string
	Failed  []string
	Err     error
}

// Unclean is the reason in plain words, or "" when the runner is clean.
func (r CleanResult) Unclean() string {
	if r.Err != nil && len(r.Failed) == 0 {
		return "the clean-up could not run: " + r.Err.Error()
	}
	if len(r.Failed) == 0 {
		return ""
	}
	shown := r.Failed
	more := ""
	if len(shown) > 3 {
		more = fmt.Sprintf(" and %d more", len(shown)-3)
		shown = shown[:3]
	}
	return "the clean-up of the last job left " + strings.Join(shown, "; ") + more + " in place"
}

func (c *Cleaner) logf(format string, a ...any) {
	if c.Log != nil {
		c.Log("runner clean: " + fmt.Sprintf(format, a...))
	}
}

func (c *Cleaner) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (c *Cleaner) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Cleaner) rootOrUnknown() bool { return c.Self == "" || c.Self == "0" }

// Enabled is true when this hook runs for a persistent runner of the product:
// the runner's .env names its folder. A hook run by hand never cleans.
func Enabled(getenv func(string) string) bool { return getenv(hygiene.EnvRunnerDir) != "" }

// Clean runs the always-clean rule and writes the state file the agent reads.
// It never panics and never returns early on one failure: every part runs, and
// what stays is named in the result.
func (c *Cleaner) Clean() (res CleanResult) {
	folder := c.Getenv(hygiene.EnvRunnerDir)
	if folder == "" {
		return CleanResult{Err: fmt.Errorf("%s is not set", hygiene.EnvRunnerDir)}
	}
	defer func() {
		if r := recover(); r != nil {
			res.Err = fmt.Errorf("%v", r)
		}
		st := hygiene.State{UncleanReason: res.Unclean(), At: c.now(), Phase: c.Phase}
		if err := hygiene.WriteState(osWriter{}, folder, st); err != nil {
			c.logf("could not write the state file (%v)", err)
		}
	}()
	keep := hygiene.ReadKeep(osReader{}, folder)
	c.endProcesses(&res)
	// A second pass removes what a process held open a moment ago.
	for attempt := 0; attempt < 3; attempt++ {
		pass := CleanResult{}
		c.sweep(folder, keep, &pass)
		if len(pass.Failed) == 0 || attempt == 2 {
			res.Removed = append(res.Removed, pass.Removed...)
			res.Failed = append(res.Failed, pass.Failed...)
			break
		}
		res.Removed = append(res.Removed, pass.Removed...)
		c.sleep(200 * time.Millisecond)
	}
	if !keep {
		c.emptyDependencies(folder, &res)
	}
	c.logf("%s: removed %d entries, %d stay (dependencies %s)", c.Phase, len(res.Removed), len(res.Failed), map[bool]string{true: "kept", false: "removed"}[keep])
	return res
}

func (c *Cleaner) endProcesses(res *CleanResult) {
	if c.Procs == nil || c.End == nil || c.rootOrUnknown() {
		return
	}
	all, err := c.Procs()
	if err != nil {
		c.logf("the process list cannot be read (%v)", err)
		return
	}
	byPID := map[int64]ProcInfo{}
	for _, p := range all {
		byPID[p.PID] = p
	}
	keep := map[int64]bool{c.Pid: true}
	for pid, hops := c.Pid, 0; hops < 64; hops++ {
		p, ok := byPID[pid]
		if !ok || p.PPID <= 0 || keep[p.PPID] {
			break
		}
		keep[p.PPID] = true
		pid = p.PPID
	}
	var victims []ProcInfo
	for _, p := range all {
		name := strings.TrimSuffix(strings.ToLower(p.Name), ".exe")
		if p.Owner != c.Self || keep[p.PID] || runnerProcessNames[name] {
			continue
		}
		victims = append(victims, p)
	}
	// Newest first: children go before the parent that would start them again.
	sort.Slice(victims, func(i, j int) bool { return victims[i].PID > victims[j].PID })
	for _, p := range victims {
		if err := c.End(p.PID); err != nil && processAlive(c, p.PID) {
			res.Failed = append(res.Failed, fmt.Sprintf("the process %s (%d), which will not end", p.Name, p.PID))
			continue
		}
		res.Removed = append(res.Removed, fmt.Sprintf("process %s (%d)", p.Name, p.PID))
	}
}

// processAlive asks the process list again, so a process that ended by itself
// between the list and the kill is not a failure.
func processAlive(c *Cleaner, pid int64) bool {
	all, err := c.Procs()
	if err != nil {
		return true
	}
	for _, p := range all {
		if p.PID == pid {
			return true
		}
	}
	return false
}

// workDir is the runner's _work folder: from the job's workspace when it lies
// in one, else the runner folder's own.
func workDir(getenv func(string) string, runnerFolder string) string {
	if ws := getenv("GITHUB_WORKSPACE"); ws != "" {
		if abs, err := filepath.Abs(ws); err == nil {
			parts := strings.Split(abs, string(filepath.Separator))
			for i := len(parts) - 1; i > 0; i-- {
				if strings.EqualFold(parts[i], "_work") {
					return strings.Join(parts[:i+1], string(filepath.Separator))
				}
			}
		}
	}
	return filepath.Join(runnerFolder, "_work")
}

// startProtect are the names under _work/_temp the runner made for the job that
// is starting; the job-started hook leaves them.
var startProtect = []string{"_runner_file_commands", "_github_", "_temp"}

// sharedKeep are the entries of a temp folder that the runner's own .NET
// process uses; a live runner holds them open, so they are never removed.
var sharedKeep = []string{".dotnet", ".net", "dotnet-diagnostic-", "clr-debug-pipe-", "CoreFxPipe_", "system-commandline-sentinel-files"}

func (c *Cleaner) sweep(runnerFolder string, keep bool, res *CleanResult) {
	work := workDir(c.Getenv, runnerFolder)
	emptyKeep := map[string]bool{}
	protect := map[string]bool{filepath.Join(work, "_PipelineMapping"): true}
	if keep {
		protect[filepath.Join(work, "_actions")] = true
		protect[filepath.Join(work, "_tool")] = true
	}
	tempDir := filepath.Join(work, "_temp")
	emptyKeep[tempDir] = true
	if c.Phase == PhaseStarted {
		if ws := c.Getenv("GITHUB_WORKSPACE"); ws != "" {
			if abs, err := filepath.Abs(ws); err == nil && strings.HasPrefix(abs, work+string(filepath.Separator)) {
				emptyKeep[abs] = true
				emptyKeep[filepath.Dir(abs)] = true
			}
		}
	}
	c.empty(work, "", emptyKeep, protect, nil, res)

	seen := map[string]bool{tempDir: true}
	for _, k := range []string{"TMPDIR", "RUNNER_TEMP", "TEMP", "TMP"} {
		t := c.Getenv(k)
		if t == "" || seen[t] || !safeTempRoot(t, runnerFolder) {
			continue
		}
		seen[t] = true
		c.empty(t, c.Self, nil, nil, sharedKeep, res)
	}
	if !c.rootOrUnknown() {
		for _, s := range c.Shared {
			if !seen[s] {
				seen[s] = true
				c.empty(s, c.Self, nil, nil, sharedKeep, res)
			}
		}
	}
}

// safeTempRoot refuses a temp folder that is the filesystem root, the runner
// folder itself or one of its parents.
func safeTempRoot(t, runnerFolder string) bool {
	abs, err := filepath.Abs(t)
	if err != nil || filepath.Dir(abs) == abs {
		return false
	}
	rf, _ := filepath.Abs(runnerFolder)
	return abs != rf && !strings.HasPrefix(rf+string(filepath.Separator), abs+string(filepath.Separator))
}

// empty removes the entries of dir. owner, when set, limits it to entries that
// account owns (a shared folder such as /tmp). A folder in emptyKeep is emptied
// and stays; a path in protect stays; a name with a prefix in skip stays.
func (c *Cleaner) empty(dir, owner string, emptyKeep, protect map[string]bool, skip []string, res *CleanResult) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no folder, nothing to clean
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if protect[p] || hasPrefixAny(e.Name(), skip) || (c.Phase == PhaseStarted && filepath.Base(dir) == "_temp" && hasPrefixAny(e.Name(), startProtect)) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // gone already
		}
		if owner != "" {
			if c.Owner == nil || c.Owner(info) != owner {
				continue // another account's entry, never touched
			}
			if m := info.Mode(); m&(os.ModeSocket|os.ModeNamedPipe|os.ModeDevice) != 0 {
				continue
			}
		}
		if emptyKeep[p] && info.IsDir() {
			c.empty(p, owner, emptyKeep, protect, skip, res)
			continue
		}
		if err := c.remove(p); err != nil {
			res.Failed = append(res.Failed, p+" ("+shortReason(err)+")")
			continue
		}
		res.Removed = append(res.Removed, p)
	}
}

func hasPrefixAny(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func shortReason(err error) string {
	var pe *fs.PathError
	if ok := asPathError(err, &pe); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

func asPathError(err error, target **fs.PathError) bool {
	for err != nil {
		if pe, ok := err.(*fs.PathError); ok { //nolint:errorlint // walking by hand, the type is exact
			*target = pe
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// emptyDependencies removes the runner's own dependency folder when the runner
// does not keep its dependencies (runners R5.90). The folder stays, so the
// runner's environment still points at one that exists.
func (c *Cleaner) emptyDependencies(runnerFolder string, res *CleanResult) {
	root := runnercache.DependencyRoot(runnerFolder, filepath.Join)
	c.empty(root, "", nil, nil, nil, res)
}

func (c *Cleaner) remove(p string) error {
	if c.Remove != nil {
		return c.Remove(p)
	}
	return os.RemoveAll(p)
}

type osReader struct{}

func (osReader) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) } //nolint:gosec // the runner folder

type osWriter struct{}

func (osWriter) WriteFile(p string, d []byte, perm fs.FileMode) error {
	return os.WriteFile(p, d, perm)
}
