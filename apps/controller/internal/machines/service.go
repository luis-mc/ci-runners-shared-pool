package machines

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Timings of the spec.
const (
	// OfflineAfter: no heartbeat for this long marks a machine offline (R5.35).
	OfflineAfter = 30 * time.Second
	// HeartbeatEvery is what the controller expects of agents (R5.35).
	HeartbeatEvery = 10 * time.Second
	// StaleAfter: a load older than this is marked stale (R6.7).
	StaleAfter = 20 * time.Second
	// LoadStep: load history is kept at this step (R6.8).
	LoadStep = 15 * time.Second
	// LoadRetention: older load is dropped (R6.8).
	LoadRetention = 24 * time.Hour

	// DefaultCodeTTL and MaxCodeTTL are the join code lifetime (R5.4).
	DefaultCodeTTL = 15 * time.Minute
	MaxCodeTTL     = 24 * time.Hour

	// joinFailLimit failed joins from one address inside joinFailWindow refuse
	// that address for joinBlockFor (R6.3).
	joinFailLimit  = 10
	joinFailWindow = time.Minute
	joinBlockFor   = 5 * time.Minute
)

// Notifier is told after a change committed, so live views update (R5.50).
type Notifier interface {
	MachineChanged(machineID string)
	LoadChanged(machineID string)
}

// DrainOverdueNotifier is an optional part of a Notifier: it is told once
// when a drain passed its timeout and still waits, so the console, the CLI and
// the alerts say "still draining" (R5.33).
type DrainOverdueNotifier interface {
	DrainOverdue(machineID, kind string, waited time.Duration, jobs []JobRef)
}

// PresenceObserver is told what the controller sees of each machine, so the uptime and the reasons
// a machine took no job are kept (R5.83). *machinehealth.Service implements it.
type PresenceObserver interface {
	// Observe: online says whether heartbeats arrive; reason is empty while the machine takes jobs, why
	// it takes none while online, or why it went offline; lastSeen is the last heartbeat.
	Observe(ctx context.Context, machineID string, online bool, reason string, lastSeen, at time.Time)
}

// Options configure a Service.
type Options struct {
	Repo Repository
	Now  func() time.Time
	// Controller address and the certificate pin go into the install commands.
	ControllerAddress string
	AgentPort         int
	Fingerprint       string
	Notify            Notifier
	// OnIdle runs a change of another feature (agent update, runner image
	// rebuild, service restart) once the machine is idle; it runs in the step
	// that keeps new jobs out (R5.32). Nil means nothing else to do.
	OnIdle func(ctx context.Context, c Change) error
	// PoolDefaults gives the pool's defaults for a machine that joins
	// (Settings, Budgets: machines R5.22). Nil, or an error, means the
	// built-in defaults. Changing them never touches a machine that already
	// joined, so no running job waits for it.
	PoolDefaults func(ctx context.Context) (PoolDefaults, error)
	// SSH runs the install command over SSH for the optional SSH join
	// (RES3). Nil means the SSH join is not available.
	SSH SSHRunner
	// WindowsPackages says where the Windows installer is, for an SSH join to a
	// Windows machine that has no crsp.exe yet (CRSP123). Nil means it cannot send one.
	WindowsPackages WindowsPackageSource
	// AgentInstallers says where the agent installers are (CRSP304). When set,
	// an SSH join installs the agent on a machine that has no crsp yet, from the
	// controller's cache or the release, and the owner never sees an installer.
	AgentInstallers AgentInstallerSource
	// Packages gives the release packages a downloaded installer file fetches
	// (R5.65). Nil means the download option is not available.
	Packages PackageSource
	// ConsoleURL is the address the packages are fetched from, such as
	// https://pool.lan:8460, as the machine to join sees it.
	ConsoleURL string
	// MinProtocol is the lowest agent link protocol version a Session accepts
	// (operations R5.44); an older agent keeps only the recovery call. 0 accepts all.
	MinProtocol int32
}

// PoolDefaults are the settings a new machine starts with.
type PoolDefaults struct {
	BudgetMemoryPct      int
	BudgetCPUPct         int
	SizeSlotsFromMachine bool
	JobSlots             int
	DrainTimeoutMinutes  int
}

