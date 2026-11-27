package tests

import (
	"testing"
	"time"
)

// TestSameIdTwoJobs: R5.36, the same test id in two job keys of one run.
func TestSameIdTwoJobs(t *testing.T) {
	rows := []Row{
		row("dup", "unit", 1, 1, "s", Passed, 3*time.Minute),
		row("dup", "coverage", 1, 1, "s", Passed, 4*time.Minute),
		row("once", "unit", 1, 1, "s", Passed, time.Second),
		// a matrix of the same job is one job key
		row("matrix", "unit (linux)", 1, 1, "s", Passed, time.Second),
		row("matrix", "unit (mac)", 1, 1, "s", Passed, time.Second),
		// two runs: not the same run
		row("tworuns", "unit", 1, 1, "s", Passed, time.Second),
		row("tworuns", "unit", 2, 1, "s", Passed, time.Second),
		row("dup", "unit", 2, 1, "s", Skipped, 0),
	}
	got := DuplicateTests(rows)
	if len(got) != 1 || got[0].TestID != "dup" {
		t.Fatalf("%+v", got)
	}
	d := got[0]
	if d.Extra != 3*time.Minute || d.Runs != 1 || len(d.JobIdents) != 2 || d.JobIdents[0] != "coverage" {
		t.Fatalf("%+v", d)
	}
}
