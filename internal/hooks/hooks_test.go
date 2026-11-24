package hooks_test

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/hooks"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/load"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

const machine = "m1"

// world is a real store and engine, with one clock for the engine and the hook.
type world struct {
	t     *testing.T
	st    *store.Store
	eng   *scheduler.Engine
	mu    sync.Mutex
	now   time.Time
	lines []string
	// running are the jobs that fill() started; they stay up while a gate waits,
	// so the agent's holder report renews their leases (scheduling R5.70).
	running []scheduler.JobRef
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st, err := store.OpenForTests(context.Background(), filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SeedMachine(context.Background(), machine, "linux"); err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, st: st, now: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)}
	w.eng = scheduler.NewEngine(st, scheduler.WithClock(w.clock))
	return w
}

func (w *world) clock() time.Time { w.mu.Lock(); defer w.mu.Unlock(); return w.now }
func (w *world) sleep(_ context.Context, d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	running := append([]scheduler.JobRef(nil), w.running...)
	w.mu.Unlock()
	if len(running) > 0 {
		_ = w.eng.RenewJobs(context.Background(), machine, running)
	}
}

// agent is the in-process agent: the engine plus a hello.
type agent struct {
	*scheduler.Engine
	set slots.Settings
}

func (a agent) Hello(context.Context) (hooks.Hello, error) {
	return hooks.Hello{MachineID: machine, Settings: a.set}, nil
}

func (w *world) cfg(client hooks.Client, gate slots.GateSettings, m load.Reading, id string, run int64) hooks.GateConfig {
	return hooks.GateConfig{
		Client: client, Machine: slots.Defaults(), Gate: gate, MachineID: machine,
		Job:    scheduler.JobRef{Repository: "o/r", RunID: run, RunAttempt: 1, Job: "build"},
		Holder: scheduler.Holder{Machine: machine, HolderPID: 10 + run, GatePID: 1},
		GateID: id, Measure: func() load.Reading { return m },
		Log: func(l string) { w.lines = append(w.lines, l) },
		Now: w.clock, Sleep: w.sleep,
	}
}

var (
	quiet = load.Reading{MemoryPct: 40, MemoryKnown: true, CPUPct: 10, CPUKnown: true}
	hot   = load.Reading{MemoryPct: 95, MemoryKnown: true, CPUPct: 10, CPUKnown: true}
	gate  = slots.GateSettings{MaxWaitMin: 1, PollSec: 15, OnTimeout: "fail"}
)

func (w *world) fill(n int) {
	w.t.Helper()
	for i := 0; i < n; i++ {
		a := agent{w.eng, slots.Defaults()}
		if code := hooks.RunGate(context.Background(), w.cfg(a, gate, quiet, "fill"+string(rune('a'+i)), int64(900+i))); code != 0 {
			w.t.Fatalf("fill %d: exit %d", i, code)
		}
		w.mu.Lock()
		w.running = append(w.running, scheduler.JobRef{Repository: "o/r", RunID: int64(900 + i), RunAttempt: 1, Job: "build"})
		w.mu.Unlock()
	}
}

func (w *world) text() string { return strings.Join(w.lines, "\n") }

// R5.12. Ported from test_machine.py Gate test_free_slot_and_quiet_machine_goes_at_once and
// test_main_with_the_layer_takes_a_slot_and_job_done_frees_it.
func TestFreeSlotQuietMachineGoesAtOnce(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	if code := hooks.RunGate(context.Background(), w.cfg(a, gate, quiet, "g1", 1)); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !strings.Contains(w.text(), "slot 1 taken after 0 s") {
		t.Fatalf("log:\n%s", w.text())
	}
	st, _ := w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 1 {
		t.Fatalf("status %+v", st)
	}
	// the job-completed hook frees it
	done := hooks.DoneConfig{Client: a, MachineID: machine, Job: scheduler.JobRef{Repository: "o/r", RunID: 1, RunAttempt: 1, Job: "build"}, Log: func(l string) { w.lines = append(w.lines, l) }}
	if code := hooks.RunJobDone(context.Background(), done); code != 0 {
		t.Fatal(code)
	}
	st, _ = w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 0 || !strings.Contains(w.text(), "freed slot 1 of o/r run 1 job build") {
		t.Fatalf("status %+v\n%s", st, w.text())
	}
}

