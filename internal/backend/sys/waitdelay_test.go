package sys

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// helperEnv makes the test binary act as a parent that leaves a child holding
// its output pipes, the way Runner.Listener could outlive PowerShell (OPS96).
const helperEnv = "CRSP_SYS_PIPE_HELPER"

// TestPipeHelperProcess is not a test: it is the program the tests below run.
func TestPipeHelperProcess(t *testing.T) {
	switch os.Getenv(helperEnv) {
	case "child":
		time.Sleep(60 * time.Second)
		os.Exit(0)
	case "parent-exit", "parent-hang":
		c := exec.Command(os.Args[0], "-test.run=^TestPipeHelperProcess$") //nolint:gosec // the test binary itself.
		c.Env = append(os.Environ(), helperEnv+"=child")
		c.Stdout, c.Stderr = os.Stdout, os.Stderr // the child inherits the pipes
		if err := c.Start(); err != nil {
			fmt.Println("start child:", err)
			os.Exit(2)
		}
		fmt.Printf("child-pid %d\n", c.Process.Pid)
		if os.Getenv(helperEnv) == "parent-hang" {
			time.Sleep(60 * time.Second)
		}
		os.Exit(0)
	}
}

func runHelper(t *testing.T, ctx context.Context, mode string) (Result, error, time.Duration) {
	t.Helper()
	old := PipeWaitDelay
	PipeWaitDelay = time.Second
	t.Cleanup(func() { PipeWaitDelay = old })
	begin := time.Now()
	res, err := ExecCommands{}.Run(ctx, Command{
		Name: os.Args[0],
		Args: []string{"-test.run=^TestPipeHelperProcess$"},
		Env:  map[string]string{helperEnv: mode},
	})
	took := time.Since(begin)
	killChild(t, res.Stdout)
	return res, err, took
}

func killChild(t *testing.T, out string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "child-pid "); ok {
			if pid, err := strconv.Atoi(v); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	}
}

// OPS96: a program that ends but leaves a child holding its output pipes no
// longer blocks Run until that child ends; its own exit code is the result.
func TestRunReturnsWhenChildKeepsPipes(t *testing.T) {
	res, err, took := runHelper(t, context.Background(), "parent-exit")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("err %v, result %+v", err, res)
	}
	if !strings.Contains(res.Stdout, "child-pid ") {
		t.Fatalf("output read before the end is kept: %q", res.Stdout)
	}
	if took > 30*time.Second {
		t.Fatalf("Run waited %s for the leftover child", took)
	}
}

// OPS96: when ctx ends, the program is killed and Run returns even though a
// child it started still holds the output pipes.
func TestRunReturnsOnCancelWhenChildKeepsPipes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, took := runHelper(t, ctx, "parent-hang")
	if took > 30*time.Second {
		t.Fatalf("Run waited %s after ctx ended", took)
	}
}
