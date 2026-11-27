package container

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

// CRSP127, runners R5.47: on a Windows machine the Linux container runner is
// launched through the Windows docker command, and the run arguments keep the
// same rules as on Linux: no --privileged, no socket, non-root.
func TestWindowsHostKeepsTheRunArgsRules(t *testing.T) {
	r := newRig(t, func(c *Config) { c.CrspHostPath = `C:\Program Files\crsp\crsp-linux` })
	r.start("job-1", "demo", r.image())
	pool := r.pool("demo")
	args := strings.Join(pool.args, " ")
	for _, bad := range []string{"--privileged", "docker.sock", "docker_engine", "--cap-add", "--user 0", "--user root", " --pid host"} {
		if strings.Contains(args, bad) {
			t.Errorf("run args hold %q: %s", bad, args)
		}
	}
	for _, want := range []string{"--user 1001:1001", "--cap-drop ALL", "--security-opt no-new-privileges", "--read-only",
		"source=C:\\Program Files\\crsp,target=/opt/crsp-bin,readonly", "CRSP_BIN=/opt/crsp-bin/crsp-linux"} {
		if !strings.Contains(args, want) {
			t.Errorf("run args miss %q: %s", want, args)
		}
	}
	if err := CheckRunArgs(pool.args); err != nil {
		t.Fatalf("the guard refuses the Windows host args: %v", err)
	}
	// A Windows path of the engine socket is still refused.
	if err := CheckRunArgs(append(append([]string{}, pool.args...), "--mount", `type=bind,source=\\.\pipe\docker_engine,target=/x`)); err == nil {
		t.Fatal("the Windows engine pipe was accepted")
	}
}

func TestWindowsEngineRunsDockerThroughTheWindowsCLI(t *testing.T) {
	c := &recorder{}
	d := &Docker{Cmd: c, GOOS: "windows"}
	st, err := d.Detect(context.Background())
	if err != nil || !st.Usable || st.Route != RouteWindowsCLI {
		t.Fatalf("%+v %v", st, err)
	}
	cmd, err := d.Command("run", "-d", "img")
	if err != nil || cmd.Name != "docker" || cmd.Args[0] != "run" {
		t.Fatalf("%+v %v", cmd, err)
	}
	for _, x := range c.seen {
		if strings.HasPrefix(x, "wsl docker") {
			t.Errorf("wsl docker asked: %s", x)
		}
	}
}

type recorder struct{ seen []string }

func (r *recorder) Run(_ context.Context, c sys.Command) (sys.Result, error) {
	r.seen = append(r.seen, c.Name+" "+strings.Join(c.Args, " "))
	if c.Name == "docker" {
		return sys.Result{Stdout: "29.9.3\n"}, nil
	}
	return sys.Result{}, nil
}
