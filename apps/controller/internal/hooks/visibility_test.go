package hooks_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/hooks"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

func deadEnv(t *testing.T, out *bytes.Buffer, dir string) hooks.Env {
	t.Helper()
	env := map[string]string{
		hooks.SocketEnv: filepath.Join(t.TempDir(), "none.sock"), hooks.HookDirEnv: dir,
		"GITHUB_REPOSITORY": "o/r", "GITHUB_RUN_ID": "9", "GITHUB_JOB": "build",
	}
	return hooks.Env{Getenv: func(k string) string { return env[k] }, Stdout: out, Stderr: out,
		Measure: func() load.Reading { return load.Reading{MemoryPct: 10, MemoryKnown: true, CPUPct: 10, CPUKnown: true} }}
}

// R5.61: an unreachable agent keeps the job going, and says so on the job and in the runner folder.
func TestUnreachableAgentIsAnnotatedAndRecorded(t *testing.T) {
	for _, hook := range []string{"job-started", "job-completed"} {
		dir := t.TempDir()
		var out bytes.Buffer
		if code := hooks.Run(context.Background(), []string{hook}, deadEnv(t, &out, dir)); code != 0 {
			t.Fatalf("%s exit %d\n%s", hook, code, out.String())
		}
		var note string
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(l, "::warning title=crsp::") { // at the start of the line, or GitHub ignores it
				note = l
			}
		}
		if !strings.Contains(note, "could not reach its agent for "+hook) || !strings.Contains(note, "crsp doctor --check runner.hooks") {
			t.Fatalf("%s: no annotation in\n%s", hook, out.String())
		}
		got := hooks.ReadProblems(dir)
		if len(got) != 1 || !strings.HasPrefix(got[0], hook+": crsp could not reach its agent") {
			t.Fatalf("%s: problems %q", hook, got)
		}
	}
}

// R5.61: a hook that works again clears the record.
func TestHealthyHookClearsTheRecord(t *testing.T) {
	w := newWorld(t)
	dir := t.TempDir()
	hooks.RecordProblem(dir, "job-started", "crsp is not installed")
	var out bytes.Buffer
	e := deadEnv(t, &out, dir)
	e.Client = agentOf(w)
	if code := hooks.Run(context.Background(), []string{"job-started"}, e); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
	if p := hooks.ReadProblems(dir); len(p) != 0 || strings.Contains(out.String(), "::warning") {
		t.Fatalf("problems %q output %s", p, out.String())
	}
}

func TestAnnotationEscapesLines(t *testing.T) {
	if got := hooks.Annotation("a\nb%"); got != "::warning title=crsp::a%0Ab%25" {
		t.Fatal(got)
	}
}

// Doctor R5.29: probe mode answers at once and takes no slot.
func TestProbeAnswersWithoutTheGate(t *testing.T) {
	var out bytes.Buffer
	e := deadEnv(t, &out, "")
	getenv := e.Getenv
	e.Getenv = func(k string) string {
		if k == hooks.ProbeEnv {
			return "1"
		}
		return getenv(k)
	}
	if code := hooks.Run(context.Background(), []string{"job-started"}, e); code != 0 || strings.TrimSpace(out.String()) != hooks.ProbeOK+"\n"+hooks.NonInteractiveOK {
		t.Fatalf("exit %d output %q", code, out.String())
	}
}

func agentOf(w *world) hooks.Client { return agent{w.eng, slots.Defaults()} }