// R5.20. Ported from test_machine.py Gate test_all_slots_busy_times_out_with_exit_1_and_removes_ticket_and_marker,
// test_on_timeout_run_goes_without_a_slot, test_main_over_the_load_limit_times_out_with_exit_1.
func TestTimeout(t *testing.T) {
	w := newWorld(t)
	w.fill(2)
	a := agent{w.eng, slots.Defaults()}
	w.lines = nil
	if code := hooks.RunGate(context.Background(), w.cfg(a, gate, quiet, "late", 5)); code != hooks.ExitTimeout {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	st, _ := w.eng.Status(context.Background(), machine)
	if len(st.Tickets) != 0 || len(st.Slots) != 2 {
		t.Fatalf("ticket not removed: %+v", st)
	}
	if !strings.Contains(w.text(), "timed out after waiting for no free slot (all 2 busy)") || !strings.Contains(w.text(), "can be re-run") {
		t.Fatalf("log:\n%s", w.text())
	}
	// on timeout run: exit 0 without a slot
	w.lines = nil
	run := slots.GateSettings{MaxWaitMin: 1, PollSec: 15, OnTimeout: "run"}
	if code := hooks.RunGate(context.Background(), w.cfg(a, run, quiet, "late2", 6)); code != 0 {
		t.Fatalf("exit %d", code)
	}
	st, _ = w.eng.Status(context.Background(), machine)
	if len(st.Tickets) != 0 || len(st.Slots) != 2 || !strings.Contains(w.text(), "runs without a slot") {
		t.Fatalf("%+v\n%s", st, w.text())
	}
	// the next poll would pass the maximum wait: a zero wait times out at the first hold
	zero := slots.GateSettings{MaxWaitMin: 0, PollSec: 15, OnTimeout: "fail"}
	if code := hooks.RunGate(context.Background(), w.cfg(a, zero, hot, "late3", 7)); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

// R5.21. Ported from test_load.py Gate test_waits_then_passes_and_logs_what_it_waited_for and test_passes_at_once_when_under.
func TestLogLines(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	reading := hot
	calls := 0
	cfg := w.cfg(a, slots.GateSettings{MaxWaitMin: 30, PollSec: 15, OnTimeout: "fail"}, hot, "g", 1)
	cfg.Measure = func() load.Reading {
		calls++
		switch {
		case calls <= 2:
			return hot
		case calls == 3:
			return load.Reading{MemoryPct: 40, MemoryKnown: true, CPUPct: 90, CPUKnown: true}
		}
		return quiet
	}
	_ = reading
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	text := w.text()
	// one start line, "still holding" only when the reasons change, one slot line
	if strings.Count(text, "holding: memory 95% > 85% (poll 15 s, max wait 30 min)") != 1 ||
		strings.Count(text, "still holding: CPU 90% > 60%") != 1 || strings.Count(text, "still holding") != 1 ||
		strings.Count(text, "slot 1 taken after 45 s") != 1 {
		t.Fatalf("log:\n%s", text)
	}
}

// A hook whose ticket was dropped queues again and keeps its place (R5.19), and a vanished ticket is logged.
// Ported from test_dl200_gate_vanished_ticket.py test_dropped_ticket_is_queued_again_and_the_job_gets_a_slot.
func TestVanishedTicketQueuesAgain(t *testing.T) {
	w := newWorld(t)
	w.fill(2)
	a := agent{w.eng, slots.Defaults()}
	cfg := w.cfg(a, slots.GateSettings{MaxWaitMin: 10, PollSec: 15, OnTimeout: "fail"}, quiet, "g", 5)
	n := 0
	cfg.Sleep = func(ctx context.Context, d time.Duration) {
		w.sleep(ctx, d)
		n++
		switch n {
		case 1: // the ticket is dropped behind the hook's back
			_ = w.eng.Cancel(ctx, machine, "g")
		case 2: // then a slot frees
			_, _ = w.eng.Release(ctx, machine, scheduler.JobRef{Repository: "o/r", RunID: 900, RunAttempt: 1, Job: "build"}, nil)
		}
	}
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !strings.Contains(w.text(), "our ticket was gone") {
		t.Fatalf("log:\n%s", w.text())
	}
}

// R5.22 and R5.23. Ported from test_machine.py Gate test_main_without_the_layer_is_the_old_load_gate,
// test_unmeasurable_load_does_not_hold_the_job; test_load.py Gate test_unmeasurable_fails_open.
func TestNoAgentFallsBackToLoadOnlyWait(t *testing.T) {
	w := newWorld(t)
	// a client that cannot be reached
	dead := &hooks.SocketClient{Path: filepath.Join(t.TempDir(), "none.sock")}
	cfg := w.cfg(dead, slots.GateSettings{MaxWaitMin: 5, PollSec: 15, OnTimeout: "fail"}, hot, "g", 1)
	calls := 0
	cfg.Measure = func() load.Reading {
		calls++
		if calls < 3 {
			return hot
		}
		return quiet
	}
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !strings.Contains(w.text(), "agent not reachable") || !strings.Contains(w.text(), "waiting on this machine's load only") {
		t.Fatalf("log:\n%s", w.text())
	}
	// still over budget at the end: the on-timeout rule applies
	w.lines = nil
	cfg = w.cfg(nil, slots.GateSettings{MaxWaitMin: 1, PollSec: 15, OnTimeout: "fail"}, hot, "g", 2)
	if code := hooks.RunGate(context.Background(), cfg); code != 1 {
		t.Fatalf("exit %d", code)
	}
	// unmeasurable (decision D53, on purpose unlike the old pool): the job
	// waits and polls; it does not exit 0. It goes when the load is measured.
	w.lines = nil
	cfg = w.cfg(nil, slots.GateSettings{MaxWaitMin: 5, PollSec: 15, OnTimeout: "fail"}, load.Reading{}, "g", 3)
	calls = 0
	var reported []string
	cfg.Report = func(m string) { reported = append(reported, m) }
	cfg.Measure = func() load.Reading {
		calls++
		if calls < 4 {
			return load.Reading{}
		}
		return quiet
	}
	if code := hooks.RunGate(context.Background(), cfg); code != 0 || calls < 4 {
		t.Fatalf("exit %d, %d measures\n%s", code, calls, w.text())
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "crsp doctor") {
		t.Fatalf("the warning is given once with what to do: %q", reported)
	}
}

// R5.22, R5.23 (D53): never measured means the on-timeout rule "run" becomes
// "fail", with and without an agent.
func TestUnmeasuredTimeoutNeverRuns(t *testing.T) {
	w := newWorld(t)
	run := slots.GateSettings{MaxWaitMin: 1, PollSec: 15, OnTimeout: "run"}
	cfg := w.cfg(nil, run, load.Reading{}, "g", 1)
	if code := hooks.RunGate(context.Background(), cfg); code != hooks.ExitTimeout {
		t.Fatalf("no agent: exit %d\n%s", code, w.text())
	}
	if !strings.Contains(w.text(), "load not measured yet") || !strings.Contains(w.text(), "the job fails and can be re-run") {
		t.Fatalf("log:\n%s", w.text())
	}
	// with an agent that answers: the gate holds, then the same rule applies
	w.lines = nil
	a := agent{w.eng, slots.Defaults()}
	cfg = w.cfg(a, run, load.Reading{}, "g2", 2)
	if code := hooks.RunGate(context.Background(), cfg); code != hooks.ExitTimeout {
		t.Fatalf("agent: exit %d\n%s", code, w.text())
	}
	if strings.Contains(w.text(), "runs without a slot") {
		t.Fatalf("the job must not run:\n%s", w.text())
	}
	// a measured, hot machine keeps the old rule: run on timeout
	w.lines = nil
	cfg = w.cfg(nil, run, hot, "g3", 3)
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("hot: exit %d", code)
	}
}

// R6.6: fail open. A controller that errors mid-wait (not a gone ticket) ends in the load-only wait.
func TestFailOpen(t *testing.T) {
	w := newWorld(t)
	cfg := w.cfg(brokenAfterHello{}, gate, quiet, "g", 1)
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if !strings.Contains(w.text(), "waiting on this machine's load only") {
		t.Fatalf("log:\n%s", w.text())
	}
	// the job-completed hook never fails, even when the agent is gone or panics
	for _, c := range []hooks.Client{nil, brokenAfterHello{}, panicClient{}} {
		code := hooks.RunJobDone(context.Background(), hooks.DoneConfig{Client: c, Job: scheduler.JobRef{Repository: "o/r"}, Log: func(string) {}})
		if code != 0 {
			t.Fatalf("job done exit %d", code)
		}
	}
}

type brokenAfterHello struct{}

func (brokenAfterHello) Hello(context.Context) (hooks.Hello, error) {
	return hooks.Hello{MachineID: machine, Settings: slots.Defaults()}, nil
}
func (brokenAfterHello) Gate(context.Context, scheduler.GateRequest) (scheduler.Decision, error) {
	return scheduler.Decision{}, hooks.ErrUnreachable
}
func (brokenAfterHello) Beat(context.Context, string, string, scheduler.Load) (scheduler.Decision, error) {
	return scheduler.Decision{}, hooks.ErrUnreachable
}
func (brokenAfterHello) Cancel(context.Context, string, string) error { return hooks.ErrUnreachable }
func (brokenAfterHello) Release(context.Context, string, scheduler.JobRef, []string) ([]scheduler.Freed, error) {
	return nil, hooks.ErrUnreachable
}

type panicClient struct{ brokenAfterHello }

func (panicClient) Release(context.Context, string, scheduler.JobRef, []string) ([]scheduler.Freed, error) {
	panic("boom")
}

// R5.12 over the real socket: the agent's Serve and the hook's SocketClient speak one protocol.
func TestHookOverTheAgentSocket(t *testing.T) {
	w := newWorld(t)
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Skipf("unix sockets are not available here: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- hooks.Serve(ln, agent{w.eng, slots.Defaults()}) }()
	t.Cleanup(func() {
		_ = ln.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	env := map[string]string{
		hooks.SocketEnv: sock, "GITHUB_REPOSITORY": "o/r", "GITHUB_RUN_ID": "77", "GITHUB_JOB": "build",
		"GITHUB_TOKEN": "ghs_supersecret", "CRSP_HOLDER_PID": "4242",
	}
	var out, errOut bytes.Buffer
	e := hooks.Env{Getenv: func(k string) string { return env[k] }, Stdout: &out, Stderr: &errOut,
		Measure: func() load.Reading { return quiet }}
	if code := hooks.Run(context.Background(), []string{"job-started"}, e); code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
	st, _ := w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 1 || st.Slots[0].Job.RunID != 77 {
		t.Fatalf("status %+v", st)
	}
	// R6.5: no secret reaches a log line
	if strings.Contains(out.String(), "ghs_supersecret") || strings.Contains(errOut.String(), "ghs_supersecret") {
		t.Fatal("a secret reached the log")
	}
	out.Reset()
	if code := hooks.Run(context.Background(), []string{"job-completed"}, e); code != 0 {
		t.Fatalf("exit %d", code)
	}
	st, _ = w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 0 || !strings.Contains(out.String(), "freed slot 1") {
		t.Fatalf("status %+v\n%s", st, out.String())
	}
	// a bad flag never fails the job-completed hook
	if code := hooks.Run(context.Background(), []string{"job-completed", "--nonsense"}, e); code != 0 {
		t.Fatalf("bad flag exit %d", code)
	}
	if code := hooks.Run(context.Background(), []string{"bogus"}, e); code != 2 {
		t.Fatalf("unknown hook exit %d", code)
	}
}

// R5.28: "this job held no slot".
func TestJobDoneHeldNoSlot(t *testing.T) {
	w := newWorld(t)
	var lines []string
	hooks.RunJobDone(context.Background(), hooks.DoneConfig{Client: agent{w.eng, slots.Defaults()}, MachineID: machine,
		Job: scheduler.JobRef{Repository: "o/r", RunID: 5, Job: "x"}, Log: func(l string) { lines = append(lines, l) }})
	if !strings.Contains(strings.Join(lines, "\n"), "this job held no slot") {
		t.Fatalf("%v", lines)
	}
}

// R5.29. Ported from test_machine.py JobDone test_work_folder_only_inside__work and
// test_empties_the_workspace_and_keeps_the_folder.
func TestWorkFolder(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "runner", "_work")
	repo := filepath.Join(work, "repo", "repo")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.txt", "sub/b.txt"} {
		if err := os.WriteFile(filepath.Join(repo, p), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if hooks.WorkFolder("") != "" || hooks.WorkFolder(work) != "" || hooks.WorkFolder(filepath.Join(root, "elsewhere")) != "" {
		t.Fatal("unsafe folder accepted")
	}
	if got := hooks.WorkFolder(repo); got != repo {
		t.Fatalf("got %q", got)
	}
	if n := hooks.EmptyFolder(repo); n != 0 {
		t.Fatalf("%d entries stay", n)
	}
	if entries, err := os.ReadDir(repo); err != nil || len(entries) != 0 {
		t.Fatalf("folder not emptied: %v %v", entries, err)
	}
	// --keep-work skips it
	if err := os.WriteFile(filepath.Join(repo, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks.RunJobDone(context.Background(), hooks.DoneConfig{KeepWork: true, Workspace: repo})
	if _, err := os.Stat(filepath.Join(repo, "keep.txt")); err != nil {
		t.Fatal("--keep-work emptied the folder")
	}
	hooks.RunJobDone(context.Background(), hooks.DoneConfig{Workspace: repo})
	if _, err := os.Stat(filepath.Join(repo, "keep.txt")); err == nil {
		t.Fatal("work folder not emptied")
	}
}

// R5.59. Ported from test_machine.py ShardAndMarker test_no_cmd_wrappers_ship and test_dl324_runner_policy.py test_env_keeps_hooks_first.
func TestHookWrappers(t *testing.T) {
	win := hooks.Wrappers(`C:\Program Files\crsp\crsp.exe`, true)
	for name, body := range win {
		if strings.HasSuffix(name, ".cmd") || !strings.HasSuffix(name, ".ps1") {
			t.Fatalf("windows wrapper %s", name)
		}
		if !strings.Contains(body, `& 'C:\Program Files\crsp\crsp.exe' hook `) || !strings.Contains(body, "@args") || !strings.Contains(body, "exit $LASTEXITCODE") {
			t.Fatalf("%s:\n%s", name, body)
		}
	}
	lin := hooks.Wrappers("/usr/local/bin/crsp", false)
	if len(lin) != 2 || !strings.HasPrefix(lin[hooks.JobStartedSH], "#!/bin/sh\n") || !strings.Contains(lin[hooks.JobCompletedSH], `exec '/usr/local/bin/crsp' hook job-completed "$@"`) {
		t.Fatalf("%v", lin)
	}
	env := hooks.RunnerEnv("FOO=1\nACTIONS_RUNNER_HOOK_JOB_STARTED=old\nBAR=2\n", "/r/job-started.sh", "/r/job-completed.sh")
	want := "ACTIONS_RUNNER_HOOK_JOB_STARTED=/r/job-started.sh\nACTIONS_RUNNER_HOOK_JOB_COMPLETED=/r/job-completed.sh\nFOO=1\nBAR=2\n"
	if env != want {
		t.Fatalf("got %q", env)
	}
	if again := hooks.RunnerEnv(env, "/r/job-started.sh", "/r/job-completed.sh"); again != want {
		t.Fatalf("not idempotent: %q", again)
	}
}

// R6.1: a quiet gate answers in well under two seconds.
func TestQuietGateUnderTwoSeconds(t *testing.T) {
	w := newWorld(t)
	start := time.Now()
	if code := hooks.RunGate(context.Background(), w.cfg(agent{w.eng, slots.Defaults()}, gate, quiet, "g", 1)); code != 0 {
		t.Fatal(code)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
}

// Caches R5.38: under 10% free the job waits (it is not failed), the alert line is written, and the
// job goes on when space returns.
func TestDiskGuardHoldsAndResumes(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	cfg := w.cfg(a, slots.GateSettings{MaxWaitMin: 5, PollSec: 15, OnTimeout: "fail"}, quiet, "g1", 1)
	free := []float64{4, 6, 11, 13}
	reads := 0
	cfg.Disk = func() (runnercache.Disk, error) {
		f := free[min(reads, len(free)-1)]
		reads++
		return runnercache.Disk{Total: 1000, Free: int64(f * 10)}, nil
	}
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if reads != 4 {
		t.Fatalf("disk read %d times, want 4 (11%% is inside the resume margin)", reads)
	}
	if !strings.Contains(w.text(), "disk guard: holding the job: disk 4.0% free < 10%") ||
		!strings.Contains(w.text(), "disk guard: 13.0% free, the job goes on") {
		t.Fatalf("log:\n%s", w.text())
	}
	st, _ := w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 1 {
		t.Fatalf("the job did not take its slot after the hold: %+v", st)
	}
}

func TestDiskGuardTimeoutFailsWithoutSlotEvenOnTimeoutRun(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	cfg := w.cfg(a, slots.GateSettings{MaxWaitMin: 1, PollSec: 15, OnTimeout: "run"}, quiet, "g1", 1)
	cfg.Disk = func() (runnercache.Disk, error) { return runnercache.Disk{Total: 1000, Free: 20}, nil }
	if code := hooks.RunGate(context.Background(), cfg); code != hooks.ExitTimeout {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	st, _ := w.eng.Status(context.Background(), machine)
	if len(st.Slots) != 0 || len(st.Tickets) != 0 {
		t.Fatalf("a held job took a slot or ticket: %+v", st)
	}
}

func TestDiskGuardFailsOpenOnReadError(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	cfg := w.cfg(a, gate, quiet, "g1", 1)
	cfg.Disk = func() (runnercache.Disk, error) { return runnercache.Disk{}, os.ErrPermission }
	if code := hooks.RunGate(context.Background(), cfg); code != 0 || !strings.Contains(w.text(), "cannot read the disk") {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
}

// Caches R5.37: the job-completed hook evicts the oldest entries over the quota, and never fails.
func TestJobDoneEvictsOldestOverQuota(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string, size int, age time.Duration) {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(d, "blob")
		if err := os.WriteFile(f, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		_ = os.Chtimes(f, ts, ts)
		_ = os.Chtimes(d, ts, ts)
	}
	mk("old", 600, 3*time.Hour)
	mk("new", 600, time.Minute)
	var lines []string
	code := hooks.RunJobDone(context.Background(), hooks.DoneConfig{
		Log:    func(l string) { lines = append(lines, l) },
		Caches: []runnercache.Cap{{Name: "tool cache", Dir: dir, Bytes: 700}},
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatal("old entry kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Fatal("new entry removed")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "tool cache over its quota: removed 1 entries") {
		t.Fatalf("log: %v", lines)
	}
	// a cache folder that cannot be read never fails the hook
	code = hooks.RunJobDone(context.Background(), hooks.DoneConfig{Log: func(string) {},
		Caches: []runnercache.Cap{{Name: "tool cache", Dir: filepath.Join(dir, "new", "blob"), Bytes: 1}}})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
}

// rustCache makes a Rust cache folder with two keys of 600 bytes: "old" and the
// key in use "cur", whose bulk is an incremental folder.
func rustCache(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mk := func(rel string, size int, age time.Duration) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		ts := time.Now().Add(-age)
		_ = os.Chtimes(p, ts, ts)
	}
	mk("old/debug/deps/a", 600, 3*time.Hour)
	mk("cur/debug/incremental/i", 500, time.Minute)
	mk("cur/debug/deps/d", 100, time.Minute)
	return dir
}

// Caches R5.45: the Rust cap runs before the job (in the job-started hook), keeps the key in use
// and writes one line per removal into the job log.
func TestGateBringsRustCacheUnderItsCapBeforeTheJob(t *testing.T) {
	w := newWorld(t)
	a := agent{w.eng, slots.Defaults()}
	cfg := w.cfg(a, slots.GateSettings{MaxWaitMin: 5, PollSec: 15, OnTimeout: "fail"}, quiet, "g1", 1)
	dir := rustCache(t)
	cfg.Caches = []runnercache.Cap{{Name: "Rust build cache", Dir: dir, Bytes: 700, KeyBytes: 700, Rust: true, Keep: "cur"}}
	if code := hooks.RunGate(context.Background(), cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, w.text())
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatal("the other key was not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "cur", "debug", "incremental", "i")); err != nil {
		t.Fatal("the key in use was removed although the other key made it fit")
	}
	if !strings.Contains(w.text(), "Rust build cache: removed old") {
		t.Fatalf("log:\n%s", w.text())
	}
}

// Caches R5.45: after the job, a key in use that is alone over the cap loses its incremental folders first.
func TestJobDoneTrimsKeyInUseIncrementalFirst(t *testing.T) {
	dir := rustCache(t)
	var lines []string
	code := hooks.RunJobDone(context.Background(), hooks.DoneConfig{
		Log:    func(l string) { lines = append(lines, l) },
		Caches: []runnercache.Cap{{Name: "Rust build cache", Dir: dir, Bytes: 300, KeyBytes: 300, Rust: true, Keep: "cur"}},
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "cur", "debug", "incremental")); !os.IsNotExist(err) {
		t.Fatal("incremental folder kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "cur", "debug", "deps", "d")); err != nil {
		t.Fatal("deps removed before it had to be")
	}
	if !strings.Contains(strings.Join(lines, "\n"), "incremental") {
		t.Fatalf("log: %v", lines)
	}
}
