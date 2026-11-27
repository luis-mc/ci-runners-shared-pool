package container

import (
	"context"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
)

type oomEngine struct {
	inspect, events string
	calls           []string
}

func (e *oomEngine) Detect(context.Context) (Status, error) {
	return Status{Present: true, Usable: true}, nil
}
func (e *oomEngine) Command(args ...string) (sys.Command, error) {
	return sys.Command{Name: "docker", Args: args}, nil
}
func (e *oomEngine) Run(_ context.Context, args ...string) (sys.Result, error) {
	e.calls = append(e.calls, strings.Join(args, " "))
	switch args[0] {
	case "inspect":
		return sys.Result{Stdout: e.inspect}, nil
	case "exec":
		if args[2] == "cat" {
			return sys.Result{Stdout: e.events}, nil
		}
		return sys.Result{Stdout: "runner line\n"}, nil
	}
	return sys.Result{}, nil
}

func oomBackend(e *oomEngine, logs *[]string) *Backend {
	return New(sys.Deps{LogLine: func(step, msg string) { *logs = append(*logs, step+": "+msg) }}, e, Config{})
}

func TestExitTextNames137(t *testing.T) {
	if got := ExitText(137); got != "stopped by the memory cap (exit 137)" {
		t.Errorf("got %q", got)
	}
	if ExitText(1) != "" || ExitText(0) != "" {
		t.Error("other exit codes need no words")
	}
}

func TestParseOOMKill(t *testing.T) {
	if n := ParseOOMKill("low 0\nhigh 0\nmax 3\noom 2\noom_kill 2\n"); n != 2 {
		t.Errorf("got %d", n)
	}
	if n := ParseOOMKill("low 0\n"); n != -1 {
		t.Errorf("got %d", n)
	}
}

func TestStreamLogsReportsMemoryCapWithCap(t *testing.T) {
	var logs []string
	e := &oomEngine{inspect: "true 137 8589934592\n", events: "oom_kill 1\n"}
	b := oomBackend(e, &logs)
	out := make(chan backend.LogLine, 16)
	if err := b.StreamLogs(context.Background(), backend.Handle{Ref: "pool:r1"}, out); err != nil {
		t.Fatal(err)
	}
	close(out)
	var last backend.LogLine
	for l := range out {
		last = l
	}
	if last.Level != "error" || !strings.Contains(last.Message, "stopped by the memory cap (exit 137)") ||
		!strings.Contains(last.Message, "memory cap 8192 MiB") {
		t.Errorf("job log line %+v", last)
	}
	if len(logs) != 1 || !strings.HasPrefix(logs[0], "oom: ") || !strings.Contains(logs[0], "8192 MiB") {
		t.Errorf("event log %v", logs)
	}
}

func TestStreamLogsQuietWithoutOOM(t *testing.T) {
	var logs []string
	b := oomBackend(&oomEngine{inspect: "false 0 0\n", events: "oom_kill 0\n"}, &logs)
	out := make(chan backend.LogLine, 16)
	if err := b.StreamLogs(context.Background(), backend.Handle{Ref: "pool:r1"}, out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || len(logs) != 0 {
		t.Errorf("lines %d, log %v", len(out), logs)
	}
}

func TestReportUsageStoppedByMemoryCap(t *testing.T) {
	var logs []string
	b := oomBackend(&oomEngine{inspect: "true 137 1073741824\n", events: "oom_kill 1\n"}, &logs)
	u, err := b.ReportUsage(context.Background(), backend.Handle{Ref: "pool:r1"})
	if err != nil || u.StoppedBy != MemoryCapText {
		t.Errorf("usage %+v err %v", u, err)
	}
	b = oomBackend(&oomEngine{inspect: "false 0 0\n", events: "oom_kill 0\n"}, &logs)
	if u, _ = b.ReportUsage(context.Background(), backend.Handle{Ref: "pool:r1"}); u.StoppedBy != "" {
		t.Errorf("usage %+v", u)
	}
}
