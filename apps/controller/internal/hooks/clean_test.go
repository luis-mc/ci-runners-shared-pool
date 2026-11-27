package hooks_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hooks"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hygiene"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// runner is a fake runner folder with a job's leftovers (runners R5.91).
type runner struct {
	t              *testing.T
	folder, work   string
	workspace      string
	tmp, sharedTmp string
	env            map[string]string
}

func mk(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func newRunner(t *testing.T, keep bool) *runner {
	t.Helper()
	root := t.TempDir()
	r := &runner{t: t, folder: filepath.Join(root, "runner"), tmp: filepath.Join(root, "tmp-runner"), sharedTmp: filepath.Join(root, "shared-tmp")}
	r.work = filepath.Join(r.folder, "_work")
	r.workspace = filepath.Join(r.work, "repo", "repo")
	mk(t,
		filepath.Join(r.workspace, "src", "main.go"),
		filepath.Join(r.work, "_temp", "script.sh"),
		filepath.Join(r.work, "_temp", "_runner_file_commands", "save_state"),
		filepath.Join(r.work, "_actions", "actions", "checkout", "action.yml"),
		filepath.Join(r.work, "_tool", "node", "22", "bin"),
		filepath.Join(r.tmp, "build-123", "a.o"),
		filepath.Join(r.sharedTmp, "mine-1"),
		filepath.Join(r.sharedTmp, "other-1"),
		filepath.Join(r.folder, "cache", "tools", "node", "bin"),
		filepath.Join(r.folder, "cache", "npm", "_cacache", "x"),
		filepath.Join(r.folder, "cache", "go", "mod", "m"),
	)
	if err := hygiene.WriteKeep(osw{}, r.folder, keep); err != nil {
		t.Fatal(err)
	}
	r.env = map[string]string{
		hygiene.EnvRunnerDir: r.folder, "GITHUB_WORKSPACE": r.workspace, "TMPDIR": r.tmp, "RUNNER_TEMP": filepath.Join(r.work, "_temp"),
	}
	return r
}

type osw struct{}

func (osw) WriteFile(p string, d []byte, m fs.FileMode) error { return os.WriteFile(p, d, m) }

func (r *runner) cleaner(phase string) *hooks.Cleaner {
	return &hooks.Cleaner{
		Getenv: func(k string) string { return r.env[k] }, Phase: phase, Self: "1001", Pid: 500,
		Owner: func(fi fs.FileInfo) string {
			if strings.HasPrefix(fi.Name(), "other") {
				return "2002"
			}
			return "1001"
		},
		Shared: []string{r.sharedTmp},
		Sleep:  func(time.Duration) {},
	}
}

func TestCleanAlwaysRemovesTemporaryFilesWhateverTheDependencySetting(t *testing.T) {
	for _, keep := range []bool{true, false} {
		for _, phase := range []string{hooks.PhaseCompleted, hooks.PhaseStarted} {
			r := newRunner(t, keep)
			res := r.cleaner(phase).Clean()
			if why := res.Unclean(); why != "" {
				t.Fatalf("keep=%v %s: unclean: %s", keep, phase, why)
			}
			if exists(filepath.Join(r.workspace, "src")) {
				t.Errorf("keep=%v %s: the workspace was not emptied", keep, phase)
			}
			if exists(filepath.Join(r.work, "_temp", "script.sh")) {
				t.Errorf("keep=%v %s: RUNNER_TEMP kept a file", keep, phase)
			}
			if exists(filepath.Join(r.tmp, "build-123")) {
				t.Errorf("keep=%v %s: TMPDIR kept a folder", keep, phase)
			}
			if exists(filepath.Join(r.sharedTmp, "mine-1")) {
				t.Errorf("keep=%v %s: /tmp entry of the runner user stays", keep, phase)
			}
			if !exists(filepath.Join(r.sharedTmp, "other-1")) {
				t.Errorf("keep=%v %s: an entry of another user was removed", keep, phase)
			}
			if !exists(r.work) || !exists(filepath.Join(r.work, "_temp")) {
				t.Errorf("keep=%v %s: _work or _temp itself was removed", keep, phase)
			}
		}
	}
}

func TestCleanDependenciesAreKeptOnlyWhenTheSettingIsOn(t *testing.T) {
	on := newRunner(t, true)
	if why := on.cleaner(hooks.PhaseCompleted).Clean().Unclean(); why != "" {
		t.Fatal(why)
	}
	for _, p := range []string{"cache/tools/node/bin", "cache/npm/_cacache/x", "cache/go/mod/m", "_work/_actions/actions/checkout/action.yml", "_work/_tool/node/22/bin"} {
		if !exists(filepath.Join(on.folder, filepath.FromSlash(p))) {
			t.Errorf("keep on: %s was removed", p)
		}
	}
	off := newRunner(t, false)
	if why := off.cleaner(hooks.PhaseCompleted).Clean().Unclean(); why != "" {
		t.Fatal(why)
	}
	for _, p := range []string{"cache/tools/node", "cache/npm/_cacache", "cache/go/mod", "_work/_actions/actions", "_work/_tool/node"} {
		if exists(filepath.Join(off.folder, filepath.FromSlash(p))) {
			t.Errorf("keep off: %s stays", p)
		}
	}
	if !exists(filepath.Join(off.folder, "cache")) {
		t.Error("keep off: the dependency folder itself must stay")
	}
}

func TestCleanBeforeTheNextJobSpareTheFilesTheRunnerMadeForIt(t *testing.T) {
	r := newRunner(t, true)
	if why := r.cleaner(hooks.PhaseStarted).Clean().Unclean(); why != "" {
		t.Fatal(why)
	}
	if !exists(filepath.Join(r.work, "_temp", "_runner_file_commands", "save_state")) {
		t.Error("the file commands of the starting job were removed")
	}
	if !exists(r.workspace) {
		t.Error("the workspace folder of the starting job must stay (emptied)")
	}
	st, err := hygiene.ReadState(osr{}, r.folder)
	if err != nil || st.Phase != hooks.PhaseStarted || st.UncleanReason != "" || st.At.IsZero() {
		t.Fatalf("state %+v %v", st, err)
	}
}

type osr struct{}

func (osr) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }

