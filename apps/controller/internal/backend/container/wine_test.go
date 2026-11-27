package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
)

// TestPrefixPerRunnerAndTmpdirUnset: runners R5.42.
func TestPrefixPerRunnerAndTmpdirUnset(t *testing.T) {
	a, b := RunnerWinePrefix("/srv/runners/r1"), RunnerWinePrefix(`C:\runners\r2`)
	if a == b || !strings.HasPrefix(a, "/srv/runners/r1/") || !strings.HasPrefix(b, "C:/runners/r2/") {
		t.Errorf("prefixes %q %q", a, b)
	}
	env := WineEnv(a)
	if env["WINEPREFIX"] != a || env["POOL_WINE_PREFIX"] != a {
		t.Errorf("env %v", env)
	}
	if _, ok := env["TMPDIR"]; ok {
		t.Error("TMPDIR in the Wine environment")
	}
	if got := UnsetWineEnv(); len(got) != 1 || got[0] != "TMPDIR" {
		t.Errorf("unset %v", got)
	}
	fs, cmd := sysfake.NewFS(), &sysfake.Commands{}
	PrepareWinePrefix(context.Background(), cmd, fs, a, "runner")
	for _, c := range cmd.Calls {
		if _, ok := c.Env["TMPDIR"]; ok {
			t.Error("TMPDIR passed to a Wine tool")
		}
		if !strings.Contains(strings.Join(c.Args, " "), "env -u TMPDIR") {
			t.Errorf("call does not unset TMPDIR: %s", c)
		}
	}
}

// TestPrefixOnceAndKept: runners R5.52.
func TestPrefixOnceAndKept(t *testing.T) {
	fs, cmd := sysfake.NewFS(), &sysfake.Commands{}
	prefix := "/cache/lane/wine"
	r := PrepareWinePrefix(context.Background(), cmd, fs, prefix, "runner")
	if !r.Made || r.Skipped || r.Problem != "" {
		t.Fatalf("first: %+v", r)
	}
	texts := cmd.Texts()
	if len(texts) != 2 || !strings.Contains(texts[0], "xvfb-run -a wineboot --init") || !strings.Contains(texts[1], "wineserver -w") {
		t.Fatalf("calls %v", texts)
	}
	if !strings.HasPrefix(texts[0], "runuser -u runner") {
		t.Errorf("not run as the job user: %s", texts[0])
	}
	if cmd.Calls[0].Env["WINEDEBUG"] != "-all" || cmd.Calls[0].Env["WINEPREFIX"] != prefix || cmd.Calls[0].Env["POOL_WINE_PREFIX"] != prefix {
		t.Errorf("env %v", cmd.Calls[0].Env)
	}

	// A prefix with system.reg is skipped: no call.
	_ = fs.WriteFile(prefix+"/system.reg", []byte("x"), 0o644)
	cmd.Calls = nil
	r = PrepareWinePrefix(context.Background(), cmd, fs, prefix, "runner")
	if !r.Skipped || len(cmd.Calls) != 0 {
		t.Errorf("second: %+v calls %v", r, cmd.Texts())
	}
}

// A failure is a Problem, never an error that stops the job.
func TestPrefixFailureNeverStopsTheJob(t *testing.T) {
	for name, cmd := range map[string]*sysfake.Commands{
		"exit code": {Rules: []sysfake.Rule{{Match: "wineboot", Result: sys.Result{ExitCode: 1}}}},
		"error":     {Rules: []sysfake.Rule{{Match: "wineboot", Err: errors.New("timed out")}}},
		"server":    {Rules: []sysfake.Rule{{Match: "wineserver", Result: sys.Result{ExitCode: 2}}}},
	} {
		r := PrepareWinePrefix(context.Background(), cmd, sysfake.NewFS(), "/p", "runner")
		if r.Made || r.Problem == "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if WinePrefixTimeout.Seconds() != 180 {
		t.Errorf("timeout %s", WinePrefixTimeout)
	}
}

func wineCmd(version string) *sysfake.Commands {
	return &sysfake.Commands{Rules: []sysfake.Rule{{Match: "wine --version", Result: sys.Result{Stdout: version}}}}
}

func boots(cmd *sysfake.Commands) int {
	n := 0
	for _, t := range cmd.Texts() {
		if strings.Contains(t, "wineboot --init") {
			n++
		}
	}
	return n
}

