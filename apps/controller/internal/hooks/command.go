package hooks

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/load"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
)

// SocketEnv names the agent's local socket.
const SocketEnv = "CRSP_AGENT_SOCKET"

// DefaultSocket is where the agent listens when SocketEnv is not set.
func DefaultSocket() string {
	if runtime.GOOS == "windows" {
		pd := os.Getenv("PROGRAMDATA")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "crsp", "agent.sock")
	}
	return "/var/run/crsp/agent.sock"
}

// Env is the process environment a hook runs in; tests pass their own.
type Env struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	// Setenv sets a variable of this process; nil is os.Setenv (tests replace it).
	Setenv func(key, value string)
	// Client overrides the socket client (tests, an in-process agent).
	Client Client
	// Sleep, Now and Measure override the real ones (tests).
	Sleep   func(ctx context.Context, d time.Duration)
	Now     func() time.Time
	Measure func() load.Reading
	// Cleaner lets a test replace parts of the real cleaner.
	Cleaner func(*Cleaner)
	// Disk overrides the disk read of the disk guard (tests).
	Disk func() (runnercache.Disk, error)
}

// Run is `crsp hook <job-started|job-completed> [flags]`. It returns the exit
// code. job-completed always returns 0 (R5.30).
func Run(ctx context.Context, args []string, e Env) int {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if len(args) == 0 {
		_, _ = fmt.Fprintln(e.Stderr, "usage: crsp hook job-started|job-completed [flags]")
		return 2
	}
	// First thing, before any other step: nothing a hook starts may ask for a login.
	e.applyNonInteractive()
	if e.Getenv(ProbeEnv) == "1" && (args[0] == "job-started" || args[0] == "job-completed") {
		_, _ = fmt.Fprintln(e.Stdout, ProbeOK)
		_, _ = fmt.Fprintln(e.Stdout, NonInteractiveOK)
		return 0
	}
	switch args[0] {
	case "job-started":
		return runStarted(ctx, args[1:], e)
	case "job-completed":
		return runCompleted(ctx, args[1:], e)
	}
	_, _ = fmt.Fprintf(e.Stderr, "unknown hook %q\n", args[0])
	return 2
}

// jobFromEnv reads the job's identity from the runner's environment (R5.28).
func jobFromEnv(getenv func(string) string) scheduler.JobRef {
	run, _ := strconv.ParseInt(getenv("GITHUB_RUN_ID"), 10, 64)
	att, _ := strconv.Atoi(getenv("GITHUB_RUN_ATTEMPT"))
	if att < 1 {
		att = 1
	}
	return scheduler.JobRef{Repository: getenv("GITHUB_REPOSITORY"), RunID: run, RunAttempt: att, Job: getenv("GITHUB_JOB"),
		Shard: getenv("CRSP_SHARD"), RunnerName: getenv("RUNNER_NAME")}
}

func (e Env) client() Client {
	if e.Client != nil {
		return &helloOnce{Client: e.Client}
	}
	path := e.Getenv(SocketEnv)
	if path == "" {
		path = DefaultSocket()
	}
	return &helloOnce{Client: &SocketClient{Path: path, Timeout: CallTimeout}}
}

// helloOnce asks the agent for hello one time. The answer, or the failure, is
// kept: a slow agent costs the hook one limit, not one per step (R5.62).
type helloOnce struct {
	Client
	done  bool
	hello Hello
	err   error
}

func (h *helloOnce) Hello(ctx context.Context) (Hello, error) {
	if !h.done {
		h.hello, h.err = h.Client.Hello(ctx)
		h.done = true
	}
	return h.hello, h.err
}

// logger writes each line to stdout (the job's Set up job step) and the agent log file.
func (e Env) logger(path string) func(string) {
	return func(line string) {
		line = time.Now().Format("2006-01-02 15:04:05") + " " + line
		_, _ = fmt.Fprintln(e.Stdout, line)
		if path == "" {
			return
		}
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintln(f, line)
		_ = f.Close()
	}
}

func defaultLogPath(getenv func(string) string) string {
	if p := getenv("CRSP_GATE_LOG"); p != "" {
		return p
	}
	return ""
}

