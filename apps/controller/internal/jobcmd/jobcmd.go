// Package jobcmd holds the commands a container job calls inside the base
// image: `crsp vm` (caches R5.46, vm.go) and `crsp db-env` (runners R5.54).
// A command that is not built yet fails with NOT YET IMPLEMENTED, so a
// workflow never believes a step that did nothing.
package jobcmd

import (
	"fmt"
	"io"
	"os"
)

// NotYetImplemented is the line a stub prints.
const NotYetImplemented = "NOT YET IMPLEMENTED"

// ExitNotImplemented is the exit code of a stub.
const ExitNotImplemented = 3

// later lists the commands that are not built yet. None is left: `crsp vm` is built.
var later = map[string]string{}

// Handles says whether name is one of the job commands.
func Handles(name string) bool { return name == "db-env" || name == "vm" || later[name] != "" }

// Run runs the job command name with the process environment and returns the
// exit code.
func Run(name string, args []string, stdout, stderr io.Writer) int {
	return RunEnv(name, args, Env{Getenv: os.Getenv, Stdout: stdout, Stderr: stderr})
}

// RunEnv is Run with an injected environment, for tests.
func RunEnv(name string, args []string, env Env) int {
	switch name {
	case "db-env":
		return DBEnv(env)
	case "vm":
		return VM(args, env)
	}
	stderr := env.Stderr
	why, ok := later[name]
	if !ok {
		_, _ = fmt.Fprintf(stderr, "unknown job command %q\n", name)
		return 2
	}
	_, _ = fmt.Fprintf(stderr, "%s: %s\n", NotYetImplemented, why)
	return ExitNotImplemented
}