// Service is the controller side of machines.
type Service struct {
	o               Options
	sessions        *Sessions
	hooks           Hooks
	recoveryHandler http.Handler
	depFilesHandler http.Handler
	host            HostRunner
	compact         compactBook
	// removeRunners deregisters the runners of a machine that is removed (R5.40 to R5.42).
	removeRunners func(ctx context.Context, machineID, actor string, agentUp bool) []RunnerRemoval
	queue         QueueSource
	disks         map[string]DockerDisk

	// rec serialises the read-modify-write of a machine record, so a report of the
	// agent (heartbeat, facts) never writes back a stale copy over a change of
	// the user (a rename, a drain) made between its read and its write.
	rec sync.Mutex

	mu       sync.Mutex
	presence PresenceObserver
	loads    map[string]Load
	policies map[string]Policy
	engines  map[string]Engine
	rsvcs    map[string][]RunnerService
	lastKept map[string]time.Time
	running  map[string][]JobRef
	// watches is the load watch of each machine (scheduling R5.77).
	watches map[string]*watchState
	// onRunning sees each running-jobs report (SetRunningObserver).
	onRunning func(machineID string, jobs []JobRef)
	runners   map[string][]string
	fails     map[string][]time.Time
	blocked   map[string]time.Time
	updates   map[string]AgentUpdate
	recov     map[string]recState
	typical   TypicalFunc
	// stuckSent holds the changes whose stuck alert went out, so the tick and a
	// status report that read the same change cannot both raise it.
	stuckSent map[string]bool
	// lo holds the runner leftovers sources and the latest scan per machine.
	lo leftoverState
}

// New returns a Service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AgentPort == 0 {
		o.AgentPort = 8461
	}
	return &Service{
		o: o, sessions: newSessions(),
		loads: map[string]Load{}, policies: map[string]Policy{}, disks: map[string]DockerDisk{}, engines: map[string]Engine{}, rsvcs: map[string][]RunnerService{}, lastKept: map[string]time.Time{}, running: map[string][]JobRef{},
		runners: map[string][]string{}, fails: map[string][]time.Time{}, blocked: map[string]time.Time{},
		updates: map[string]AgentUpdate{}, recov: map[string]recState{},
	}
}

func (s *Service) now() time.Time { return s.o.Now().UTC() }

// updateMachine reads the record afresh, lets fn change it and writes it back
// when fn says it changed something. fn sees the newest record, never one read
// earlier by the caller.
func (s *Service) updateMachine(ctx context.Context, id string, fn func(m *Machine) (changed bool)) (Machine, bool, error) {
	s.rec.Lock()
	defer s.rec.Unlock()
	m, err := s.o.Repo.GetMachine(ctx, id)
	if err != nil {
		return Machine{}, false, err
	}
	if !fn(&m) {
		return m, false, nil
	}
	return m, true, s.o.Repo.UpdateMachine(ctx, m)
}

// ---------------------------------------------------------------- secrets

