package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/internal/cpucap"
	"github.com/luis-mc/ci-runners-shared-pool/internal/dockercompact"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/internal/leftovers"
	"github.com/luis-mc/ci-runners-shared-pool/internal/load"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/internal/recovery"
	"github.com/luis-mc/ci-runners-shared-pool/internal/redact"
	enginemirror "github.com/luis-mc/ci-runners-shared-pool/internal/registry/mirror/engineconf"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runnercache"
	"github.com/luis-mc/ci-runners-shared-pool/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/internal/secrets"
)

// Config configures the agent.
type Config struct {
	// Controller is host:port of the agent link (default port 8461).
	Controller string
	// Pin is the controller CA fingerprint (SHA-256, hex) from the install command.
	Pin string
	// JoinCode is the one-time code; used only while the agent holds no key.
	JoinCode string
	// LocalCredentialFile is the file the controller writes for its own
	// machine's agent (R5.1). It is read at join and again if the machine key
	// is refused.
	LocalCredentialFile string
	Secrets             secrets.Store
	DataDir             string
	Version             string
	Logger              *slog.Logger
	Sys                 Sys
	Sampler             *load.Sampler
	Gate                *Gate
	// JobSampling measures the CPU and memory of each job the gate starts (OPS66); nil measures none.
	JobSampling *JobSampling
	// Updater follows the controller's version (operations R5.39); nil answers "unsupported".
	Updater Updater
	// UpdaterFor builds the Updater once the gate exists (the agent command sets it).
	UpdaterFor func(*Gate) Updater
	// Caches lists the runners whose cache folders the agent measures and wipes
	// (caches R5.36, web-console R5.25); nil reports none.
	Caches CacheSource
	// CacheFS is the file access of the cache measure and wipe; nil is the real one.
	CacheFS runnercache.FS
	// Runners carries out the runner commands of the controller (runners R5.1);
	// nil answers each with a failure that says the agent has no backend.
	Runners runners.Agent
	// RunnerFiles replaces runner files on UpdateRunnerFiles (runners R5.38);
	// nil means this agent cannot, and its machine report says so.
	RunnerFiles *RunnerFiles
	// Images lists the job images for the machine report; nil reports none.
	Images ImageSource
	// MacVMs counts the macOS VMs of this Mac for the machine report (machines
	// R5.14); nil on any OS but macOS, and the report then says nothing.
	MacVMs MacVMSource
	// BaseImages checks that the runner base image of each backend exists on
	// this machine before jobs are admitted (runners R5.53); nil checks none.
	BaseImages BaseImageSource
	// InstallPackages installs a runner's extra packages into its environment
	// (the persistent prefix or the VM base disk), never on the host OS
	// (runners R5.86). A container runner gets them in its image build, so
	// the hook is not called for it. nil installs none, and a persistent
	// runner that names packages is refused.
	InstallPackages func(ctx context.Context, spec backend.RunnerSpec) error
	// PackageFailures lists the package sets that failed to install, for the
	// machine report and the doctor (runners R5.86); nil reports none.
	PackageFailures func() []runners.PackageFailure
	// Symlink reads whether this account can make symbolic links, for the
	// machine report (doctor R5.42); nil uses ProbeSymlink.
	Symlink SymlinkProber
	// Redactor learns every token and JIT config the controller sends, so no
	// log line or answer shows one (runners R5.11); nil makes a private one.
	Redactor *redact.Redactor
	// Recoverer installs a signed recovery target when the controller refuses
	// the Session as too old (operations R5.44); nil makes the agent wait.
	Recoverer Recoverer
	// CPUCap sets the CPU cap on the runner processes (machines R5.64); nil uses
	// the OS applier.
	CPUCap cpucap.Applier

	// Leftovers is the OS access of the runner leftovers scan and removal
	// (doctor R5.48); nil is the real host. Tests give a fake.
	Leftovers leftovers.OS

	// DockerHost is the OS access of the docker-compact helper (caches R5.51 to R5.54); nil
	// is the real one. Tests give a fake: the real compact never runs in tests.
	DockerHost dockercompact.Host
	// EngineMirrorPaths are the engine files the engine-mirror host step writes (registry
	// R5.65); nil uses the system locations on Linux and writes nothing elsewhere.
	EngineMirrorPaths *enginemirror.Paths

	// DiskFloorGiB is the free space the DataDir volume needs before a job or VM
	// starts, in GiB (runners R5.83). 0 sets no floor here; the commands resolve
	// the setting with runnercache.FloorSetting (default 10).
	DiskFloorGiB int

	// Registry is the client of the image registry calls (registry R5.19); nil
	// makes one. Run puts it into container.Link, where the container backend
	// finds it.
	Registry *RegistryClient

	// Intervals; the defaults are the spec's (R5.35, R5.13, R5.16).
	HeartbeatEvery time.Duration // 10 s
	LoadEvery      time.Duration // 15 s
	RedetectEvery  time.Duration // 10 min
	SampleEvery    time.Duration // 5 s
	DrainPollEvery time.Duration // 2 s
	CacheEvery     time.Duration // 5 min
	// PolicyEvery is how often the agent checks the power source and the pause
	// timer; 0 takes 5 s (machines R5.62).
	PolicyEvery time.Duration
	// BudgetEvery is how often the pool-wide cache budget is checked; 0 takes 10 min (caches R5.48).
	BudgetEvery time.Duration
	// Backoff is the reconnect delay; Sleep waits for it (tests replace both).
	Backoff *Backoff
	Sleep   func(ctx context.Context, d time.Duration) error
}

