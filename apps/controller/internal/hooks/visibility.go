package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A crsp that is missing, broken or cannot reach its agent never fails a job
// (R5.23), but it is never silent (R5.61): the job gets a `::warning`
// annotation, the runner folder gets a problem file the doctor reads, and the
// controller is told when a job ends that never started through the gate.
const (
	// ProblemFile is written next to the hook scripts by the scripts and by
	// crsp itself, and read by the agent for the doctor (doctor R5.14, R5.29).
	ProblemFile = "crsp-hook-problem.txt"
	// HookDirEnv is the folder of the hook scripts; the scripts set it for crsp.
	HookDirEnv = "CRSP_HOOK_DIR"
	// ProbeEnv set to 1 makes `crsp hook` answer ProbeOK and exit 0 at once,
	// so the doctor can run the real hook as the service account (doctor R5.29).
	ProbeEnv = "CRSP_HOOK_PROBE"
	// ProbeOK is what a working hook prints in probe mode.
	ProbeOK = "crsp hook ok"
)

// Annotation is the GitHub Actions workflow command that puts msg on the job.
func Annotation(msg string) string {
	r := strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	return "::warning title=crsp::" + r.Replace(msg)
}

// RecordProblem writes one problem line for hook into dir. It never fails the hook.
func RecordProblem(dir, hook, msg string) {
	if dir == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, ProblemFile), []byte(hook+": "+msg+"\n"), 0o644)
}

// ClearProblem removes the problem file once crsp has worked again.
func ClearProblem(dir string) {
	if dir != "" {
		_ = os.Remove(filepath.Join(dir, ProblemFile))
	}
}

// ReadProblems returns the problem lines in dir, for the agent to hand to the
// doctor as Runner.HookProblems. No file means no problem.
func ReadProblems(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, ProblemFile))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// unreachableText is the annotation when crsp runs but its agent does not answer.
func unreachableText(hook string, err error) string {
	return fmt.Sprintf("crsp could not reach its agent for %s (%v), so this job ran without the queue and the load gate. "+
		"Start the crsp agent on this machine, then run 'crsp doctor --check runner.hooks'. The job was not stopped.", hook, err)
}

// report prints the annotation and records the problem.
func (e Env) reporter(hook string) (report func(string), healthy func()) {
	dir := e.Getenv(HookDirEnv)
	return func(msg string) {
			_, _ = fmt.Fprintln(e.Stdout, Annotation(msg))
			RecordProblem(dir, hook, msg)
		}, func() {
			ClearProblem(dir)
		}
}