func randomToken(prefix string, nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no system randomness: nothing safe can continue
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret is the stored form of a join code or machine key (R6.2). Both
// carry at least 160 bits of randomness, so a plain SHA-256 is enough.
func hashSecret(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func newID(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

// ---------------------------------------------------------------- join codes

// CreatedCode is a new join code. Code is shown once and never stored.
type CreatedCode struct {
	Code     string
	JoinCode JoinCode
}

// CreateJoinCode makes a one-time code that lives ttl (default 15 minutes,
// ceiling 24 hours) and makes every older unused code invalid (R5.4). The code
// has 160 bits of entropy (R6.3).
func (s *Service) CreateJoinCode(ctx context.Context, actor string, ttl time.Duration) (CreatedCode, error) {
	if ttl == 0 {
		ttl = DefaultCodeTTL
	}
	if ttl < 0 || ttl > MaxCodeTTL {
		return CreatedCode{}, fmt.Errorf("%w: a join code lives at most 24 hours", ErrInvalid)
	}
	now := s.now()
	if err := s.o.Repo.RevokeOpenJoinCodes(ctx, now); err != nil {
		return CreatedCode{}, err
	}
	code := randomToken("", 20)
	jc := JoinCode{ID: newID("jc_"), Hash: hashSecret(code), Kind: CodeKindCode, CreatedBy: actor, CreatedAt: now, ExpiresAt: now.Add(ttl)}
	if err := s.o.Repo.InsertJoinCode(ctx, jc); err != nil {
		return CreatedCode{}, err
	}
	_ = s.o.Repo.Audit(ctx, actor, "join_code.create", jc.ID, "expires "+jc.ExpiresAt.Format(time.RFC3339))
	return CreatedCode{Code: code, JoinCode: jc}, nil
}

// GetJoinCode returns a code's record (never its value).
func (s *Service) GetJoinCode(ctx context.Context, id string) (JoinCode, CodeState, error) {
	c, err := s.o.Repo.GetJoinCode(ctx, id)
	if err != nil {
		return JoinCode{}, "", err
	}
	return c, c.State(s.now()), nil
}

// RevokeJoinCode withdraws an unused code.
func (s *Service) RevokeJoinCode(ctx context.Context, actor, id string) error {
	ok, err := s.o.Repo.RevokeJoinCode(ctx, id, s.now())
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the code is used, withdrawn or unknown", ErrNotFound)
	}
	_ = s.o.Repo.Audit(ctx, actor, "join_code.revoke", id, "")
	return nil
}

// LocalCredential makes the one-time credential the controller writes for its
// own machine's agent (R5.1): same Join call, no code typed. It lives ten
// minutes and does not revoke other codes.
func (s *Service) LocalCredential(ctx context.Context) (string, error) {
	now := s.now()
	cred := randomToken("", 20)
	jc := JoinCode{ID: newID("jc_"), Hash: hashSecret(cred), Kind: CodeKindLocal, CreatedBy: "controller", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	if err := s.o.Repo.InsertJoinCode(ctx, jc); err != nil {
		return "", err
	}
	return cred, nil
}

// InstallCommand is one install command for one OS (R5.3).
type InstallCommand struct {
	OS      string
	Command string
}

// InstallCommands builds the command per OS. It carries the controller
// address, the agent port, the certificate fingerprint and, when given, the
// join code (R5.3). With no code the placeholder says where to put it.
//
// When this controller has agent installers and a console address a machine can
// reach (CRSP304), the Linux and macOS commands first download the agent
// installer from that LAN link, check its SHA-256 and install it if crsp is
// missing, and the Windows command does the same in PowerShell, so one pasted
// command is enough. The commands of an SSH join are the plain ones.
func (s *Service) InstallCommands(code string) []InstallCommand {
	cmds := s.baseCommands(code)
	if s.o.AgentInstallers == nil || s.o.ConsoleURL == "" {
		return cmds
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := range cmds {
		if wrapped, err := s.manualInstall(ctx, cmds[i].OS, cmds[i].Command); err == nil {
			cmds[i].Command = wrapped
		}
	}
	return cmds
}

func (s *Service) baseCommands(code string) []InstallCommand {
	addr := fmt.Sprintf("%s:%d", s.o.ControllerAddress, s.o.AgentPort)
	if code == "" {
		code = "<join code>"
	}
	args := fmt.Sprintf("agent install --controller %s --fingerprint %s --join-code %s", addr, s.o.Fingerprint, code)
	return []InstallCommand{
		{OS: "windows", Command: "crsp.exe " + args},
		{OS: "linux", Command: "sudo crsp " + args},
		{OS: "macos", Command: "sudo crsp " + args},
	}
}

// ControllerInfo is what an install command carries.
func (s *Service) ControllerInfo() (address string, port int, fingerprint string) {
	return s.o.ControllerAddress, s.o.AgentPort, s.o.Fingerprint
}

// ---------------------------------------------------------------- join

// JoinInput is a join request.
type JoinInput struct {
	Code         string
	Local        bool // the code is the local credential (R5.1)
	Facts        Facts
	AgentVersion string
	// RemoteAddr is the caller's address, for the failed-attempt limit (R6.3).
	RemoteAddr string
}

// JoinResult is what the agent gets once (R5.6, R5.7).
type JoinResult struct {
	MachineID string
	// Key is the machine key, shown here only; the controller keeps its hash.
	Key         string
	Fingerprint string
	Settings    Settings
	Machine     Machine
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func (s *Service) joinBlocked(addr string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return now.Before(s.blocked[addr])
}

func (s *Service) joinFailed(addr string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.fails[addr][:0]
	for _, t := range s.fails[addr] {
		if now.Sub(t) < joinFailWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	s.fails[addr] = kept
	if len(kept) >= joinFailLimit {
		s.blocked[addr] = now.Add(joinBlockFor)
		s.fails[addr] = nil
	}
}

// Join checks the code, uses it up and issues a key for this machine only
// (R5.6). A used, expired or unknown code is refused with the reason. After 10
// failed attempts from one address in a minute, that address is refused for 5
// minutes (R6.3). A removed machine joins again only with a new code and gets
// a new key (R5.43).
func (s *Service) Join(ctx context.Context, in JoinInput) (JoinResult, error) {
	now := s.now()
	addr := hostOf(in.RemoteAddr)
	if s.joinBlocked(addr, now) {
		return JoinResult{}, &JoinError{Reason: "too many failed attempts from this address; try again in 5 minutes"}
	}
	fail := func(reason string) (JoinResult, error) {
		s.joinFailed(addr, now)
		return JoinResult{}, &JoinError{Reason: reason}
	}
	if strings.TrimSpace(in.Code) == "" {
		return fail("a join code is required")
	}
	hash := hashSecret(strings.TrimSpace(in.Code))
	jc, err := s.o.Repo.FindJoinCodeByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fail("this code is not known")
		}
		return JoinResult{}, err
	}
	wantKind := CodeKindCode
	if in.Local {
		wantKind = CodeKindLocal
	}
	if jc.Kind != wantKind {
		return fail("this code is not known")
	}
	switch jc.State(now) {
	case CodeJoined:
		return fail("this code was already used")
	case CodeRevoked:
		return fail("this code was withdrawn")
	case CodeExpired:
		return fail("this code expired at " + jc.ExpiresAt.Local().Format("15:04"))
	}

	f := in.Facts
	if f.OS == "" || f.Arch == "" {
		return JoinResult{}, fmt.Errorf("%w: the agent sent no OS or architecture", ErrInvalid)
	}
	if in.Local {
		// A crsp-agent service on this host that already joined wins; the
		// built-in agent does not make a second machine (R5.1). The credential
		// is not used up, so a later start can still join.
		if name, dup, err := s.separateAgentOnHost(ctx, f.HostName); err != nil {
			return JoinResult{}, err
		} else if dup {
			return JoinResult{}, &JoinError{Reason: LocalDuplicateMarker + " " + name + " (a separately installed agent)"}
		}
	}
	// The same machine joining again keeps its record, settings and runners (OPS59).
	prev, rejoin, err := s.rejoinTarget(ctx, in)
	if err != nil {
		return JoinResult{}, err
	}
	id := newID("m_")
	if rejoin {
		id = prev.ID
	}
	used, err := s.o.Repo.UseJoinCode(ctx, hash, id, now)
	if err != nil {
		return JoinResult{}, err
	}
	if !used {
		return fail("this code was already used")
	}
	key := randomToken("crsp_mk_", 32)
	if rejoin {
		return s.finishRejoin(ctx, prev, in, key)
	}
	m := Machine{
		ID: id, OS: f.OS, Arch: f.Arch, Facts: f, State: StateActive, IsController: in.Local,
		AgentVersion: in.AgentVersion, JoinedAt: now, Accepting: true,
	}
	set := DefaultSettings()
	set.JobSlots = defaultSlots(f)
	if s.o.PoolDefaults != nil {
		if d, err := s.o.PoolDefaults(ctx); err == nil {
			set.BudgetMemoryPct, set.BudgetCPUPct, set.DrainTimeoutMinutes = d.BudgetMemoryPct, d.BudgetCPUPct, d.DrainTimeoutMinutes
			set.JobSlots = d.JobSlots
			if d.SizeSlotsFromMachine {
				set.JobSlots = defaultSlotsFor(f, d.BudgetMemoryPct, d.BudgetCPUPct, d.JobSlots)
			}
		}
	}
	base := strings.TrimSpace(f.HostName)
	if base == "" {
		base = f.OS + "-machine"
	}
	for n := 1; ; n++ {
		m.Name = base
		if n > 1 {
			m.Name = fmt.Sprintf("%s-%d", base, n)
		}
		if err = s.o.Repo.InsertMachine(ctx, m, set, hashSecret(key)); !errors.Is(err, ErrConflict) {
			break
		}
		if n > 50 {
			return JoinResult{}, fmt.Errorf("%w: no free machine name", ErrConflict)
		}
	}
	if err != nil {
		return JoinResult{}, err
	}
	_ = s.o.Repo.Audit(ctx, "agent", "machine.join", m.ID, "name "+m.Name)
	s.notifyMachine(m.ID)
	return JoinResult{MachineID: id, Key: key, Fingerprint: s.o.Fingerprint, Settings: set, Machine: m}, nil
}

// LocalDuplicateMarker starts the reason of a refused local join when a separately
// installed agent of this host already joined.
const LocalDuplicateMarker = "this computer is already in the pool as"

// SeparateAgentOnHost reports the name of a machine that is not the
// controller's own and carries the host name given: a crsp-agent service
// installed on the controller's PC (R5.1). Host names compare without case.
func (s *Service) SeparateAgentOnHost(ctx context.Context, host string) (name string, found bool, err error) {
	return s.separateAgentOnHost(ctx, host)
}

func (s *Service) separateAgentOnHost(ctx context.Context, host string) (string, bool, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", false, nil
	}
	ms, err := s.o.Repo.ListMachines(ctx)
	if err != nil {
		return "", false, err
	}
	for _, m := range ms {
		if !m.IsController && m.RemovedAt.IsZero() && strings.EqualFold(strings.TrimSpace(m.Facts.HostName), host) {
			return m.Name, true, nil
		}
	}
	return "", false, nil
}

// defaultSlots sizes slots from the budget like dev-standards (DL79, DL176)
// when the facts say how big the machine is; the default 2 otherwise.
func defaultSlots(f Facts) int {
	d := DefaultSettings()
	return defaultSlotsFor(f, d.BudgetMemoryPct, d.BudgetCPUPct, d.JobSlots)
}

// defaultSlotsFor sizes slots from the facts within the given budget; unknown
// facts give fallback.
func defaultSlotsFor(f Facts, memPct, cpuPct, fallback int) int {
	if f.Threads <= 0 || f.MemoryBytes <= 0 {
		return fallback
	}
	cpus := f.Threads * cpuPct / 100
	byMem := int(float64(f.MemoryBytes) / (1 << 30) * float64(memPct) / 100 / 1.5)
	n := cpus
	if byMem < n {
		n = byMem
	}
	if n < 1 {
		n = 1
	}
	if n > 64 {
		n = 64
	}
	return n
}

// Authenticate checks a machine key (and, when given, the machine id the call
// names). It refuses an unknown or revoked key (R5.8).
func (s *Service) Authenticate(ctx context.Context, key, machineID string) (Machine, error) {
	if key == "" {
		return Machine{}, ErrUnauthenticated
	}
	m, err := s.o.Repo.MachineByKey(ctx, hashSecret(key))
	if err != nil {
		return Machine{}, err
	}
	if machineID != "" && machineID != m.ID {
		return Machine{}, ErrUnauthenticated
	}
	return m, nil
}

func (s *Service) notifyMachine(id string) {
	if s.o.Notify != nil {
		s.o.Notify.MachineChanged(id)
	}
}

// rejoinTarget finds the machine a join belongs to when the same machine joins
// again (machines R5.70). The controller's own agent (the local credential) is
// the controller machine. Any other agent matches a machine of the same host
// name, OS and architecture that is offline: its key was refused or lost, so
// it has no session. A removed machine never matches (R5.43).
func (s *Service) rejoinTarget(ctx context.Context, in JoinInput) (Machine, bool, error) {
	ms, err := s.o.Repo.ListMachines(ctx)
	if err != nil {
		return Machine{}, false, err
	}
	host := strings.TrimSpace(in.Facts.HostName)
	var best Machine
	found := false
	for _, m := range ms {
		if !m.RemovedAt.IsZero() {
			continue
		}
		switch {
		case in.Local:
			if !m.IsController {
				continue
			}
		default:
			if m.IsController || m.Online || host == "" || !strings.EqualFold(strings.TrimSpace(m.Facts.HostName), host) ||
				m.OS != in.Facts.OS || m.Arch != in.Facts.Arch {
				continue
			}
		}
		if !found || m.LastSeen.After(best.LastSeen) {
			best, found = m, true
		}
	}
	return best, found, nil
}

// finishRejoin gives the machine a new key and refreshes its facts; its id,
// name, settings, policy and runners stay as they were.
func (s *Service) finishRejoin(ctx context.Context, prev Machine, in JoinInput, key string) (JoinResult, error) {
	if err := s.o.Repo.RevokeKeys(ctx, prev.ID); err != nil {
		return JoinResult{}, err
	}
	if err := s.o.Repo.AddKey(ctx, prev.ID, hashSecret(key)); err != nil {
		return JoinResult{}, err
	}
	m := prev
	m.Facts, m.AgentVersion, m.Online, m.RejoinedAt = in.Facts, in.AgentVersion, false, s.now()
	if err := s.o.Repo.UpdateMachine(ctx, m); err != nil {
		return JoinResult{}, err
	}
	set, err := s.o.Repo.GetSettings(ctx, m.ID)
	if err != nil {
		return JoinResult{}, err
	}
	_ = s.o.Repo.Audit(ctx, "agent", "machine.rejoin", m.ID, "name "+m.Name+", kept its settings and runners")
	s.notifyMachine(m.ID)
	return JoinResult{MachineID: m.ID, Key: key, Fingerprint: s.o.Fingerprint, Settings: set, Machine: m}, nil
}