func TestCleanEndsProcessesTheJobLeftAndNothingElse(t *testing.T) {
	r := newRunner(t, true)
	c := r.cleaner(hooks.PhaseCompleted)
	c.Procs = func() ([]hooks.ProcInfo, error) {
		return []hooks.ProcInfo{
			{PID: 1, PPID: 0, Owner: "0", Name: "init"},
			{PID: 100, PPID: 1, Owner: "1001", Name: "runsvc.sh"},
			{PID: 200, PPID: 100, Owner: "1001", Name: "Runner.Listener"},
			{PID: 300, PPID: 200, Owner: "1001", Name: "Runner.Worker"},
			{PID: 400, PPID: 300, Owner: "1001", Name: "sh"},
			{PID: 500, PPID: 400, Owner: "1001", Name: "crsp"},
			{PID: 600, PPID: 1, Owner: "1001", Name: "node"},          // left by the job
			{PID: 601, PPID: 600, Owner: "1001", Name: "chromium"},    // its child
			{PID: 700, PPID: 1, Owner: "2002", Name: "node"},          // another user
			{PID: 800, PPID: 1, Owner: "1001", Name: "Runner.Worker"}, // runner chain by name
		}, nil
	}
	var killed []int64
	c.End = func(pid int64) error { killed = append(killed, pid); return nil }
	res := c.Clean()
	if res.Unclean() != "" {
		t.Fatal(res.Unclean())
	}
	if len(killed) != 2 || killed[0] != 601 || killed[1] != 600 {
		t.Fatalf("killed %v, want [601 600]", killed)
	}
}

func TestCleanDoesNotSweepSharedPlacesAsRoot(t *testing.T) {
	r := newRunner(t, true)
	c := r.cleaner(hooks.PhaseCompleted)
	c.Self = "0"
	c.Procs = func() ([]hooks.ProcInfo, error) { t.Fatal("processes listed as root"); return nil, nil }
	c.Clean()
	if !exists(filepath.Join(r.sharedTmp, "mine-1")) {
		t.Error("a root cleaner swept a shared folder")
	}
}