func (c Config) symlinkProber() SymlinkProber {
	if c.Symlink != nil {
		return c.Symlink
	}
	return ProbeSymlink
}

func (c Config) sys() Sys {
	if c.Sys != nil {
		return c.Sys
	}
	return OSSys{}
}

// DataDirCache is the settings cache of this config.
func (c Config) DataDirCache() SettingsCache { return c.cache() }

func (c Config) cache() SettingsCache { return SettingsCache{Path: c.DataDir + "/settings.json"} }

func (c *Config) defaults() {
	if c.Registry == nil {
		c.Registry = NewRegistryClient()
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Sampler == nil {
		c.Sampler = load.NewSampler(c.sys())
	}
	if c.Gate == nil {
		c.Gate = NewGate(c.Sampler)
	}
	if c.DiskFloorGiB > 0 {
		c.Gate.SetDiskFloor(&runnercache.Floor{Bytes: runnercache.FloorBytes(c.DiskFloorGiB), Path: c.DataDir})
	}
	for _, d := range []struct {
		p *time.Duration
		v time.Duration
	}{
		{&c.HeartbeatEvery, machines.HeartbeatEvery}, {&c.LoadEvery, 15 * time.Second}, {&c.RedetectEvery, 10 * time.Minute},
		{&c.SampleEvery, load.SampleEvery}, {&c.DrainPollEvery, 2 * time.Second}, {&c.CacheEvery, 5 * time.Minute}, {&c.PolicyEvery, 5 * time.Second},
	} {
		if *d.p == 0 {
			*d.p = d.v
		}
	}
	if c.Updater == nil && c.UpdaterFor != nil {
		c.Updater = c.UpdaterFor(c.Gate)
	}
	if c.Backoff == nil {
		c.Backoff = &Backoff{}
	}
	if c.Sleep == nil {
		c.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
}

func untilTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// SettingsFromProto converts the wire settings.
func SettingsFromProto(p *agentv1.MachineSettings) machines.Settings {
	return machines.Settings{
		Version: p.GetVersion(), State: p.GetState(),
		BudgetMemoryPct: int(p.GetBudgetMemoryPct()), BudgetCPUPct: int(p.GetBudgetCpuPct()), JobSlots: int(p.GetJobSlots()),
		LightSlots: int(p.GetLightSlots()), HeavySlots: int(p.GetHeavySlots()), LightJobs: p.GetLightJobs(), HeavyJobs: p.GetHeavyJobs(),
		SlotMaxAgeMinutes: int(p.GetSlotMaxAgeMinutes()), GatePollSeconds: int(p.GetGatePollSeconds()),
		GateMaxWaitMinutes: int(p.GetGateMaxWaitMinutes()), GateOnTimeout: p.GetGateOnTimeout(),
		AllowedBackends: p.GetAllowedBackends(), CacheCapBytes: p.GetCacheCapBytes(),
		DrainTimeoutMinutes: int(p.GetDrainTimeoutMinutes()), WindowsSymlinkRight: p.GetWindowsSymlinkRight(),
		PoolMemoryGiB: p.GetPoolMemoryGib(), PoolCPUs: int(p.GetPoolCpus()),
		Mode: p.GetMode(), PauseOnBattery: p.GetPauseOnBattery(), PauseUntil: untilTime(p.GetPauseUntilUnixMs()),
		PausedUntilResumed: p.GetPauseUntilResumed(), CPUCapPct: int(p.GetCpuCapPct()),
	}
}

// Run is the agent. It joins when it holds no key, then keeps one Session open,
// reconnecting with backoff (R5.36). It returns nil when ctx ends, ErrRemoved
// when the controller refused the key (R5.39), and ErrPinMismatch when the
// controller's certificate is not the pinned one (R5.5). It never listens on a
// port (R5.9).
func Run(ctx context.Context, cfg Config) error {
	cfg.defaults()
	container.Link.Set(cfg.Registry)
	container.BuildLink.Set(cfg.Registry) // the build records live on the controller (IMG1)
	log := cfg.Logger
	if s, state, ok := cfg.cache().Load(); ok {
		cfg.Gate.SetSettings(s, state) // R5.24: apply the last settings while the link is down
	}
	var wg sync.WaitGroup
	// A boot can take the logon right of a runner service account away; give it back now (runners R5.89).
	wg.Add(1)
	go func() { defer wg.Done(); healAtStart(ctx, cfg) }()
	sctx, stopSampling := context.WithCancel(ctx)
	defer func() { stopSampling(); wg.Wait() }()
	wg.Add(1)
	go func() { defer wg.Done(); sampleLoop(sctx, cfg) }()
	if cfg.JobSampling != nil {
		wg.Add(1)
		go func() { defer wg.Done(); cfg.JobSampling.Run(sctx, cfg.Gate) }()
	}

	for ctx.Err() == nil {
		key, id, pin, err := cfg.loadIdentity(ctx)
		if errors.Is(err, secrets.ErrNotFound) {
			if jerr := joinFirst(ctx, cfg); jerr != nil {
				return jerr
			}
			continue
		}
		if err != nil {
			return err // the secret store is down: fail closed
		}
		err = runSession(ctx, cfg, key, id, pin)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, ErrRemoved):
			log.Error("this machine was removed from the pool")
			cfg.forget(ctx)
			if cfg.LocalCredentialFile == "" {
				return ErrRemoved // wait for a new join (R5.39)
			}
			cfg.Backoff.Reset()
			continue // the controller's own agent joins again the same way (R5.1)
		case errors.Is(err, ErrPinMismatch):
			return err
		case errors.Is(err, ErrNeedsRecovery):
			log.Warn("the controller refused this agent as too old; asking for a recovery update")
			if rerr := recoverOnce(ctx, cfg, key, id, pin); rerr != nil && !errors.Is(rerr, recovery.ErrNoUpdate) {
				log.Warn("recovery call", "err", rerr)
			}
			if serr := cfg.Sleep(ctx, cfg.Backoff.Next()); serr != nil {
				return nil
			}
		case err != nil:
			d := cfg.Backoff.Next()
			log.Warn("link lost; reconnecting", "err", err, "in", d.String())
			if serr := cfg.Sleep(ctx, d); serr != nil {
				return nil
			}
		}
	}
	return nil
}

