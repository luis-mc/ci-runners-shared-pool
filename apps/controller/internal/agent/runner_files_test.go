package agent_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/agentlink"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnerswap"
)

type swapHost struct {
	mu    sync.Mutex
	calls []string
}

func (h *swapHost) add(s string) { h.mu.Lock(); h.calls = append(h.calls, s); h.mu.Unlock() }
func (h *swapHost) Stop(_ context.Context, s string) error {
	h.add("stop " + s)
	return nil
}
func (h *swapHost) Start(_ context.Context, s string) error {
	h.add("start " + s)
	return nil
}
func (h *swapHost) log() string { h.mu.Lock(); defer h.mu.Unlock(); return strings.Join(h.calls, ",") }

type fixedBundle struct{ data []byte }

func (f fixedBundle) Bundle(context.Context, string, runners.UpdateRequest) ([]byte, error) {
	return f.data, nil
}

func swapTgz(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string]string{"bin/Runner.Listener": "new", "run.sh": "new-run"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func swapFolder(t *testing.T) string {
	t.Helper()
	f := t.TempDir()
	for p, body := range map[string]string{".crsp.json": `{"name":"r1","runner_version":"2.329.0"}`, "bin/Runner.Listener": "old", "run.sh": "old-run", ".runner": "reg"} {
		full := filepath.Join(f, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func fileIs(t *testing.T, want string, parts ...string) bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(parts...))
	return err == nil && string(b) == want
}

type swapRig struct {
	lk     *agentlink.Link
	id     string
	gate   *agent.Gate
	host   *swapHost
	folder string
	data   []byte
	sha    string
}

// startSwap joins an agent that can replace runner files (rf nil: an agent that
// cannot, like an older one) and returns the controller's Updater side.
func startSwap(t *testing.T, withFiles bool, swapper *runnerswap.Swapper) *swapRig {
	t.Helper()
	c := startController(t)
	lk := agentlink.New(c.Svc)
	lk.Timeout = 10 * time.Second
	c.Svc.SetHooks(machines.Hooks{
		OnHello: func(_ context.Context, id string, _ *agentv1.Hello) { lk.OnHello(id) },
		OnMessage: func(_ context.Context, id string, m *agentv1.SessionRequest, _ func(*agentv1.SessionResponse)) bool {
			return lk.OnMessage(id, m)
		},
	})
	code, _ := c.Svc.CreateJoinCode(context.Background(), "owner", 0)
	cfg := fastConfig(t, c, code.Code)
	cfg.Gate = agent.NewGate(cfg.Sampler)
	cfg.HeartbeatEvery = 2 * time.Second // a slow runner must not drop the link
	r := &swapRig{lk: lk, gate: cfg.Gate, host: &swapHost{}, folder: swapFolder(t), data: swapTgz(t)}
	sum := sha256.Sum256(r.data)
	r.sha = hex.EncodeToString(sum[:])
	if withFiles {
		if swapper == nil {
			swapper = &runnerswap.Swapper{}
		}
		swapper.Host = r.host
		cfg.RunnerFiles = &agent.RunnerFiles{
			Folder:  func(context.Context, string, string) (string, error) { return r.folder, nil },
			Swapper: swapper,
		}
	}
	runAgent(t, cfg)
	waitFor(t, "online", func() bool {
		vs, _ := c.Svc.List(context.Background())
		if len(vs) == 1 && vs[0].Machine.Online && c.Svc.Connected(vs[0].Machine.ID) {
			r.id = vs[0].Machine.ID
			return true
		}
		return false
	})
	waitFor(t, "machine report", func() bool { _, ok := lk.Reports()[r.id]; return ok })
	waitFor(t, "settings", func() bool { _, have := cfg.Gate.Settings(); return have })
	return r
}

func (r *swapRig) req() runners.UpdateRequest {
	return runners.UpdateRequest{RunnerID: "r_1", Name: "r1", ServiceName: "svc1", Version: "2.330.0", SHA256: r.sha}
}

func (r *swapRig) updater(b []byte, drain time.Duration) *agentlink.Updater {
	return &agentlink.Updater{L: r.lk, Bundles: fixedBundle{b}, DrainTimeout: drain}
}

// R5.38: the controller sends the bundle, the agent swaps the files and
// restarts the service, and the registration stays.
func TestRunnerFilesSwapOverTheLink(t *testing.T) {
	r := startSwap(t, true, nil)
	if err := r.updater(r.data, 0).UpdateRunner(t.Context(), r.id, r.req()); err != nil {
		t.Fatal(err)
	}
	if !fileIs(t, "new", r.folder, "bin", "Runner.Listener") || !fileIs(t, "new-run", r.folder, "run.sh") || !fileIs(t, "reg", r.folder, ".runner") {
		t.Fatal("files not swapped, or the registration changed")
	}
	if !fileIs(t, "old", r.folder, runnerswap.PreviousDir, "bin", "Runner.Listener") {
		t.Fatal("the previous version was not kept")
	}
	if r.host.log() != "stop svc1,start svc1" {
		t.Fatalf("service calls: %s", r.host.log())
	}
}

// A bundle that does not match the pinned SHA-256 is refused by the controller
// before anything is sent: no stop, no change.
func TestRunnerFilesChecksumMismatchRefused(t *testing.T) {
	r := startSwap(t, true, nil)
	bad := append([]byte(nil), r.data...)
	bad[len(bad)-1] ^= 0xff
	err := r.updater(bad, 0).UpdateRunner(t.Context(), r.id, r.req())
	if err == nil || !errors.Is(err, runnerswap.ErrChecksum) {
		t.Fatalf("err = %v", err)
	}
	if r.host.log() != "" || !fileIs(t, "old", r.folder, "bin", "Runner.Listener") {
		t.Fatal("something was touched")
	}
}

// A failure after the service stopped puts the previous files back and starts
// the service again; the answer says so.
func TestRunnerFilesFailureMidSwapRollsBack(t *testing.T) {
	var mu sync.Mutex
	n := 0
	r := startSwap(t, true, &runnerswap.Swapper{Rename: func(a, b string) error {
		mu.Lock()
		n++
		k := n
		mu.Unlock()
		if k == 3 {
			return errors.New("disk full")
		}
		return os.Rename(a, b)
	}})
	err := r.updater(r.data, 0).UpdateRunner(t.Context(), r.id, r.req())
	if err == nil || !strings.Contains(err.Error(), "disk full") || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("err = %v", err)
	}
	if !fileIs(t, "old", r.folder, "bin", "Runner.Listener") || !fileIs(t, "old-run", r.folder, "run.sh") {
		t.Fatal("the previous files are not back")
	}
	if !strings.HasSuffix(r.host.log(), "start svc1") {
		t.Fatalf("the runner was not started again: %s", r.host.log())
	}
}

// A running job is never interrupted: the agent holds the runner, sees the job,
// answers `waiting`, stops nothing and changes nothing. Once the job ends the
// same update goes through.
func TestRunnerFilesNeverCutARunningJob(t *testing.T) {
	r := startSwap(t, true, nil)
	jr := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "r1"}
	waitFor(t, "job starts", func() bool { return r.gate.Start("j1", jr, "persistent").Go })
	err := r.updater(r.data, time.Second).UpdateRunner(t.Context(), r.id, r.req())
	if !runners.IsDrainTimeout(err) {
		t.Fatalf("err = %v", err)
	}
	if r.host.log() != "" || !fileIs(t, "old", r.folder, "bin", "Runner.Listener") {
		t.Fatalf("a running job's runner was touched: %s", r.host.log())
	}
	// The hold ends right after the answer is sent (a deferred release in the agent), so the runner takes jobs
	// again within a moment, not in the same instant as the answer reaches the test.
	waitFor(t, "the runner takes jobs again", func() bool {
		return r.gate.Start("j2", machines.JobRef{Repository: "o/r", RunID: 8, Job: "x", RunnerName: "r1"}, "persistent").Go
	})
	r.gate.Finish("j1")
	r.gate.Finish("j2")
	if err := r.updater(r.data, time.Second).UpdateRunner(t.Context(), r.id, r.req()); err != nil {
		t.Fatal(err)
	}
	if !fileIs(t, "new", r.folder, "bin", "Runner.Listener") {
		t.Fatal("not swapped after the job ended")
	}
}

