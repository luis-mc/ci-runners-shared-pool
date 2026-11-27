package container

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
)

// R5.40, R5.34: the JIT config reaches the runner process on standard input
// only. It is in no argument, no container setting (so not in `docker
// inspect`), no file, and no stored command line.
func TestJitConfigOnlyOnStdin(t *testing.T) {
	r := newRig(t)
	r.start("job-1", "demo", r.image())

	// Once, on the stdin of the start call, nowhere else.
	n := 0
	for _, in := range r.f.stdins {
		if strings.Contains(in, jitSecret) {
			n++
			if strings.TrimSpace(in) != jitSecret {
				t.Fatalf("stdin holds more than the config: %q", in)
			}
		}
	}
	if n != 1 {
		t.Fatalf("the config goes in once, on stdin: %d", n)
	}
	// Not on any command line the engine was given.
	if strings.Contains(r.f.text(), jitSecret) {
		t.Fatalf("the JIT config is on a command line:\n%s", r.f.text())
	}
	// The start call is an interactive exec (stdin attached), not a detached one
	// that would have no stdin, and it sets no environment.
	var start string
	for _, c := range r.f.calls {
		if strings.Contains(c, "IFS= read -r c") {
			start = c
		}
	}
	if start == "" {
		t.Fatal("no start call that reads the config from stdin")
	}
	if !strings.HasPrefix(start, "docker exec -i ") || strings.Contains(start, " -d ") {
		t.Fatalf("the start call must attach stdin: %s", start)
	}
	if strings.Contains(strings.ToLower(start), " -e actions_runner_input_jitconfig") {
		t.Fatalf("the config must not be a container or exec env: %s", start)
	}
	// No file holds it, in any folder of the pool container.
	for p, content := range r.pool("demo").files {
		if strings.Contains(content, jitSecret) {
			t.Fatalf("a file holds the config: %s", p)
		}
	}
	// The script reads one line and writes no file: no redirect that makes a
	// file but the runner log, no `cat >`.
	if strings.Contains(runnerScript, "cat >") || strings.Contains(runnerScript, "umask") {
		t.Fatalf("the start script writes the config to disk: %s", runnerScript)
	}
}

// R5.40: the pool container's creation args (what `docker inspect` shows) have
// no JIT config, no JIT environment and no tmpfs for one.
func TestInspectShowsNoJitConfig(t *testing.T) {
	r := newRig(t)
	r.start("job-1", "demo", r.image())
	args := strings.Join(r.pool("demo").args, " ")
	if strings.Contains(args, jitSecret) || strings.Contains(strings.ToUpper(args), "JITCONFIG") {
		t.Fatalf("the pool container carries a JIT setting:\n%s", args)
	}
	if strings.Contains(args, "--tmpfs") && strings.Contains(args, "/run/crsp") {
		t.Fatalf("a folder for the config is mounted:\n%s", args)
	}
}

// A start with an empty config is refused before any engine call that reads it.
func TestEmptyConfigStartsNothing(t *testing.T) {
	r := newRig(t)
	img := r.image()
	_, err := r.b.Start(t.Context(), r.spec("job-1", "demo", img), backend.StartConfig{})
	if err == nil {
		t.Fatal("an empty config must not start a runner")
	}
}