func joinFirst(ctx context.Context, cfg Config) error {
	code, local := strings.TrimSpace(cfg.JoinCode), false
	if code == "" && cfg.LocalCredentialFile != "" {
		b, err := os.ReadFile(cfg.LocalCredentialFile)
		if err != nil {
			return errors.New("no join code and no local credential: " + err.Error())
		}
		code, local = strings.TrimSpace(string(b)), true
	}
	if code == "" {
		return errors.New("this machine has not joined a controller: give a join code (crsp agent install)")
	}
	if err := Join(ctx, cfg, code, local); err != nil {
		return err
	}
	cfg.Logger.Info("joined the controller")
	return nil
}

func sampleLoop(ctx context.Context, cfg Config) {
	cfg.Sampler.Refresh()
	cfg.Sampler.Sample()
	t := time.NewTicker(cfg.SampleEvery)
	defer t.Stop()
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n++
			if n%6 == 0 { // every 30 s with the 5 s default
				go cfg.Sampler.Refresh()
			}
			cfg.Sampler.Sample()
		}
	}
}

func jobToProto(j machines.JobRef) *agentv1.JobRef {
	out := &agentv1.JobRef{Repository: j.Repository, RunId: j.RunID, RunAttempt: int32(j.RunAttempt), Job: j.Job, GithubJobId: j.GitHubJobID, RunnerName: j.RunnerName} //nolint:gosec // small
	if !j.StartedAt.IsZero() {
		out.StartedUnixMs = j.StartedAt.UnixMilli()
	}
	return out
}