// While the swap waits for a job, a new job of that runner is kept out.
func TestRunnerFilesHoldKeepsNewJobsOut(t *testing.T) {
	r := startSwap(t, true, nil)
	jr := machines.JobRef{Repository: "o/r", RunID: 7, Job: "build", RunnerName: "r1"}
	waitFor(t, "job starts", func() bool { return r.gate.Start("j1", jr, "persistent").Go })
	done := make(chan error, 1)
	go func() { done <- r.updater(r.data, 5*time.Second).UpdateRunner(t.Context(), r.id, r.req()) }()
	waitFor(t, "runner held", func() bool {
		if r.gate.Start("j2", machines.JobRef{Repository: "o/r", RunID: 9, Job: "y", RunnerName: "r1"}, "persistent").Go {
			r.gate.Finish("j2") // not held yet: the probe job must not stay
			return false
		}
		return true
	})
	r.gate.Finish("j1")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !fileIs(t, "new", r.folder, "bin", "Runner.Listener") {
		t.Fatal("not swapped")
	}
}

// An agent without runner file access (an older agent, or one with no backend)
// says so in its report; the controller sends nothing and answers truthfully.
func TestRunnerFilesOlderAgentIsNotSentTheMessage(t *testing.T) {
	r := startSwap(t, false, nil)
	err := r.updater(r.data, 0).UpdateRunner(t.Context(), r.id, r.req())
	if !errors.Is(err, agentlink.ErrAgentCannotSwap) {
		t.Fatalf("err = %v", err)
	}
	if err := r.updater(r.data, 0).UpdateRunner(t.Context(), "m_unknown", r.req()); !errors.Is(err, agentlink.ErrAgentCannotSwap) {
		t.Fatalf("a machine with no report: %v", err)
	}
}