func runStarted(ctx context.Context, args []string, e Env) int {
	fs := flag.NewFlagSet("job-started", flag.ContinueOnError)
	fs.SetOutput(e.Stderr)
	maxWait := fs.Int("max-wait", -1, "maximum wait in minutes")
	poll := fs.Int("poll", -1, "poll interval in seconds")
	onTimeout := fs.String("on-timeout", "", "fail or run")
	logPath := fs.String("log", defaultLogPath(e.Getenv), "agent log file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := e.logger(*logPath)
	// Before the gate and before any step: a fork pull request on a public
	// repository fails at once unless the policy allows it (R5.26).
	if ForkPullRequest(e.Getenv) {
		_, _ = fmt.Fprintln(e.Stdout, "::error title=crsp::"+ForkRefusal)
		log("job-started: refused, fork pull request on a public repository (policy)")
		return 1
	}
	var gateID [8]byte
	_, _ = rand.Read(gateID[:])
	machine := slots.Defaults()
	cfg := GateConfig{
		Client:     e.client(),
		Machine:    machine,
		Gate:       slots.ResolveGate(*maxWait, *poll, *onTimeout, e.Getenv, machine),
		Job:        jobFromEnv(e.Getenv),
		EventClass: slots.EventClass(e.Getenv),
		Holder:     scheduler.Holder{HolderPID: HolderPID(e.Getenv), GatePID: int64(os.Getpid())},
		GateID:     "gate-" + hex.EncodeToString(gateID[:]),
		Caches:     runnercache.CapsFromEnv(e.Getenv),
		Measure:    e.Measure,
		Disk:       e.Disk,
		Log:        log,
		Top:        load.TopProcessText,
		Now:        e.Now,
		Sleep:      e.Sleep,
	}
	cfg.BudgetMemoryPct, _ = strconv.Atoi(strings.TrimSpace(e.Getenv("CRSP_BUDGET_MEMORY_PCT")))
	cfg.BudgetCPUPct, _ = strconv.Atoi(strings.TrimSpace(e.Getenv("CRSP_BUDGET_CPU_PCT")))
	if cfg.Disk == nil {
		cfg.Disk = osDisk(e.Getenv)
	}
	cfg.MinFreePct, _ = strconv.Atoi(strings.TrimSpace(e.Getenv("CRSP_DISK_MIN_FREE_PCT")))
	if cfg.Measure == nil {
		s := &load.Sampler{Probe: load.NewProbe(), CachePath: load.DefaultCachePath()}
		cfg.Measure = s.Sample
	}
	// The gate settings come from the agent when it answers (flag and env still win, R5.5).
	if cfg.Client != nil {
		log(fmt.Sprintf("job-started: asking the agent for the gate settings (limit %.0f s)", CallTimeout.Seconds()))
		if h, err := cfg.Client.Hello(ctx); err == nil {
			cfg.Gate = slots.ResolveGate(*maxWait, *poll, *onTimeout, e.Getenv, h.Settings)
		}
	}
	cfg.Report, cfg.Healthy = e.reporter("job-started")
	if Enabled(e.Getenv) {
		cfg.Clean = func() CleanResult { return e.cleaner(PhaseStarted, log).Clean() }
	}
	return RunGate(ctx, cfg)
}

func runCompleted(ctx context.Context, args []string, e Env) int {
	fs := flag.NewFlagSet("job-completed", flag.ContinueOnError)
	fs.SetOutput(e.Stderr)
	keep := fs.Bool("keep-work", false, "do not empty the work folder")
	logPath := fs.String("log", defaultLogPath(e.Getenv), "agent log file")
	if err := fs.Parse(args); err != nil {
		// Even a bad flag never fails a finished job (R5.30).
		_, _ = fmt.Fprintln(e.Stderr, "job-completed:", err)
	}
	report, healthy := e.reporter("job-completed")
	logf := e.logger(*logPath)
	var clean func() CleanResult
	if Enabled(e.Getenv) {
		clean = func() CleanResult { return e.cleaner(PhaseCompleted, logf).Clean() }
	}
	return RunJobDone(ctx, DoneConfig{Clean: clean,
		Client: e.client(), Job: jobFromEnv(e.Getenv), Workspace: e.Getenv("GITHUB_WORKSPACE"), KeepWork: *keep,
		Log: logf, Caches: runnercache.CapsFromEnv(e.Getenv), Report: report, Healthy: healthy,
	})
}

// osDisk reads the volume of the runner's tool cache, else of the job's
// workspace, else of the current folder: the first that exists.
func osDisk(getenv func(string) string) func() (runnercache.Disk, error) {
	return func() (runnercache.Disk, error) {
		var last error
		for _, p := range []string{getenv(runnercache.EnvToolsDir), getenv("GITHUB_WORKSPACE"), "."} {
			if p == "" {
				continue
			}
			d, err := runnercache.OSDisk(p)
			if err == nil {
				return d, nil
			}
			last = err
		}
		return runnercache.Disk{}, last
	}
}

// cleaner is the real cleaner for this hook; the environment is the hook's own.
func (e Env) cleaner(phase string, log func(string)) *Cleaner {
	c := NewCleaner(e.Getenv, phase, log)
	if e.Cleaner != nil {
		e.Cleaner(c)
	}
	return c
}