// runningReport lists the jobs that run on this machine.
func runningReport(jobs []machines.JobRef) *agentv1.HolderReport {
	var running []*agentv1.JobRef
	for _, j := range jobs {
		running = append(running, jobToProto(j))
	}
	return &agentv1.HolderReport{Running: running}
}

func loadToProto(r load.Report, slots int) *agentv1.LoadReport {
	return &agentv1.LoadReport{
		SampledAtUnixMs: r.SampledAt.UnixMilli(), MemoryPct: r.MemoryPct, MemoryKnown: r.MemoryKnown, CpuPct: r.CPUPct, CpuKnown: r.CPUKnown,
		UnknownReason: r.UnknownReason, WslVmBytes: r.WSLVMBytes, ContainersBytes: r.ContainersBytes,
		SlotsUsed: int32(slots), CgroupLimited: r.CgroupLimited, //nolint:gosec // small
	}
}

type link struct {
	cfg    Config
	stream *connect.BidiStreamForClient[agentv1.SessionRequest, agentv1.SessionResponse]
	sendMu sync.Mutex
	facts  *agentv1.Facts
	factMu sync.Mutex

	drainMu sync.Mutex
	drainID string // command id of the drain in progress; empty when none
	// drainRunners are the runners the drain touches; empty means the machine (R5.57).
	drainRunners []string

	followMu  sync.Mutex
	following bool

	wipes  *wiper
	hyg    hygieneKeeper
	budget *Budgeter

	// kick wakes the policy loop; capMu guards cap, the last CPU cap set.
	kick  chan struct{}
	capMu sync.Mutex
	cap   capState
}

func (l *link) send(m *agentv1.SessionRequest) error {
	l.sendMu.Lock()
	defer l.sendMu.Unlock()
	return l.stream.Send(m)
}

func (l *link) redetect(ctx context.Context) *agentv1.Facts {
	f := DetectAll(ctx, l.cfg.sys(), l.cfg.DataDir, time.Now())
	l.factMu.Lock()
	l.facts = f
	l.factMu.Unlock()
	return f
}

func runSession(ctx context.Context, cfg Config, key, id, pin string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l := &link{cfg: cfg, kick: make(chan struct{}, 1)}
	l.budget = NewBudgeter(cfg)
	l.wipes = &wiper{cfg: cfg, send: l.send, fsys: cfg.cacheFS(), poll: cfg.DrainPollEvery}
	l.stream = newClient(pin, cfg.Controller).Session(ctx)
	l.stream.RequestHeader().Set("Authorization", "Bearer "+key)
	l.stream.RequestHeader().Set("Crsp-Machine-Id", id)
	// Ending the session must unblock Receive at once, so close both halves.
	go func() {
		<-ctx.Done()
		_ = l.stream.CloseRequest()
		_ = l.stream.CloseResponse()
	}()

	facts := l.redetect(ctx)
	rep, _ := cfg.Sampler.Latest()
	set, _ := cfg.Gate.Settings()
	var running []*agentv1.JobRef
	for _, j := range cfg.Gate.Running() {
		running = append(running, jobToProto(j))
	}
	hello := &agentv1.Hello{
		MachineId: id, AgentVersion: cfg.Version, ProtocolVersion: agentv1.ProtocolVersion_PROTOCOL_VERSION_3,
		SettingsVersion: set.Version, Facts: facts, Load: loadToProto(rep, cfg.Gate.SlotsUsed()),
		Holders: &agentv1.HolderReport{Running: running},
	}
	if err := l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Hello{Hello: hello}}); err != nil {
		// On io.EOF the real error, such as "unauthenticated", is on the receive side.
		if _, rerr := l.stream.Receive(); rerr != nil {
			err = rerr
		}
		return classify(err)
	}
	ack, err := l.stream.Receive()
	if err != nil {
		return classify(err)
	}
	if ack.GetHelloAck() == nil {
		return errors.New("the controller did not answer Hello")
	}
	if s := ack.GetHelloAck().GetSettings(); s != nil {
		applySettings(cfg, s)
	}
	rs := cfg.Registry.attach(l.send, HelloAckHasRegistry(ack.GetHelloAck()))
	defer cfg.Registry.detach(rs)
	if cfg.JobSampling != nil {
		defer cfg.JobSampling.Sender.Attach(l.send, HelloAckHasJobSamples(ack.GetHelloAck()))()
	}
	cfg.Backoff.Reset() // a successful session resets the backoff (R5.36)
	cfg.Logger.Info("session open")

	lastRecv := newStamp()
	go l.sendLoop(ctx, cancel, lastRecv)
	go l.cacheLoop(ctx)
	go l.machineReportLoop(ctx)
	go l.budgetLoop(ctx)
	go l.policyLoop(ctx)

	for {
		msg, err := l.stream.Receive()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return classify(err)
		}
		lastRecv.set()
		if rerr := l.handle(ctx, msg); rerr != nil {
			return rerr
		}
	}
}

