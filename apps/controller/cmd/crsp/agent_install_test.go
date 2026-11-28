package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentinstallers"
)

type recAgentExec struct{ cmds [][]string }

func (r *recAgentExec) Run(_ context.Context, argv []string) error {
	r.cmds = append(r.cmds, argv)
	return nil
}
func (r *recAgentExec) WriteFile(context.Context, agent.File) error { return nil }

var installArgs = []string{"--controller", "pool.lan:8461", "--fingerprint", strings.Repeat("ab", 32), "--join-code", "C"}

// The default data folder is the machine-wide one of each OS, never the user's profile.
func TestAgentDataDirDefaultsToTheMachineFolder(t *testing.T) {
	want := map[string]string{"windows": `C:\ProgramData\crsp-agent`, "darwin": "/Library/Application Support/crsp-agent", "linux": "/var/lib/crsp-agent"}
	for goos, dir := range want {
		for _, cmd := range []string{"run", "join", "install"} {
			_, f, err := parseAgentFlagsFor(goos, cmd, installArgs)
			if err != nil || f.dataDir != dir {
				t.Fatalf("%s %s: %q %v", goos, cmd, f.dataDir, err)
			}
		}
		_, f, _ := parseAgentFlagsFor(goos, "install", append([]string{"--data-dir", "/custom"}, installArgs...))
		if f.dataDir != "/custom" {
			t.Fatalf("%s: explicit --data-dir lost: %q", goos, f.dataDir)
		}
	}
}

func TestInstallNotElevatedPrintsNothingAndRunsNothing(t *testing.T) {
	ex := &recAgentExec{}
	var out strings.Builder
	err := installWith(installArgs, &out, strings.NewReader("yes\n"), "windows", false, ex)
	if !errors.Is(err, agent.ErrNotElevated) || out.Len() != 0 || len(ex.cmds) != 0 {
		t.Fatalf("err=%v out=%q ran=%v", err, out.String(), ex.cmds)
	}
}

func TestInstallElevatedUsesTheMachineFolder(t *testing.T) {
	for goos, dir := range map[string]string{"windows": `C:\ProgramData\crsp-agent`, "linux": "/var/lib/crsp-agent", "darwin": "/Library/Application Support/crsp-agent"} {
		ex := &recAgentExec{}
		if err := installWith(installArgs, io.Discard, strings.NewReader("yes\n"), goos, true, ex); err != nil {
			t.Fatalf("%s: %v", goos, err)
		}
		var joined string
		for _, c := range ex.cmds {
			if len(c) > 2 && c[1] == "agent" && c[2] == "join" {
				joined = strings.Join(c, " ")
			}
		}
		if !strings.Contains(joined, "--data-dir "+dir) {
			t.Errorf("%s: join does not use %s: %q", goos, dir, joined)
		}
	}
}

// CRSP123: --yes answers the plan without stdin (an SSH join has none).
func TestInstallYesNeedsNoStdin(t *testing.T) {
	ex := &recAgentExec{}
	var out strings.Builder
	args := append(append([]string{}, installArgs...), "--yes")
	if err := installWith(args, &out, strings.NewReader(""), "windows", true, ex); err != nil {
		t.Fatal(err)
	}
	if len(ex.cmds) == 0 {
		t.Fatal("the plan did not run")
	}
	ex2 := &recAgentExec{}
	if err := installWith(installArgs, io.Discard, strings.NewReader(""), "windows", true, ex2); err == nil || len(ex2.cmds) != 0 {
		t.Fatalf("without --yes and without an answer nothing runs: %v", err)
	}
}

// CRSP347: a development build has no agent installer store, and that stays a nil interface (the API answers 501).
func TestAgentInstallerServiceStaysNilWithoutAStore(t *testing.T) {
	if agentInstallerService(nil) != nil || agentInstallerService(&machinesRuntime{}) != nil {
		t.Fatal("a missing store must be a nil interface")
	}
	if agentInstallerService(&machinesRuntime{AgentInstallers: &agentinstallers.Store{}}) == nil {
		t.Fatal("a store must reach the API")
	}
}
