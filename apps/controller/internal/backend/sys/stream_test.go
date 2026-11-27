package sys

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// R5.68: ExecCommands hands over each line while the program runs, also a
// carriage-return progress line, and still collects the whole output.
func TestRunStreamGivesLines(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var got []string
	res, err := ExecCommands{}.RunStream(context.Background(),
		Command{Name: "sh", Args: []string{"-c", `printf 'one\ntwo\rthree\n'; echo err >&2; exit 3`}},
		func(l string) { got = append(got, l) })
	if err != nil || res.ExitCode != 3 {
		t.Fatalf("%v %+v", err, res)
	}
	// The two pipes are read at the same time, so "err" can arrive anywhere
	// between the output lines; each stream keeps its own order.
	var out, errs []string
	for _, l := range got {
		if l == "err" {
			errs = append(errs, l)
		} else {
			out = append(out, l)
		}
	}
	if strings.Join(out, "|") != "one|two|three" || len(errs) != 1 {
		t.Fatalf("lines %q", got)
	}
	if res.Stdout != "one\ntwo\rthree\n" || res.Stderr != "err\n" {
		t.Fatalf("whole output kept: %q %q", res.Stdout, res.Stderr)
	}
	var _ StreamCommands = ExecCommands{}
}