func classify(err error) error {
	if isTooOld(err) {
		return ErrNeedsRecovery
	}
	if connect.CodeOf(err) == connect.CodeUnauthenticated {
		return ErrRemoved
	}
	if isPinError(err) {
		return ErrPinMismatch
	}
	return err
}

func applySettings(cfg Config, s *agentv1.MachineSettings) {
	cfg.Gate.SetSettings(SettingsFromProto(s), s.GetState())
	if err := cfg.cache().Save(SettingsFromProto(s), s.GetState()); err != nil {
		cfg.Logger.Warn("settings could not be cached", "err", err)
	}
}

type stamp struct {
	mu sync.Mutex
	t  time.Time
}

func newStamp() *stamp { return &stamp{t: time.Now()} }
func (s *stamp) set()  { s.mu.Lock(); s.t = time.Now(); s.mu.Unlock() }
func (s *stamp) age() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.t)
}

// sendLoop sends heartbeats, load reports, periodic facts and drain status. When
// the controller has been silent for three heartbeats it ends the session so the
// agent reconnects (R5.35, R5.36).
func (l *link) sendLoop(ctx context.Context, cancel context.CancelFunc, lastRecv *stamp) {
	cfg := l.cfg
	hb := time.NewTicker(cfg.HeartbeatEvery)
	ld := time.NewTicker(cfg.LoadEvery)
	rd := time.NewTicker(cfg.RedetectEvery)
	dr := time.NewTicker(cfg.DrainPollEvery)
	defer hb.Stop()
	defer ld.Stop()
	defer rd.Stop()
	defer dr.Stop()
	fail := func(err error) bool {
		if err != nil {
			cancel()
			return true
		}
		return false
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			if lastRecv.age() > 3*cfg.HeartbeatEvery {
				cancel()
				return
			}
			if fail(l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Heartbeat{Heartbeat: &agentv1.Heartbeat{SentAtUnixMs: time.Now().UnixMilli()}}})) {
				return
			}
		case <-ld.C:
			rep, _ := cfg.Sampler.Latest()
			if fail(l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_LoadReport{LoadReport: loadToProto(rep, cfg.Gate.SlotsUsed())}})) {
				return
			}
			// The holder report is the lease heartbeat of the jobs that run here (scheduling R5.70).
			if fail(l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_HolderReport{HolderReport: runningReport(cfg.Gate.Running())}})) {
				return
			}
		case <-rd.C:
			if fail(l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Facts{Facts: l.redetect(ctx)}})) {
				return
			}
		case <-dr.C:
			if fail(l.reportDrain()) {
				return
			}
		}
	}
}

func (l *link) setDrain(id string, runners ...string) {
	l.drainMu.Lock()
	l.drainID, l.drainRunners = id, runners
	l.drainMu.Unlock()
}

// reportDrain sends the drain status: the idle check and the hold are one step
// in Gate.Hold (R5.32). It sends nothing while no drain is in progress.
func (l *link) reportDrain() error {
	l.drainMu.Lock()
	id, runners := l.drainID, l.drainRunners
	l.drainMu.Unlock()
	if id == "" {
		return nil
	}
	idle, waiting := l.cfg.Gate.HoldRunners(runners)
	st := &agentv1.DrainStatus{CommandId: id, Idle: idle}
	for _, j := range waiting {
		st.WaitingFor = append(st.WaitingFor, jobToProto(j))
	}
	if idle {
		l.setDrain("") // the controller acts on this one report
	}
	return l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_DrainStatus{DrainStatus: st}})
}

