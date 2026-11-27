package agent_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// fakeRunners is the agent's runner backend: it records what it was asked.
type fakeRunners struct {
	mu      sync.Mutex
	jit     string
	removal string
	calls   []string
	failAdd error
}

func (f *fakeRunners) AddRunner(_ context.Context, _ string, spec backend.RunnerSpec, cfg backend.StartConfig) (backend.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jit = cfg.JITConfig
	f.calls = append(f.calls, "add:"+spec.Name)
	if f.failAdd != nil {
		return backend.Handle{}, f.failAdd
	}
	return backend.Handle{RunnerID: spec.ID, Ref: "pool:" + spec.Name}, nil
}
func (f *fakeRunners) StopService(_ context.Context, _, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "stop:"+s)
	return nil
}
func (f *fakeRunners) RemoveRunner(_ context.Context, _ string, r runners.RemoveRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removal = r.RemoveToken
	f.calls = append(f.calls, "remove:"+r.ServiceName)
	return errors.New("deregister refused with " + r.RemoveToken)
}
func (f *fakeRunners) CleanUp(_ context.Context, _, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "cleanup:"+s)
	return nil
}

// startLinked joins one agent to a controller whose link carries the runner
// commands, and returns the controller's side of the link.
func startLinked(t *testing.T, fr *fakeRunners, logs *bytes.Buffer) (*agentlink.Link, string) {
	t.Helper()
	c := startController(t)
	lk := agentlink.New(c.Svc)
	lk.Timeout = 5 * time.Second
	c.Svc.SetHooks(machines.Hooks{
		OnHello: func(_ context.Context, id string, _ *agentv1.Hello) { lk.OnHello(id) },
		OnMessage: func(_ context.Context, id string, m *agentv1.SessionRequest, _ func(*agentv1.SessionResponse)) bool {
			return lk.OnMessage(id, m)
		},
	})
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Runners = fr
	cfg.Redactor = redact.New()
	cfg.Logger = slog.New(slog.NewJSONHandler(logs, nil))
	runAgent(t, cfg)
	var id string
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online && c.Svc.Connected(vs[0].Machine.ID) {
			id = vs[0].Machine.ID
			return true
		}
		return false
	})
	return lk, id
}

// A JIT runner starts on a remote machine through the link; the answer carries
// the service ref, and the config reaches the backend and no log (runners R5.11).
func TestAddJITRunnerOverTheLink(t *testing.T) {
	fr := &fakeRunners{}
	var logs bytes.Buffer
	lk, id := startLinked(t, fr, &logs)
	jit := "JITCONFIG" + "-payload-0123456789abcdef"
	h, err := (&agentlink.Agent{L: lk}).AddRunner(context.Background(), id,
		backend.RunnerSpec{ID: "r_1", Name: "crsp-job-7", OS: "linux", Arch: "x64", Labels: []string{"self-hosted", "linux"}}, backend.StartConfig{JITConfig: jit})
	if err != nil {
		t.Fatal(err)
	}
	if h.Ref != "pool:crsp-job-7" || h.RunnerID != "r_1" {
		t.Fatalf("handle = %+v", h)
	}
	if fr.jit != jit {
		t.Fatalf("the backend got %q", fr.jit)
	}
	if strings.Contains(logs.String(), jit) {
		t.Fatal("the JIT config is in the agent log")
	}
}

// A failed add is reported as failed, and the error holds no config.
func TestFailedAddIsTruthfulAndRedacted(t *testing.T) {
	jit := "JITCONFIG" + "-payload-fedcba9876543210"
	fr := &fakeRunners{failAdd: errors.New("start failed for " + jit)}
	var logs bytes.Buffer
	lk, id := startLinked(t, fr, &logs)
	_, err := (&agentlink.Agent{L: lk}).AddRunner(context.Background(), id,
		backend.RunnerSpec{ID: "r_2", Name: "crsp-job-8"}, backend.StartConfig{JITConfig: jit})
	if err == nil {
		t.Fatal("a failed add was reported as done")
	}
	if strings.Contains(err.Error(), jit) || !strings.Contains(err.Error(), redact.Mask) {
		t.Fatalf("error = %q", err)
	}
}

// Stop, clean up and remove reach the backend; a refused remove says so.
func TestStopCleanUpRemoveOverTheLink(t *testing.T) {
	fr := &fakeRunners{}
	var logs bytes.Buffer
	lk, id := startLinked(t, fr, &logs)
	a := &agentlink.Agent{L: lk}
	ctx := context.Background()
	if err := a.StopService(ctx, id, "pool:a"); err != nil {
		t.Fatal(err)
	}
	if err := a.CleanUp(ctx, id, "pool:a"); err != nil {
		t.Fatal(err)
	}
	rm := "REMOVAL" + "-value-abcdef123456"
	err := a.RemoveRunner(ctx, id, runners.RemoveRequest{RunnerID: "r_3", ServiceName: "svc", RemoveToken: rm})
	if err == nil || strings.Contains(err.Error(), rm) {
		t.Fatalf("remove error = %v", err)
	}
	if got := strings.Join(fr.calls, ","); got != "stop:pool:a,cleanup:pool:a,remove:svc" {
		t.Fatalf("calls = %s", got)
	}
	if err := a.DrainRunner(ctx, id, "r_3"); err == nil {
		t.Fatal("draining one runner claimed success though it is not built")
	}
}

// The agent reports KVM, engine and images after Hello.
func TestMachineReportArrivesAfterHello(t *testing.T) {
	var logs bytes.Buffer
	lk, id := startLinked(t, &fakeRunners{}, &logs)
	waitFor(t, "machine report", func() bool { _, ok := lk.Reports()[id]; return ok })
	r := lk.Reports()[id]
	if r.GetKvm() == nil || r.GetEngine() == nil || !r.GetKvm().GetDeviceKnown() {
		t.Fatalf("report = %+v", r.MachineReport)
	}
}

// A command to a machine with no stream fails at once.
func TestCommandToOfflineMachineFails(t *testing.T) {
	c := startController(t)
	lk := agentlink.New(c.Svc)
	err := (&agentlink.Agent{L: lk}).StopService(context.Background(), "m_none", "x")
	if err == nil {
		t.Fatal("no error for an offline machine")
	}
}