func TestCleanThatFailsIsUncleanAndNamesWhatStays(t *testing.T) {
	r := newRunner(t, true)
	c := r.cleaner(hooks.PhaseCompleted)
	locked := filepath.Join(r.tmp, "build-123")
	c.Remove = func(p string) error {
		if p == locked {
			return &fs.PathError{Op: "remove", Path: p, Err: errors.New("file in use")}
		}
		return os.RemoveAll(p)
	}
	res := c.Clean()
	why := res.Unclean()
	if why == "" || !strings.Contains(why, "build-123") || !strings.Contains(why, "file in use") {
		t.Fatalf("reason %q", why)
	}
	st, _ := hygiene.ReadState(osr{}, r.folder)
	if st.UncleanReason != why {
		t.Fatalf("state %q, want %q", st.UncleanReason, why)
	}
	// the next clean-up that passes makes the runner healthy again
	c.Remove = nil
	if again := c.Clean().Unclean(); again != "" {
		t.Fatal(again)
	}
	if st, _ = hygiene.ReadState(osr{}, r.folder); st.UncleanReason != "" {
		t.Fatalf("still unclean: %q", st.UncleanReason)
	}
}

func gateCfg(clean func() hooks.CleanResult, now *time.Time) hooks.GateConfig {
	return hooks.GateConfig{
		Machine: slots.Defaults(), Gate: slots.GateSettings{PollSec: 30, MaxWaitMin: 2, OnTimeout: "fail"},
		Measure: func() load.Reading {
			return load.Reading{MemoryPct: 1, MemoryKnown: true, CPUPct: 1, CPUKnown: true}
		},
		Clean: clean, Now: func() time.Time { return *now },
		Sleep: func(_ context.Context, d time.Duration) { *now = now.Add(d) },
	}
}

func TestUncleanRunnerTakesNoJobThenGoesWhenClean(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	calls := 0
	cfg := gateCfg(func() hooks.CleanResult {
		calls++
		if calls < 3 {
			return hooks.CleanResult{Failed: []string{"/tmp/x"}}
		}
		return hooks.CleanResult{}
	}, &now)
	if code := hooks.RunGate(context.Background(), cfg); code != hooks.ExitGo {
		t.Fatalf("exit %d after %d clean-ups", code, calls)
	}
	if calls != 3 {
		t.Fatalf("the clean-up was retried %d times, want 3", calls)
	}
	// never clean: the job fails after the maximum wait and can be re-run
	now = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	cfg = gateCfg(func() hooks.CleanResult { return hooks.CleanResult{Failed: []string{"/tmp/x"}} }, &now)
	if code := hooks.RunGate(context.Background(), cfg); code != hooks.ExitTimeout {
		t.Fatalf("an unclean runner took a job (exit %d)", code)
	}
}

func TestJobDoneCleansAndReportsUnclean(t *testing.T) {
	var reported string
	code := hooks.RunJobDone(context.Background(), hooks.DoneConfig{
		KeepWork: true, // never a way to keep the temporary files
		Clean: func() hooks.CleanResult {
			return hooks.CleanResult{Failed: []string{"the process node (9), which will not end"}}
		},
		Report: func(s string) { reported = s },
	})
	if code != 0 || !strings.Contains(reported, "not clean") || !strings.Contains(reported, "node (9)") {
		t.Fatalf("code %d reported %q", code, reported)
	}
}

func TestEnabledOnlyForARunnerOfTheProduct(t *testing.T) {
	if hooks.Enabled(func(string) string { return "" }) {
		t.Error("a hook run by hand must not clean")
	}
	if !hooks.Enabled(func(k string) string {
		if k == hygiene.EnvRunnerDir {
			return "/x"
		}
		return ""
	}) {
		t.Error("a runner with CRSP_RUNNER_DIR must clean")
	}
}
