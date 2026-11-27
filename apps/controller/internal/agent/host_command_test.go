package agent_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/dockercompact"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// TestHostHelperProcess is the "command" the host tests run: this test binary
// again. It prints its argument, then exits or sleeps as the environment says.
func TestHostHelperProcess(t *testing.T) {
	if os.Getenv("CRSP_HOST_HELPER") != "1" {
		t.Skip("helper process only")
	}
	fmt.Println("helper says " + os.Getenv("CRSP_HOST_SAY"))
	if d, err := time.ParseDuration(os.Getenv("CRSP_HOST_SLEEP")); err == nil {
		<-time.After(d) // lives until the parent stops it
	}
	code, _ := strconv.Atoi(os.Getenv("CRSP_HOST_EXIT"))
	os.Exit(code)
}

func helperArgv() []string {
	return []string{os.Args[0], "-test.run=^TestHostHelperProcess$"}
}

type hostRig struct {
	svc  *machines.Service
	lk   *agentlink.Link
	id   string
	gate *agent.Gate
}

func startHost(t *testing.T) *hostRig { return startHostWith(t, nil) }

func startHostWith(t *testing.T, mutate func(*agent.Config)) *hostRig {
	t.Helper()
	c := startController(t)
	lk := agentlink.New(c.Svc)
	lk.Timeout = 10 * time.Second
	c.Svc.SetHostRunner(lk)
	c.Svc.SetHooks(machines.Hooks{
		OnHello: func(_ context.Context, id string, _ *agentv1.Hello) { lk.OnHello(id) },
		OnMessage: func(_ context.Context, id string, m *agentv1.SessionRequest, _ func(*agentv1.SessionResponse)) bool {
			return lk.OnMessage(id, m)
		},
	})
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	cfg.HeartbeatEvery = 2 * time.Second
	if mutate != nil {
		mutate(&cfg)
	}
	r := &hostRig{svc: c.Svc, lk: lk, gate: cfg.Gate}
	runAgent(t, cfg)
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online && c.Svc.Connected(vs[0].Machine.ID) {
			r.id = vs[0].Machine.ID
			return true
		}
		return false
	})
	waitFor(t, "machine report", func() bool { rep, ok := lk.Reports()[r.id]; return ok && rep.GetHostCommand() })
	waitFor(t, "settings", func() bool { _, have := cfg.Gate.Settings(); return have })
	return r
}

func (r *hostRig) drain(t *testing.T) {
	t.Helper()
	if _, err := r.svc.Drain(context.Background(), "owner", r.id, "maintenance"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "drained", func() bool {
		v, _ := r.svc.Get(context.Background(), r.id)
		return v.Machine.State == machines.StateDrained
	})
}

// A drained machine runs the command; the answer holds the output, and the
// machine stays drained (the caller lifts the hold).
func TestHostCommandRunsOnADrainedMachine(t *testing.T) {
	t.Setenv("CRSP_HOST_HELPER", "1")
	t.Setenv("CRSP_HOST_SAY", "compact")
	r := startHost(t)
	r.drain(t)
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, helperArgv(), time.Minute)
	if err != nil || !out.OK || out.ExitCode != 0 || !strings.Contains(out.Output, "helper says compact") {
		t.Fatalf("out %+v err %v", out, err)
	}
	if v, _ := r.svc.Get(context.Background(), r.id); v.Machine.State != machines.StateDrained {
		t.Fatalf("state %s", v.Machine.State)
	}
}

func TestHostCommandFailureKeepsExitCodeAndOutput(t *testing.T) {
	t.Setenv("CRSP_HOST_HELPER", "1")
	t.Setenv("CRSP_HOST_SAY", "disk busy")
	t.Setenv("CRSP_HOST_EXIT", "3")
	r := startHost(t)
	r.drain(t)
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, helperArgv(), time.Minute)
	if err != nil || out.OK || out.ExitCode != 3 || !strings.Contains(out.Output, "disk busy") || !strings.Contains(out.Message, "code 3") {
		t.Fatalf("out %+v err %v", out, err)
	}
}

func TestHostCommandTimeoutStopsTheCommand(t *testing.T) {
	t.Setenv("CRSP_HOST_HELPER", "1")
	t.Setenv("CRSP_HOST_SLEEP", "1m")
	r := startHost(t)
	r.drain(t)
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, helperArgv(), time.Second)
	if err != nil || out.OK || out.ExitCode != -1 || !strings.Contains(out.Message, "was stopped") {
		t.Fatalf("out %+v err %v", out, err)
	}
}