// TestWineLineFromVersion: the line is major.minor; a patch keeps it.
func TestWineLineFromVersion(t *testing.T) {
	for in, want := range map[string]string{
		"wine-9.0 (Ubuntu 9.0~repack-4build3)\n": "9.0",
		"wine-9.0.1":                             "9.0",
		"wine-10.2":                              "10.2",
	} {
		if got, ok := WineLine(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "wine", "Wine 9.0", "bash: wine: not found"} {
		if _, ok := WineLine(bad); ok {
			t.Errorf("%q read as a line", bad)
		}
	}
}

// TestWineCommandBuilding: the argv, the user and the environment of each call.
func TestWineCommandBuilding(t *testing.T) {
	c := WineCommand("/wine", "runner", WineBootArgv()...)
	if got := c.String(); got != "runuser -u runner env -u TMPDIR xvfb-run -a wineboot --init" {
		t.Errorf("boot: %s", got)
	}
	if got := WineCommand("/wine", "runner", WineServerArgv()...).String(); got != "runuser -u runner env -u TMPDIR wineserver -w" {
		t.Errorf("server: %s", got)
	}
	if got := WineCommand("/wine", "runner", WineVersionArgv()...).String(); got != "runuser -u runner env -u TMPDIR wine --version" {
		t.Errorf("version: %s", got)
	}
	want := map[string]string{"WINEPREFIX": "/wine", "POOL_WINE_PREFIX": "/wine", "WINEDEBUG": "-all"}
	if len(c.Env) != 3 {
		t.Errorf("env %v", c.Env)
	}
	for k, v := range want {
		if c.Env[k] != v {
			t.Errorf("env %s=%q", k, c.Env[k])
		}
	}
}

// TestWinePrefixOnlyWithTheLayer: no windows-packaging layer, no prefix and no call.
func TestWinePrefixOnlyWithTheLayer(t *testing.T) {
	if NeedsWinePrefix([]string{"llvm", "kvm"}) || NeedsWinePrefix(nil) || !NeedsWinePrefix([]string{"llvm", "windows-packaging"}) {
		t.Fatal("layer test wrong")
	}
	cmd := wineCmd("wine-9.0")
	r := EnsureWinePrefix(context.Background(), cmd, sysfake.NewFS(), WinePrefixSpec{Prefix: "/w", JobUser: "runner", Layers: []string{"llvm"}})
	if !r.NotNeeded || len(cmd.Calls) != 0 {
		t.Errorf("%+v calls %v", r, cmd.Texts())
	}
}

// TestWineStampRebuildsOnlyWhenTheLineChanges: R5.52.
func TestWineStampRebuildsOnlyWhenTheLineChanges(t *testing.T) {
	fs := sysfake.NewFS()
	spec := WinePrefixSpec{Prefix: "/cache/lane/wine", JobUser: "runner", Layers: []string{"windows-packaging"}}
	ctx := context.Background()

	cmd := wineCmd("wine-9.0 (Ubuntu)")
	r := EnsureWinePrefix(ctx, cmd, fs, spec)
	if !r.Made || r.Line != "9.0" || boots(cmd) != 1 {
		t.Fatalf("first: %+v %v", r, cmd.Texts())
	}
	if b, err := fs.ReadFile(spec.Prefix + "/" + WinePrefixStamp); err != nil || strings.TrimSpace(string(b)) != "9.0" {
		t.Fatalf("stamp %q %v", b, err)
	}
	// wineboot writes system.reg; the fake does not, so write it.
	_ = fs.WriteFile(spec.Prefix+"/system.reg", []byte("x"), 0o644)

	// Same line, also after a patch release: kept, no boot.
	cmd = wineCmd("wine-9.0.2")
	if r = EnsureWinePrefix(ctx, cmd, fs, spec); !r.Skipped || boots(cmd) != 0 {
		t.Fatalf("same line: %+v %v", r, cmd.Texts())
	}

	// New line: removed and made again, stamp updated.
	cmd = wineCmd("wine-10.0")
	if r = EnsureWinePrefix(ctx, cmd, fs, spec); !r.Made || boots(cmd) != 1 {
		t.Fatalf("new line: %+v %v", r, cmd.Texts())
	}
	if b, _ := fs.ReadFile(spec.Prefix + "/" + WinePrefixStamp); strings.TrimSpace(string(b)) != "10.0" {
		t.Errorf("stamp %q", b)
	}
	if _, err := fs.ReadFile(spec.Prefix + "/system.reg"); err == nil {
		t.Error("the old prefix was kept")
	}
}

// A prefix with no stamp has an unknown line: rebuilt once. A failed boot
// writes no stamp, so the next job tries again. A bad version never stops the job.
func TestWineStampMissingAndFailures(t *testing.T) {
	ctx := context.Background()
	spec := WinePrefixSpec{Prefix: "/w", JobUser: "runner", Layers: []string{"windows-packaging"}}
	fs := sysfake.NewFS()
	_ = fs.WriteFile("/w/system.reg", []byte("x"), 0o644)
	cmd := wineCmd("wine-9.0")
	if r := EnsureWinePrefix(ctx, cmd, fs, spec); !r.Made || boots(cmd) != 1 {
		t.Errorf("no stamp: %+v", r)
	}

	fs = sysfake.NewFS()
	cmd = wineCmd("wine-9.0")
	cmd.Rules = append([]sysfake.Rule{{Match: "wineboot", Result: sys.Result{ExitCode: 1}}}, cmd.Rules...)
	if r := EnsureWinePrefix(ctx, cmd, fs, spec); r.Made || r.Problem == "" || fs.Exists("/w/"+WinePrefixStamp) {
		t.Errorf("failed boot: %+v", r)
	}
	for _, v := range []string{"", "garbage"} {
		if r := EnsureWinePrefix(ctx, wineCmd(v), sysfake.NewFS(), spec); r.Made || r.Problem == "" {
			t.Errorf("version %q: %+v", v, r)
		}
	}
	bad := &sysfake.Commands{Rules: []sysfake.Rule{{Match: "wine --version", Err: errors.New("no wine")}}}
	if r := EnsureWinePrefix(ctx, bad, sysfake.NewFS(), spec); r.Problem == "" {
		t.Errorf("version error: %+v", r)
	}
}

// TestLanePrefixNeverSharedAndEnv: a prefix is inside its lane; the job sees it
// under both names and never TMPDIR.
func TestLanePrefixNeverSharedAndEnv(t *testing.T) {
	a := runnercache.LaneFor("/cache", "repo-a", 1)
	b := runnercache.LaneFor("/cache", "repo-b", 1)
	if LaneWinePrefix(a) == LaneWinePrefix(b) || !strings.HasPrefix(LaneWinePrefix(a), a.Root) {
		t.Errorf("%q %q", LaneWinePrefix(a), LaneWinePrefix(b))
	}
	env := LaneWineJobEnv()
	if env["WINEPREFIX"] != LaneInside.Wine || env["POOL_WINE_PREFIX"] != LaneInside.Wine || len(env) != 2 {
		t.Errorf("env %v", env)
	}
}
