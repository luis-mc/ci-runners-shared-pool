package scheduler_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
)

// R5.27: the scheduler only holds new jobs. No call to a process kill, signal
// or priority API appears in the scheduler, hook or load packages (new guard).
func TestNoProcessSignalled(t *testing.T) {
	banned := []string{".Kill(", "syscall.Kill", ".Signal(", "SIGSTOP", "SIGKILL", "TerminateProcess", "SuspendThread", "setpriority", "Setpriority", "SetPriorityClass", "os.FindProcess"}
	for _, root := range []string{".", "../hooks"} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, bad := range banned {
				if strings.Contains(string(b), bad) {
					t.Errorf("%s calls %s: the scheduler never kills, suspends or signals a process", p, bad)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// R5.54: the follow-up row for a freed slot is inserted in the same transaction as the change.
func TestEnqueueInSameTransaction(t *testing.T) {
	e := newEnv(t)
	goes(t, e.gate(e.req("a", job("o/r", 1, "x"), 1, quiet)))
	before, err := e.st.CountJobs(context.Background(), "queued")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.eng.Release(context.Background(), m1, job("o/r", 1, "x"), nil); err != nil {
		t.Fatal(err)
	}
	after, _ := e.st.CountJobs(context.Background(), "queued")
	if after != before+1 {
		t.Fatalf("job_queue rows %d -> %d, want one wake_gates row", before, after)
	}
	// nothing freed, nothing enqueued
	if _, err := e.eng.Release(context.Background(), m1, job("o/r", 1, "x"), nil); err != nil {
		t.Fatal(err)
	}
	if again, _ := e.st.CountJobs(context.Background(), "queued"); again != after {
		t.Fatalf("a release that freed nothing enqueued work: %d", again)
	}
}

// R6.3: every query runs under a deadline; a cancelled context stops the gate.
func TestQueriesHaveDeadline(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.eng.Gate(ctx, e.req("a", job("o/r", 1, "x"), 1, quiet)); err == nil {
		t.Fatal("a cancelled context did not stop the gate")
	}
	// a context that already expired fails fast too
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := e.eng.Status(ctx, m1); err == nil {
		t.Fatal("an expired context did not stop the query")
	}
	if len(e.status(m1).Slots) != 0 {
		t.Fatal("a stopped gate left a slot")
	}
	_ = scheduler.SlotCI
}
