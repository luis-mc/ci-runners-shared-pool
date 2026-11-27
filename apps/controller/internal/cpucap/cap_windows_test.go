//go:build windows

package cpucap

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// R5.64: the Job Object carries a hard CPU rate cap that reads back as set.
func TestJobObjectCapReadsBack(t *testing.T) {
	for _, pct := range []int{25, 50, 75, 100} {
		j, err := NewJob(pct)
		if err != nil {
			t.Fatalf("NewJob(%d): %v", pct, err)
		}
		got, err := j.Rate()
		if err != nil || got != pct {
			t.Errorf("rate after NewJob(%d) = %d, %v", pct, got, err)
		}
		if err := j.SetCap(100); err != nil {
			t.Fatal(err)
		}
		if got, err := j.Rate(); err != nil || got != 100 {
			t.Errorf("rate after lifting the cap = %d, %v", got, err)
		}
		_ = j.Close()
	}
}

// R5.64: a process tree is assigned to the job, and a second assign of the same
// tree is not an error.
func TestJobObjectAssignsAProcessTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cmd.exe", "/c", "ping -n 20 127.0.0.1 >nul")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	j, err := NewJob(50)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	n, err := j.AssignTree(uint32(cmd.Process.Pid)) //nolint:gosec // a pid fits
	if err != nil || n < 1 {
		t.Fatalf("AssignTree = %d, %v", n, err)
	}
	if n2, err := j.AssignTree(uint32(cmd.Process.Pid)); err != nil || n2 < 1 { //nolint:gosec // a pid fits
		t.Fatalf("second AssignTree = %d, %v", n2, err)
	}
}
