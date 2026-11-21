package cli

import (
	"strings"
	"testing"
)

func TestScriptsAndOfflineCompletion(t *testing.T) {
	f := newFixture(t)
	f.srv.Close() // no controller: completion scripts need no network (R6.4)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		code, out, _ := f.run("completion", sh)
		if code != 0 || !strings.Contains(out, "crsp") || !strings.Contains(out, "machine") || !strings.Contains(out, "login") {
			t.Errorf("%s: %d %q", sh, code, out)
		}
	}
	if code, _, _ := f.run("completion", "tcsh"); code != ExitUsage {
		t.Error("unknown shell")
	}
	if code, _, _ := f.run("completion"); code != ExitUsage {
		t.Error("no shell")
	}
	// Names: silent and quick when the controller is down.
	code, out, e := f.run("__complete", "machine")
	if code != 0 || out != "" || e != "" {
		t.Fatalf("down: %d %q %q", code, out, e)
	}
	// And they list names when it answers.
	g := newFixture(t)
	g.machines(machineJSON("build-01", "active", true))
	g.repos(repoJSON(10, "luis-mc/app", true))
	if _, out, _ := g.run("__complete", "machine"); out != "build-01\n" {
		t.Fatalf("%q", out)
	}
	if _, out, _ := g.run("__complete", "repo"); out != "luis-mc/app\n" {
		t.Fatalf("%q", out)
	}
	// Not in the help.
	if _, out, _ := g.run("help"); strings.Contains(out, "__complete") {
		t.Fatal("__complete is listed in help")
	}
}

func BenchmarkOfflineCommands(b *testing.B) {
	for i := 0; i < b.N; i++ {
		f := &fixture{}
		var out, errOut strings.Builder
		_ = f
		_ = out
		_ = errOut
		env := &Env{Version: "x", GoVersion: "y"}
		Run(env, []string{"version"})
		Run(env, []string{"help"})
		Run(env, []string{"completion", "bash"})
	}
}