func (l *link) handle(ctx context.Context, msg *agentv1.SessionResponse) error {
	cfg := l.cfg
	switch m := msg.GetMessage().(type) {
	case *agentv1.SessionResponse_Settings:
		applySettings(cfg, m.Settings)
		l.kickPolicy()
	case *agentv1.SessionResponse_Drain:
		l.setDrain(m.Drain.GetCommandId(), m.Drain.GetRunnerNames()...)
		_ = l.reportDrain() // the first check right away
	case *agentv1.SessionResponse_Resume:
		l.setDrain("")
		cfg.Gate.Release()
	case *agentv1.SessionResponse_Redetect:
		f := l.redetect(ctx)
		_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_Facts{Facts: f}})
		go l.reportMachine(ctx, "")
		_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_CommandResult{CommandResult: &agentv1.CommandResult{CommandId: m.Redetect.GetCommandId(), Ok: true, Outcome: "done"}}})
	case *agentv1.SessionResponse_ReportMachine:
		go l.reportMachine(ctx, m.ReportMachine.GetCommandId())
	case *agentv1.SessionResponse_AddRunner, *agentv1.SessionResponse_StopRunner,
		*agentv1.SessionResponse_RemoveRunner, *agentv1.SessionResponse_DrainRunner:
		l.runnerCmd(ctx, msg)
	case *agentv1.SessionResponse_UpdateRunnerFiles:
		go l.updateFiles(ctx, m.UpdateRunnerFiles)
	case *agentv1.SessionResponse_RunHostCommand:
		go l.runHostCommand(ctx, m.RunHostCommand)
	case *agentv1.SessionResponse_ScanRunnerLeftovers:
		go l.scanLeftovers(ctx, m.ScanRunnerLeftovers)
	case *agentv1.SessionResponse_RemoveRunnerLeftovers:
		go l.removeLeftovers(ctx, m.RemoveRunnerLeftovers)
	case *agentv1.SessionResponse_MeasureCaches:
		go l.reportCaches(ctx, m.MeasureCaches.GetCommandId())
	case *agentv1.SessionResponse_RunnerHygiene:
		l.applyHygiene(ctx, m.RunnerHygiene)
	case *agentv1.SessionResponse_WipeCache:
		l.wipes.start(ctx, m.WipeCache)
	case *agentv1.SessionResponse_UpdateAgent:
		l.handleUpdate(ctx, m.UpdateAgent)
	case *agentv1.SessionResponse_EnsureImageAnswer:
		cfg.Registry.onAnswer(m.EnsureImageAnswer)
	case *agentv1.SessionResponse_RegistryGrant:
		cfg.Registry.onToken(m.RegistryGrant)
	case *agentv1.SessionResponse_MachineRemoved:
		return ErrRemoved
	}
	return nil
}

func (c Config) cacheFS() runnercache.FS {
	if c.CacheFS != nil {
		return c.CacheFS
	}
	return runnercache.OSFS{}
}

// reportCaches measures every runner's cache folders and sends the report. The
// agent measures and reports; the controller lists (web-console R5.25).
func (l *link) reportCaches(ctx context.Context, commandID string) {
	rep := &agentv1.CacheReport{
		CommandId: commandID, MeasuredAtUnixMs: time.Now().UnixMilli(),
		Caches: MeasureCaches(ctx, l.cfg.Caches, l.cfg.cacheFS()),
	}
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_CacheReport{CacheReport: rep}})
	// The clean-up state of each persistent runner goes with it (runners R5.91).
	l.reportClean(ctx)
}

// cacheLoop reports once when the session opens and then every CacheEvery.
func (l *link) cacheLoop(ctx context.Context) {
	l.reportCaches(ctx, "")
	t := time.NewTicker(l.cfg.CacheEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.reportCaches(ctx, "")
		}
	}
}

// machineReportLoop reports once when the session opens and then every
// RedetectEvery.
func (l *link) machineReportLoop(ctx context.Context) {
	l.reportMachine(ctx, "")
	t := time.NewTicker(l.cfg.RedetectEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.reportMachine(ctx, "")
		}
	}
}
