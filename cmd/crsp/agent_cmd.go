package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/luis-mc/ci-runners-shared-pool/internal/agent"
	"github.com/luis-mc/ci-runners-shared-pool/internal/agent/jobsample"
	"github.com/luis-mc/ci-runners-shared-pool/internal/logging"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
)

// runAgentCmd is `crsp agent <run|join|install>` (machines R5.3, R5.9, R5.44 to R5.48).
func runAgentCmd(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("agent needs a command: run, join or install (crsp agent install --help)")
	}
	switch args[0] {
	case "run":
		return agentRun(args[1:], stderr)
	case "join":
		return agentJoin(args[1:], stdout)
	case "install":
		return agentInstall(args[1:], stdout)
	}
	return fmt.Errorf("unknown agent command %q (run, join or install)", args[0])
}

type agentFlags struct {
	controller, fingerprint, joinCode, dataDir string
	diskFloorGiB                               int
	yes                                        bool
}

func parseAgentFlags(name string, args []string) (*flag.FlagSet, *agentFlags, error) {
	return parseAgentFlagsFor(runtime.GOOS, name, args)
}

// parseAgentFlagsFor parses the flags of an agent command. Without --data-dir the
// machine-wide folder of goos is used, the one the install step makes: the
// service runs as LocalSystem or root, never from a user profile (R5.44).
func parseAgentFlagsFor(goos, name string, args []string) (*flag.FlagSet, *agentFlags, error) {
	fs := flag.NewFlagSet("agent "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := &agentFlags{}
	fs.StringVar(&f.controller, "controller", os.Getenv("CRSP_CONTROLLER"), "controller address, host:8461")
	fs.StringVar(&f.fingerprint, "fingerprint", os.Getenv("CRSP_FINGERPRINT"), "SHA-256 fingerprint of the controller certificate")
	fs.StringVar(&f.joinCode, "join-code", "", "the one-time join code")
	fs.StringVar(&f.dataDir, "data-dir", "", "the agent's data folder")
	fs.IntVar(&f.diskFloorGiB, "disk-floor-gib", 0, "free GiB needed before a job or VM starts; 0 takes 10, a negative value turns the floor off")
	fs.BoolVar(&f.yes, "yes", false, "install: answer yes to the plan (for a join over SSH, where nobody can type)")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	if f.dataDir == "" {
		f.dataDir = agent.DefaultDataDir(goos)
	}
	return fs, f, nil
}

func agentConfig(f *agentFlags, logger *slog.Logger) (agent.Config, error) {
	store, err := agent.NewFileStore(filepath.Join(f.dataDir, "secrets"))
	if err != nil {
		return agent.Config{}, err
	}
	return agent.Config{Controller: f.controller, Pin: f.fingerprint, JoinCode: f.joinCode, Secrets: store, DataDir: f.dataDir, Version: version, Logger: logger, Caches: agentCacheSource(), DiskFloorGiB: runnercache.FloorSetting(f.diskFloorGiB),
		Runners: agentRunners(), RunnerFiles: agentRunnerFiles(), Images: agentImageSource(), MacVMs: agentMacVMs(), Redactor: secretRedactor}, nil
}

func agentRun(args []string, stderr io.Writer) error {
	_, f, err := parseAgentFlags("run", args)
	if err != nil {
		return err
	}
	if f.controller == "" {
		return errors.New("the controller address is required: --controller host:8461")
	}
	if _, err := addresses.Load(os.Getenv); err != nil {
		return err
	}
	logger := slog.New(logging.NewHandler(slog.NewJSONHandler(stderr, nil), secretRedactor))
	slog.SetDefault(logger)
	cfg, err := agentConfig(f, logger)
	if err != nil {
		return err
	}
	rec := &agentRecoverer{}
	cfg.Recoverer = rec
	// The per-job CPU and memory peaks (OPS66): the reader of this operating system, sampled while a job runs.
	cfg.JobSampling = agent.NewJobSampling(jobsample.NewPlatform(), nil)
	cfg.UpdaterFor = func(g *agent.Gate) agent.Updater {
		u := newAgentUpdater(cfg, g, logger)
		rec.set(u)
		return u
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = agent.Run(ctx, cfg)
	if errors.Is(err, agent.ErrRemoved) {
		return err
	}
	return err
}

func agentJoin(args []string, stdout io.Writer) error {
	_, f, err := parseAgentFlags("join", args)
	if err != nil {
		return err
	}
	if f.controller == "" || f.fingerprint == "" || f.joinCode == "" {
		return errors.New("join needs --controller, --fingerprint and --join-code")
	}
	cfg, err := agentConfig(f, slog.Default())
	if err != nil {
		return err
	}
	if err := agent.Join(context.Background(), cfg, f.joinCode, false); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "Joined the controller. The agent keeps its key in the secret store.")
	return nil
}

type osExec struct{}

func (osExec) Run(ctx context.Context, argv []string) error {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // argv built by the approved install plan
	if err != nil {
		return agent.CommandError(argv, out, err)
	}
	return nil
}

func (osExec) WriteFile(_ context.Context, f agent.File) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil { //nolint:gosec // system folder of a unit file
		return err
	}
	return os.WriteFile(f.Path, []byte(f.Body), f.Mode)
}

// agentInstall is the one step that needs admin rights: it lists each admin
// action and asks for a yes before anything runs (R5.45). Without admin rights
// it stops first with one plain line and changes nothing.
func agentInstall(args []string, stdout io.Writer) error {
	return installWith(args, stdout, os.Stdin, runtime.GOOS, agent.Elevated(), osExec{})
}

func installWith(args []string, stdout io.Writer, stdin io.Reader, goos string, elevated bool, ex agent.Executor) error {
	_, f, err := parseAgentFlagsFor(goos, "install", args)
	if err != nil {
		return err
	}
	if f.controller == "" || f.fingerprint == "" {
		return errors.New("install needs --controller and --fingerprint (copy the command from Add machine)")
	}
	bin, err := os.Executable()
	if err != nil {
		return err
	}
	plan, err := agent.BuildInstallPlan(goos, agent.InstallOptions{
		Binary: bin, Controller: f.controller, Fingerprint: f.fingerprint, JoinCode: f.joinCode, DataDir: f.dataDir,
	})
	if err != nil {
		return err
	}
	in := bufio.NewReader(stdin)
	confirm := func(text string) bool {
		if f.yes {
			_, _ = fmt.Fprint(stdout, text, "Continue? --yes was given.\n")
			return true
		}
		_, _ = fmt.Fprint(stdout, text, "Continue? Type yes: ")
		line, _ := in.ReadString('\n')
		return strings.EqualFold(strings.TrimSpace(line), "yes")
	}
	if err := plan.RunElevated(context.Background(), elevated, confirm, ex); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "The agent is installed and running.")
	return nil
}
