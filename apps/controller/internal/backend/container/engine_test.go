package container

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys/sysfake"
)

var ctx = context.Background()

func ok(out string) sys.Result  { return sys.Result{Stdout: out} }
func bad(msg string) sys.Result { return sys.Result{ExitCode: 1, Stderr: msg} }

func detect(t *testing.T, goos string, rules ...sysfake.Rule) (Status, *sysfake.Commands, *Docker) {
	t.Helper()
	c := &sysfake.Commands{Rules: rules}
	d := &Docker{Cmd: c, GOOS: goos}
	st, err := d.Detect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st, c, d
}

func TestWindowsCLIFirstThenWsl(t *testing.T) {
	// The Windows CLI answers: wsl docker is never asked.
	st, c, d := detect(t, "windows",
		sysfake.Rule{Match: "wsl --status", Result: ok("Default Version: 2")},
		sysfake.Rule{Match: "docker version", Result: ok("27.3.1\n")})
	if !st.Usable || st.Route != RouteWindowsCLI || st.Version != "27.3.1" || st.WSL2 != "ok" {
		t.Fatalf("status %+v", st)
	}
	for _, x := range c.Texts() {
		if strings.HasPrefix(x, "wsl docker") {
			t.Errorf("wsl docker asked: %s", x)
		}
	}
	cmd, _ := d.Command("ps")
	if cmd.Name != "docker" {
		t.Errorf("command %v", cmd)
	}

	// The CLI cannot run (not installed): wsl docker answers.
	c2 := &sysfake.Commands{Rules: []sysfake.Rule{
		{Match: "wsl docker version", Result: ok("26.1.0")},
		{Match: "docker version", Err: errors.New("executable file not found")},
	}}
	d2 := &Docker{Cmd: c2, GOOS: "windows"}
	st, _ = d2.Detect(ctx)
	if !st.Usable || st.Route != RouteWSL || st.Version != "26.1.0" {
		t.Fatalf("status %+v", st)
	}
	cmd, _ = d2.Command("ps", "-a")
	if cmd.Name != "wsl" || strings.Join(cmd.Args, " ") != "docker ps -a" {
		t.Errorf("command %v", cmd)
	}
}

func TestWindowsCLIInstalledButEngineDownFallsBackToWsl(t *testing.T) {
	st, _, _ := detect(t, "windows",
		sysfake.Rule{Match: "wsl docker version", Result: ok("26.1.0")},
		sysfake.Rule{Match: "docker version", Result: bad("cannot connect to the Docker daemon")})
	if !st.Usable || st.Route != RouteWSL {
		t.Fatalf("status %+v", st)
	}
}

func TestWindowsNothingUsableSaysWhy(t *testing.T) {
	st, _, d := detect(t, "windows",
		sysfake.Rule{Match: "wsl", Err: errors.New("not found")},
		sysfake.Rule{Match: "docker", Err: errors.New("not found")})
	if st.Usable || st.Present || st.WSL2 != "missing" || !strings.Contains(st.Reason, ReasonCLIMissing) || !strings.Contains(st.Reason, ReasonWSLMissing) {
		t.Fatalf("status %+v", st)
	}
	if _, err := d.Command("ps"); !errors.Is(err, ErrNoEngine) {
		t.Errorf("err %v", err)
	}
	if _, err := d.Run(ctx, "ps"); !errors.Is(err, ErrNoEngine) {
		t.Errorf("err %v", err)
	}
}

func TestWindowsWslWithoutDocker(t *testing.T) {
	st, _, _ := detect(t, "windows",
		sysfake.Rule{Match: "wsl --status", Result: ok("ok")},
		sysfake.Rule{Match: "wsl docker version", Result: bad("docker: command not found")},
		sysfake.Rule{Match: "docker version", Err: errors.New("not found")})
	if st.Usable || !st.Present || st.Reason != ReasonWSLNoDocker {
		t.Fatalf("status %+v", st)
	}
}

func TestLinuxNeverCallsWsl(t *testing.T) {
	st, c, _ := detect(t, "linux", sysfake.Rule{Match: "docker version", Result: ok("27.0.0")})
	if !st.Usable || st.Route != RouteNative {
		t.Fatalf("status %+v", st)
	}
	for _, x := range c.Texts() {
		if strings.Contains(x, "wsl") {
			t.Errorf("wsl called on linux: %s", x)
		}
	}
	st, _, _ = detect(t, "linux", sysfake.Rule{Match: "docker", Err: errors.New("not found")})
	if st.Usable || st.Reason != ReasonCLIMissing {
		t.Fatalf("status %+v", st)
	}
	st, _, _ = detect(t, "linux", sysfake.Rule{Match: "docker", Result: bad("Cannot connect\nmore")})
	if st.Usable || !st.Present || !strings.Contains(st.Reason, "Cannot connect") || strings.Contains(st.Reason, "more") {
		t.Fatalf("status %+v", st)
	}
}

func TestDetectNeverEditsEngineSettings(t *testing.T) {
	_, c, _ := detect(t, "windows", sysfake.Rule{Match: "docker version", Result: ok("27")})
	for _, x := range c.Texts() {
		if !strings.Contains(x, "version") && !strings.Contains(x, "--status") {
			t.Errorf("unexpected call %s", x)
		}
	}
}

// Docker satisfies Engine; a fake can too.
var _ Engine = (*Docker)(nil)
