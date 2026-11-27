package agentlink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

type sendFn func(string, *agentv1.SessionResponse) error

func (f sendFn) Send(id string, m *agentv1.SessionResponse) error { return f(id, m) }

// The message holds the JIT config and the registration; an error from the
// send never repeats it.
func TestOfflineErrorHoldsNoConfig(t *testing.T) {
	jit := "JITCONFIG" + "-value-0123456789"
	l := New(sendFn(func(string, *agentv1.SessionResponse) error { return errors.New("offline") }))
	_, err := (&Agent{L: l}).AddRunner(context.Background(), "m", backend.RunnerSpec{Name: "n"}, backend.StartConfig{JITConfig: jit})
	if err == nil || strings.Contains(err.Error(), jit) {
		t.Fatalf("err = %v", err)
	}
}

func TestNoAnswerTimesOut(t *testing.T) {
	l := New(sendFn(func(string, *agentv1.SessionResponse) error { return nil }))
	l.Timeout = 20 * time.Millisecond
	err := (&Agent{L: l}).StopService(context.Background(), "m", "s")
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("err = %v", err)
	}
	if len(l.pending) != 0 {
		t.Fatal("a command stayed pending")
	}
}

func TestContextEndsTheWait(t *testing.T) {
	l := New(sendFn(func(string, *agentv1.SessionResponse) error { return nil }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Agent{L: l}).CleanUp(ctx, "m", "s"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// A result with ok false is an error, even when outcome says nothing.
func TestResultWithoutOkIsAFailure(t *testing.T) {
	var l *Link
	l = New(sendFn(func(_ string, m *agentv1.SessionResponse) error {
		id := m.GetStopRunner().GetCommandId()
		go l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: &agentv1.RunnerResult{CommandId: id}}})
		return nil
	}))
	if err := (&Agent{L: l}).StopService(context.Background(), "m", "s"); err == nil {
		t.Fatal("a result without ok was taken as done")
	}
}

// cli R5.29: an agent that does not say host_command in its report is never
// sent a host command; one that does gets the argv unchanged.
func TestHostCommandOnlyToAnAgentThatCan(t *testing.T) {
	var l *Link
	var mu sync.Mutex
	var sent *agentv1.RunHostCommand
	l = New(sendFn(func(_ string, m *agentv1.SessionResponse) error {
		c := m.GetRunHostCommand()
		mu.Lock()
		sent = c
		mu.Unlock()
		go l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_RunnerResult{RunnerResult: &agentv1.RunnerResult{
			CommandId: c.GetCommandId(), Ok: false, Outcome: "failed", Error: "the command exited with code 3", ExitCode: 3, Output: "boom"}}})
		return nil
	}))
	if _, err := l.RunHostCommand(context.Background(), "m", []string{"x"}, time.Second); !errors.Is(err, ErrAgentCannotRun) {
		t.Fatalf("older agent: err %v", err)
	}
	l.OnMessage("m", &agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: &agentv1.MachineReport{HostCommand: true}}})
	out, err := l.RunHostCommand(context.Background(), "m", []string{"docker", "system", "df"}, 5*time.Second)
	if err != nil || out.OK || out.ExitCode != 3 || out.Output != "boom" || !strings.Contains(out.Message, "code 3") {
		t.Fatalf("out %+v err %v", out, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := sent.GetArgv(); len(got) != 3 || got[0] != "docker" || got[2] != "df" || sent.GetTimeoutSeconds() != 5 {
		t.Fatalf("sent %v", sent)
	}
}
