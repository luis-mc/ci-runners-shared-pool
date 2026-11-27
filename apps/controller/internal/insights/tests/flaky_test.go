package tests

import (
	"testing"
	"time"
)

// TestSameCommitDifferentResult: R5.35, same job key and commit, different results across attempts.
func TestSameCommitDifferentResult(t *testing.T) {
	rows := []Row{
		row("flaky", "unit", 10, 1, "abc", Failed, 2*time.Second),
		row("flaky", "unit", 10, 2, "abc", Passed, 3*time.Second),
		row("steady", "unit", 10, 1, "abc", Passed, time.Second),
		row("steady", "unit", 10, 2, "abc", Passed, time.Second),
		// Different commits are not the same commit: a fix, not a flake.
		row("fixed", "unit", 11, 1, "c1", Failed, time.Second),
		row("fixed", "unit", 12, 1, "c2", Passed, time.Second),
		// Different job keys are not compared with each other.
		row("twojobs", "unit", 13, 1, "abc", Failed, time.Second),
		row("twojobs", "e2e", 13, 1, "abc", Passed, time.Second),
		// Skipped is no result.
		row("skip", "unit", 14, 1, "abc", Passed, time.Second),
		row("skip", "unit", 14, 2, "abc", Skipped, 0),
	}
	got := FlakyTests(rows)
	if len(got) != 1 || got[0].TestID != "flaky" || got[0].Groups != 1 || len(got[0].Runs) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Rerun != 3*time.Second {
		t.Errorf("rerun = %s", got[0].Rerun)
	}
}

func TestPassedAfterRetryInsideOneJob(t *testing.T) {
	r := row("retried", "unit", 1, 1, "abc", Passed, 2*time.Second)
	r.Retries = 2
	failed := row("failing", "unit", 1, 1, "abc", Failed, time.Second)
	failed.Retries = 2 // failed after retries: a real failure
	got := FlakyTests([]Row{r, failed})
	if len(got) != 1 || got[0].TestID != "retried" || got[0].Rerun != 4*time.Second {
		t.Fatalf("%+v", got)
	}
}

func TestAllFailedIsNotFlaky(t *testing.T) {
	rows := []Row{row("broken", "unit", 1, 1, "abc", Failed, time.Second), row("broken", "unit", 1, 2, "abc", Error, time.Second)}
	if got := FlakyTests(rows); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
