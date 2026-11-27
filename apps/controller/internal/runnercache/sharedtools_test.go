package runnercache

import (
	"path"
	"strings"
	"testing"
)

var st = SharedTools{Root: "/var/lib/crsp/tools/r1", Group: "crsp-repo-r1"}

func TestBuildDirIsOwnedByTheRunner(t *testing.T) {
	a, b := st.BuildDir(path.Join, "crsp-a"), st.BuildDir(path.Join, "crsp-b")
	if a != "/var/lib/crsp/tools/r1/cargo-target/crsp-a" || a == b {
		t.Fatalf("%s %s", a, b)
	}
	env := st.Env(path.Join, "crsp-a")
	if env[EnvRustDir] != a || env[EnvCargoDir] != "/var/lib/crsp/tools/r1/cargo-home" ||
		env[EnvRustupDir] != "/var/lib/crsp/tools/r1/rustup-home" || env[EnvXwinCache] != "/var/lib/crsp/tools/r1/xwin" {
		t.Errorf("%v", env)
	}
}

func TestLinuxSharePlanGivesGroupAccessAndDefaultACLs(t *testing.T) {
	cmds, err := LinuxSharePlan(st, "crsp-a")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, c := range cmds {
		lines = append(lines, c.String())
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{
		"mkdir -p -- " + st.Root + "/cargo-target " + st.Root + "/cargo-home " + st.Root + "/rustup-home " + st.Root + "/xwin",
		st.Root + "/cargo-target/crsp-a",
		"chgrp -R crsp-repo-r1 " + st.Root,
		"chmod -R g+rwX " + st.Root,
		"chmod g+s",
		"setfacl -R -m g:crsp-repo-r1:rwX " + st.Root,
		"setfacl -R -d -m g:crsp-repo-r1:rwX " + st.Root, // the default ACL
	} {
		if !strings.Contains(all, want) {
			t.Errorf("plan lacks %q:\n%s", want, all)
		}
	}
}

func TestSharePlansAreOffWithoutRootAndRefuseBadNames(t *testing.T) {
	if c, err := LinuxSharePlan(SharedTools{}, "x"); c != nil || err != nil {
		t.Fatal("off is no plan")
	}
	for _, n := range []string{"", "..", "a/b", `a\b`} {
		if _, err := LinuxSharePlan(st, n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
	if WindowsSharePlan(SharedTools{}) != nil {
		t.Fatal("off is no plan")
	}
}

func TestWindowsSharePlanInheritsModifyForTheGroup(t *testing.T) {
	w := SharedTools{Root: `C:\ProgramData\crsp\tools\r1`, Group: RunnersGroup}
	cmds := WindowsSharePlan(w)
	last := cmds[len(cmds)-1]
	got := last.String()
	if last.Name != "icacls" || !strings.Contains(got, RunnersGroup+":(OI)(CI)(M)") || !strings.Contains(got, "/T") {
		t.Fatalf("%s", got)
	}
}

func TestProbeRunsAsTheRunnerUser(t *testing.T) {
	c := ProbeCommand("crsp-b", "/x/cargo-home")
	if c.Name != "runuser" || c.Args[1] != "crsp-b" || c.Args[len(c.Args)-1] != "/x/cargo-home" {
		t.Fatalf("%v", c)
	}
}

func TestFixCommandsAreTheSamePlan(t *testing.T) {
	if got := FixCommands("linux", st, "crsp-a"); !strings.Contains(got, "setfacl -R -d -m g:crsp-repo-r1:rwX") {
		t.Fatal(got)
	}
	if got := FixCommands("windows", SharedTools{Root: `C:\t`, Group: "crsp-runners"}, "a"); !strings.Contains(got, "icacls") {
		t.Fatal(got)
	}
}