// A machine that still takes jobs, or still waits for one, gets nothing: the
// controller refuses, and an agent asked directly refuses too. The running job
// is not touched.
func TestHostCommandNeverMeetsAJob(t *testing.T) {
	t.Setenv("CRSP_HOST_HELPER", "1")
	r := startHost(t)
	// Active machine: refused by the controller.
	if _, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, helperArgv(), time.Minute); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("active: %v", err)
	}
	// Active machine, straight to the agent: it refuses.
	out, err := r.lk.RunHostCommand(context.Background(), r.id, helperArgv(), time.Minute)
	if err != nil || out.OK || !strings.Contains(out.Message, "drain it first") {
		t.Fatalf("agent on an active machine: %+v %v", out, err)
	}
	// A job runs; the drain waits; the command is refused until the job ends.
	jr := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "r1"}
	waitFor(t, "job starts", func() bool { return r.gate.Start("j1", jr, "persistent").Go })
	if _, err := r.svc.Drain(context.Background(), "owner", r.id, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, helperArgv(), time.Minute); !errors.Is(err, machines.ErrConflict) {
		t.Fatalf("draining: %v", err)
	}
	out, err = r.lk.RunHostCommand(context.Background(), r.id, helperArgv(), time.Minute)
	if err != nil || out.OK || !strings.Contains(out.Message, "1 jobs still run") {
		t.Fatalf("agent with a running job: %+v %v", out, err)
	}
	// The job was not cut.
	if idle, waiting := r.gate.Hold(); idle || len(waiting) != 1 {
		t.Fatalf("the running job is gone: idle=%v waiting=%v", idle, waiting)
	}
}

func TestHostCommandRefusesEmptyAndUnknown(t *testing.T) {
	r := startHost(t)
	r.drain(t)
	if _, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, nil, time.Minute); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, []string{"x"}, 48*time.Hour); !errors.Is(err, machines.ErrInvalid) {
		t.Fatalf("long: %v", err)
	}
	if _, err := r.svc.RunHostCommand(context.Background(), "owner", "m_nope", []string{"x"}, time.Minute); !errors.Is(err, machines.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

// scriptHost is a dockercompact.Host that records calls; nothing real runs.
type scriptHost struct {
	mu    sync.Mutex
	calls []string
	admin bool
	size  int64
}

func (h *scriptHost) GOOS() string   { return "windows" }
func (h *scriptHost) Elevated() bool { return h.admin }
func (h *scriptHost) Run(_ context.Context, _ time.Duration, name string, args ...string) (dockercompact.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	h.calls = append(h.calls, line)
	switch {
	case strings.HasPrefix(line, "wsl -d docker-desktop -e df"):
		return dockercompact.Result{Out: "Filesystem 1B-blocks Used Available Use% Mounted\n/dev/sdc 200 100 100 50% /mnt\n"}, nil
	case strings.HasPrefix(line, "powershell") && strings.Contains(line, "Get-Command"):
		return dockercompact.Result{ExitCode: 3}, nil
	case strings.HasPrefix(line, "diskpart"):
		h.size = 40 << 30
	}
	return dockercompact.Result{}, nil
}
func (h *scriptHost) FileSize(p string) (int64, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.size, strings.HasSuffix(p, "docker_data.vhdx")
}
func (h *scriptHost) Getenv(k string) string {
	if k == "LOCALAPPDATA" {
		return `C:\L`
	}
	return ""
}
func (h *scriptHost) ReadFile(string) (string, error)            { return "", errors.New("none") }
func (h *scriptHost) WriteTemp(string, string) (string, error)   { return `C:\T\s.txt`, nil }
func (h *scriptHost) Remove(string)                              {}
func (h *scriptHost) Sleep(context.Context, time.Duration) error { return nil }
func (h *scriptHost) has(prefix string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// caches R5.51 to R5.54: the docker-compact command runs the helper, not a program of
// that name; the answer carries the RESULT line; the machine report names the
// disk file and the admin right.
func TestHostCommandDockerCompactRunsTheHelper(t *testing.T) {
	fake := &scriptHost{admin: true, size: 236 << 30}
	r := startHostWith(t, func(c *agent.Config) { c.DockerHost = fake })
	waitFor(t, "docker disk report", func() bool {
		rep := r.lk.Reports()[r.id]
		d := rep.GetDockerDisk()
		return d != nil && d.GetPresent() && d.GetCanCompact() && d.GetFileBytes() == 236<<30 && d.GetUsedKnown() && d.GetUsedBytes() == 100
	})
	r.drain(t)
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, []string{"docker-compact"}, time.Minute)
	if err != nil || !out.OK {
		t.Fatalf("out %+v err %v", out, err)
	}
	rep, ok := dockercompact.ParseReport(out.Output)
	if !ok || rep.FileBefore() != 236<<30 || rep.FileAfter() != 40<<30 || !fake.has("wsl --shutdown") || !fake.has("docker desktop start") {
		t.Fatalf("report %+v ok %v calls %v", rep, ok, fake.calls)
	}
}

func TestHostCommandDockerCompactFailureKeepsTheOutput(t *testing.T) {
	fake := &scriptHost{admin: false, size: 10 << 30}
	r := startHostWith(t, func(c *agent.Config) { c.DockerHost = fake })
	r.drain(t)
	out, err := r.svc.RunHostCommand(context.Background(), "owner", r.id, []string{"docker-compact"}, time.Minute)
	if err != nil || out.OK || !strings.Contains(out.Message, "administrator") {
		t.Fatalf("out %+v err %v", out, err)
	}
	if fake.has("docker desktop stop") {
		t.Fatalf("stopped Docker without admin: %v", fake.calls)
	}
}
