// Package update is the self-update and the service restart of the controller
// (operations R5.11 to R5.18, tray R5.15 to R5.18).
//
// The flow: look at GitHub Releases for a newer stable release; download the
// asset for this OS and architecture (the MSI on Windows, crsp-<os>-<arch>_<version>.tar.gz
// elsewhere) and SHA256SUMS; verify the SHA-256 and the build provenance
// attestation; then drain (no new jobs, wait for running jobs up to the drain
// timeout) and only then hand the install to a helper process that survives the
// service stopping. A running job is never cut: the install runs when the
// machine is idle, and an idle check runs again right before it.
package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Kind is what a drain is for.
type Kind string

// Kinds.
const (
	KindUpdate  Kind = "update"
	KindRestart Kind = "restart"
)

// State is where an update or restart stands.
type State string

// States, as the API shows them.
const (
	StateIdle           State = "idle"
	StateChecking       State = "checking"
	StateWaitingForJobs State = "waiting_for_jobs"
	StateDownloading    State = "downloading"
	StateInstalling     State = "installing"
	StateRestarting     State = "restarting"
	StateFailed         State = "failed"
)

// Job is a running job the drain waits for (named on the Review step).
type Job struct {
	Repository string
	Workflow   string
	RunID      int64
	Job        string
}

// Status is a snapshot for the API, the console and the tray.
type Status struct {
	Current             string
	Latest              string
	Available           bool
	State               State
	AutoUpdate          bool
	CheckedAt           time.Time
	Error               string
	RunningJobs         []Job
	DrainTimeoutMinutes int
	ReleaseURL          string
	// Stage is the live stage file (TRAY4); nil when no update ran yet.
	Stage *Stage
	// Attempt is the update or restart that runs or ran last; nil when none did (CRSP205).
	Attempt *Attempt
	// ErrorAttempt is the id of the attempt Error belongs to ("earlier" for a failure that has no id).
	ErrorAttempt string
	// ServiceStarted is when this process started; ServerTime is the clock now.
	ServiceStarted time.Time
	ServerTime     time.Time
}

// Attempt is one update or restart from the yes to its end. Its id tells a page
// which outcome is its own, so an older update's failure never shows on a restart.
type Attempt struct {
	ID        string
	Kind      Kind
	StartedAt time.Time
}

// Drainer is the machines side: it names the jobs on this machine and drains it.
// BeginDrain starts a drain; when the machine is idle the machines service
// calls Manager.OnIdle. The drain never stops a running job.
type Drainer interface {
	RunningJobs(ctx context.Context) ([]Job, error)
	BeginDrain(ctx context.Context, kind Kind, actor string) error
	DrainTimeoutMinutes(ctx context.Context) int
	// CancelDrain ends a drain and lets the machine take jobs again.
	CancelDrain(ctx context.Context, actor string) error
}

// NeedsConfirmation is returned for a request without confirmed=true (R5.16).
type NeedsConfirmation struct {
	Kind Kind
	Jobs []Job
}

func (e *NeedsConfirmation) Error() string {
	return fmt.Sprintf("a %s waits for %d running job(s) and needs a yes", e.Kind, len(e.Jobs))
}

// Errors the API maps.
var (
	ErrNoUpdate = errors.New("this is already the newest stable release")
	ErrBusy     = errors.New("an update or restart is already running")
	ErrNotIdle  = errors.New("a job started before the install; the drain goes on waiting")
	// ErrCancelled ends an update the person cancelled before the install began.
	ErrCancelled = errors.New("the update was cancelled; nothing changed")
	// ErrNotCancellable says the update is past the point where it can stop.
	ErrNotCancellable = errors.New("no update can be cancelled now: it only stops while it downloads or waits for jobs")
)

// Options configure a Manager.
type Options struct {
	Current  string
	Source   Source
	Attester Attester
	Drainer  Drainer
	// AutoUpdate reads the setting (default on).
	AutoUpdate func(ctx context.Context) bool
	// Dir is the work folder (the data folder's updates/).
	Dir       string
	HealthURL string
	Service   string
	GOOS      string
	// InstallKind is msi, pkg, deb, rpm or tarball; empty reads the install-kind marker next to Exe.
	InstallKind string
	// AutoDrainLimit is how long an automatic update waits for running jobs. Zero
	// (the product default) takes the machine's drain timeout setting, Drainer.
	// DrainTimeoutMinutes (machines R5.33), and 60 minutes when that is unset.
	AutoDrainLimit time.Duration
	GOARCH         string
	Exe            string
	Logger         *slog.Logger
	Now            func() time.Time

	// Seams for tests.
	AllowURL  func(string) bool
	Fetch     func(ctx context.Context, url, dst string) error
	Extract   func(archive, dst string) error
	SelfCheck func(ctx context.Context, bin, version string) error
	Spawn     func(ctx context.Context, exe string, p Plan, planPath string) (int, error)
	// StartMSI starts msiexec detached (Windows update); MSILog is its log path (default %ProgramData%\crsp\logs).
	StartMSI func(ctx context.Context, msi, log string) (int, error)
	MSILog   string
	// InstallerBusy says another installer holds Windows Installer, and names it when it can
	// (TRAY11). ProcessAlive says the msiexec this controller started still runs. Sleep waits;
	// tests give all three a fake clock.
	InstallerBusy func() (busy bool, name string)
	ProcessAlive  func(pid int) bool
	Sleep         func(time.Duration)
	// AfterInstall runs once at the first start after an update that succeeded, with the new
	// version. On Windows it starts the tray for each signed-in user (CRSP147).
	AfterInstall func(version string)
	// Privileged says whether this process may install (default: root or an elevated token).
	Privileged func() bool
	Pid        func() int
}

// Manager runs checks, updates and restarts. It is safe for concurrent use.
type Manager struct {
	o Options

	mu      sync.Mutex
	state   State
	latest  Release
	checked time.Time
	errText string
	pending *pending
	lock    *Lock
	timer   *time.Timer
	// cancelDL ends the download that runs now; cancelled says the person asked to cancel (D15).
	cancelDL  context.CancelFunc
	cancelled bool
	startedBy string
	// attempt is the update or restart that runs now (CRSP205): its id, kind and the time of the yes.
	attempt        string
	attemptKind    Kind
	attemptStarted time.Time
	// errAttempt is the attempt errText belongs to; empty for an error of no attempt of this process.
	errAttempt string
	// started is when this process started: a restart is over once it is later than the attempt's yes.
	started time.Time
}

type pending struct {
	kind    Kind
	version string
	file    string
}

// New returns a Manager. A missing option takes the OS default.
func New(o Options) *Manager {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.InstallKind == "" {
		o.InstallKind = InstallKindFor(o.Exe, o.GOOS)
	}
	if o.Service == "" {
		o.Service = "crsp"
	}
	if o.AllowURL == nil {
		o.AllowURL = allowedHost
	}
	if o.Fetch == nil {
		o.Fetch = func(ctx context.Context, url, dst string) error { return httpFetch(ctx, url, dst) }
	}
	if o.Extract == nil {
		o.Extract = ExtractBinary
	}
	if o.SelfCheck == nil {
		o.SelfCheck = selfCheck
	}
	if o.Spawn == nil {
		o.Spawn = Spawn
	}
	if o.Privileged == nil {
		o.Privileged = Privileged
	}
	if o.StartMSI == nil {
		o.StartMSI = StartMSIDetached
	}
	if o.InstallerBusy == nil {
		o.InstallerBusy = InstallerBusy
	}
	if o.ProcessAlive == nil {
		o.ProcessAlive = pidAlive
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	if o.Pid == nil {
		o.Pid = os.Getpid
	}
	if o.AutoUpdate == nil {
		o.AutoUpdate = func(context.Context) bool { return true }
	}
	m := &Manager{o: o, state: StateIdle, started: o.Now()}
	if settled, res := recoverMSI(o.Dir, o.Current, o.Now()); settled && res.OK && o.AfterInstall != nil {
		// The new build runs: the old tray was ended by the MSI, so start the tray again (CRSP147).
		go o.AfterInstall(res.To)
	}
	if r, ok := ReadResult(o.Dir); ok && r.OK && r.To == o.Current {
		// The update worked: keep the folders of the version that runs and the one before it (TRAY8).
		PruneVersions(o.Dir, r.To, r.From)
	}
	m.settleStaleAttempt()
	if r, ok := ReadResult(o.Dir); ok && !r.OK && r.To != o.Current {
		m.state, m.errText, m.errAttempt = StateFailed, "The last update to "+r.To+" failed: "+r.Error, r.Attempt
		if m.errAttempt == "" {
			m.errAttempt = "earlier" // a failure from before attempts had ids belongs to no attempt a page started
		}
	}
	return m
}

// Status returns the current snapshot.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	st := Status{
		Current: m.o.Current, Latest: m.latest.Version, State: m.state, CheckedAt: m.checked,
		Error: m.errText, ReleaseURL: m.latest.URL, ErrorAttempt: m.errAttempt, ServiceStarted: m.started, ServerTime: m.o.Now(),
	}
	if m.attempt != "" {
		st.Attempt = &Attempt{ID: m.attempt, Kind: m.attemptKind, StartedAt: m.attemptStarted}
	}
	m.mu.Unlock()
	if sg, ok := ReadStage(m.o.Dir); ok {
		st.Stage = &sg
		if st.Attempt == nil && sg.Attempt != "" {
			// A new process after the restart: the stage file still names the attempt.
			st.Attempt = &Attempt{ID: sg.Attempt, Kind: Kind(sg.AttemptKind), StartedAt: sg.AttemptStarted}
		}
	}
	st.Available = Newer(st.Current, st.Latest)
	st.AutoUpdate = m.o.AutoUpdate(ctx)
	if m.o.Drainer != nil {
		if jobs, err := m.o.Drainer.RunningJobs(ctx); err == nil {
			st.RunningJobs = jobs
		}
		st.DrainTimeoutMinutes = m.o.Drainer.DrainTimeoutMinutes(ctx)
	}
	return st
}

func (m *Manager) busy() bool {
	switch m.state {
	case StateChecking, StateWaitingForJobs, StateDownloading, StateInstalling, StateRestarting:
		return true
	}
	return false
}

// Check looks for a newer stable release now. It downloads nothing and never
// starts an install, whatever the Automatic updates setting says: a person who
// asked to look is only told (CRSP147). Installing takes Apply after a yes.
func (m *Manager) Check(ctx context.Context) (Status, error) {
	return m.check(ctx, false)
}

// AutoCheck is the scheduled look: like Check, and with auto-update on and a
// newer release the update then starts by itself (and drains first); a refused
// or failed automatic try is shown in Status.Error.
func (m *Manager) AutoCheck(ctx context.Context) (Status, error) {
	return m.check(ctx, true)
}

func (m *Manager) check(ctx context.Context, auto bool) (Status, error) {
	m.mu.Lock()
	if m.busy() {
		m.mu.Unlock()
		return m.Status(ctx), nil
	}
	prev := m.state
	m.state = StateChecking
	m.mu.Unlock()

	rel, err := m.o.Source.Latest(ctx)

	m.mu.Lock()
	m.checked = m.o.Now()
	switch {
	case err != nil && !errors.Is(err, ErrNoRelease):
		m.state, m.errText = StateFailed, "Could not look for updates: "+err.Error()
	case err == nil:
		m.latest = rel
		m.state = prev
		if prev == StateFailed || m.errText != "" {
			m.state, m.errText = StateIdle, ""
		}
	default:
		m.state = StateIdle
	}
	m.mu.Unlock()
	if err != nil && !errors.Is(err, ErrNoRelease) {
		return m.Status(ctx), err
	}

	if st := m.Status(ctx); auto && st.Available && st.AutoUpdate {
		if ok, why := AutoAllowed(readFailures(m.o.Dir), st.Latest, m.o.Now()); !ok {
			m.setError(why)
			return m.Status(ctx), nil
		}
		if _, err := m.begin(ctx, KindUpdate, "auto-update"); err != nil && !errors.Is(err, ErrBusy) {
			m.o.Logger.Warn("automatic update did not start", "err", err)
		}
	}
	return m.Status(ctx), nil
}

// stage records the live stage for the API and the console page. A stage that cannot be
// written is only logged: it must never stop an update.
func (m *Manager) stage(s Stage) {
	s.At = m.o.Now()
	m.mu.Lock()
	s.StartedBy = m.startedBy
	s.Attempt, s.AttemptKind, s.AttemptStarted = m.attempt, string(m.attemptKind), m.attemptStarted
	m.mu.Unlock()
	if s.StartedBy == "" || s.Attempt == "" { // a new process after the restart: keep what the first stage said
		if prev, ok := ReadStage(m.o.Dir); ok {
			if s.StartedBy == "" {
				s.StartedBy = prev.StartedBy
			}
			if s.Attempt == "" {
				s.Attempt, s.AttemptKind, s.AttemptStarted = prev.Attempt, prev.AttemptKind, prev.AttemptStarted
			}
		}
	}
	if err := WriteStage(m.o.Dir, s); err != nil {
		m.o.Logger.Warn("could not write the update stage", "err", err)
	}
}

// currentStage is the name of the stage that runs now ("" when none does), read before a failure replaces it.
func (m *Manager) currentStage() string {
	if s, ok := ReadStage(m.o.Dir); ok && s.Name != StageDone && s.Name != StageFailed {
		return s.Name
	}
	return ""
}

// logPath is the log a failed update points to.
func (m *Manager) logPath() string {
	if m.o.GOOS == "windows" && m.o.InstallKind == KindMSI {
		if m.o.MSILog != "" {
			return m.o.MSILog
		}
		return MSILogPath()
	}
	return filepath.Join(m.o.Dir, LogFile)
}

func readFailures(dir string) Failures { f, _ := ReadFailures(dir); return f }

func (m *Manager) setError(text string) {
	m.mu.Lock()
	m.errText = text
	m.errAttempt = m.attempt
	m.mu.Unlock()
}

// Apply updates or restarts after a yes. Without confirmed it changes nothing
// and returns *NeedsConfirmation with the running jobs (R5.16). With it, the
// machine drains first (R5.17); a running job is never cut.
func (m *Manager) Apply(ctx context.Context, kind Kind, confirmed bool, actor string) (Status, error) {
	if m.o.Drainer == nil {
		return Status{}, errors.New("this machine has no drain service")
	}
	jobs, err := m.o.Drainer.RunningJobs(ctx)
	if err != nil {
		return Status{}, err
	}
	if !confirmed {
		return Status{}, &NeedsConfirmation{Kind: kind, Jobs: jobs}
	}
	if kind == KindUpdate {
		// Make sure the release is known; a manual yes may come before the first look.
		if m.Status(ctx).Latest == "" {
			if _, err := m.checkOnly(ctx); err != nil {
				return Status{}, err
			}
		}
	}
	return m.begin(ctx, kind, actor)
}

// checkOnly refreshes the known release without starting an automatic update.
func (m *Manager) checkOnly(ctx context.Context) (Release, error) {
	rel, err := m.o.Source.Latest(ctx)
	if err != nil {
		return Release{}, err
	}
	m.mu.Lock()
	m.latest, m.checked = rel, m.o.Now()
	m.mu.Unlock()
	return rel, nil
}

// begin takes the lock, prepares (update: download and verify), and starts the drain.
func (m *Manager) begin(ctx context.Context, kind Kind, actor string) (Status, error) {
	m.mu.Lock()
	if m.busy() {
		m.mu.Unlock()
		return Status{}, ErrBusy
	}
	rel := m.latest
	if kind == KindUpdate && !Newer(m.o.Current, rel.Version) {
		m.mu.Unlock()
		return Status{}, ErrNoUpdate
	}
	lock, err := TakeLock(m.o.Dir, m.o.Now(), m.o.Pid(), nil)
	if err != nil {
		m.mu.Unlock()
		return Status{}, err
	}
	m.lock = lock
	m.errText, m.errAttempt = "", ""
	m.attempt, m.attemptKind, m.attemptStarted = newAttemptID(m.o.Now()), kind, m.o.Now()
	m.cancelled = false
	m.startedBy = StartedByActor(actor)
	if kind == KindUpdate {
		m.state = StateDownloading
	} else {
		m.state = StateWaitingForJobs
	}
	m.mu.Unlock()

	p := &pending{kind: kind, version: m.o.Current}
	if kind == KindUpdate {
		m.stage(Stage{Name: StageDownloading, Percent: 0, Version: rel.Version})
		last := -2
		dctx := WithProgress(ctx, func(done, total int64) {
			if pc := Percent(done, total); pc != last {
				last = pc
				m.stage(Stage{Name: StageDownloading, Percent: pc, Version: rel.Version})
			}
		})
		dctx, stop := context.WithCancel(dctx)
		m.mu.Lock()
		m.cancelDL = stop
		m.mu.Unlock()
		file, err := m.prepare(dctx, rel)
		stop()
		m.mu.Lock()
		m.cancelDL = nil
		was := m.cancelled
		m.mu.Unlock()
		if was {
			m.endCancelled(context.Background(), false, "")
			return m.Status(ctx), ErrCancelled
		}
		if err != nil {
			m.fail(err, rel.Version)
			return m.Status(ctx), err
		}
		p.version, p.file = rel.Version, file
	}
	m.mu.Lock()
	m.pending = p
	m.state = StateWaitingForJobs
	m.mu.Unlock()
	m.stage(Stage{Name: StageWaitingForJobs, Percent: -1, Version: p.version})
	if err := m.o.Drainer.BeginDrain(ctx, kind, actor); err != nil {
		m.fail(err, p.version)
		return m.Status(ctx), err
	}
	if autoActor(actor) {
		// An automatic update waits at most AutoDrainLimit; then it lets go and retries at the next check.
		m.mu.Lock()
		limit := m.autoDrainLimit(ctx)
		m.timer = time.AfterFunc(limit, func() { m.giveUpDrain(actor, limit) })
		m.mu.Unlock()
	}
	return m.Status(ctx), nil
}

// Cancel stops an update that has not begun to install: while it downloads or waits for
// the running jobs. Nothing has changed then, so the machine takes jobs again and no
// stage is left. From installing on it answers ErrNotCancellable (D15).
func (m *Manager) Cancel(ctx context.Context, actor string) (Status, error) {
	m.mu.Lock()
	switch {
	case m.state == StateDownloading:
		m.cancelled = true
		stop := m.cancelDL
		m.mu.Unlock()
		if stop != nil {
			stop()
		}
		// begin ends the download and cleans up; wait for it so the answer is the end state.
		for i := 0; i < 50; i++ {
			m.mu.Lock()
			idle := m.state != StateDownloading
			m.mu.Unlock()
			if idle {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		return m.Status(ctx), nil
	case m.state == StateWaitingForJobs && m.pending != nil:
		if m.timer != nil {
			m.timer.Stop()
		}
		m.pending = nil
		m.mu.Unlock()
		m.endCancelled(ctx, true, actor)
		return m.Status(ctx), nil
	}
	m.mu.Unlock()
	return m.Status(ctx), ErrNotCancellable
}

// endCancelled returns to idle after a cancel: the lock goes, the stage file goes, and a
// started drain ends (endDrain).
func (m *Manager) endCancelled(ctx context.Context, endDrain bool, actor string) {
	m.mu.Lock()
	m.state, m.errText, m.pending, m.cancelled = StateIdle, "", nil, false
	lock := m.lock
	m.lock = nil
	m.mu.Unlock()
	ClearStage(m.o.Dir)
	if endDrain {
		if err := m.o.Drainer.CancelDrain(ctx, actor); err != nil {
			m.o.Logger.Warn("could not cancel the drain", "err", err)
		}
	}
	lock.Release()
}

// autoDrainLimit is how long an automatic update waits: the explicit option,
// else the machine's drain timeout setting (machines R5.33), else 60 minutes.
// After it the drain is cancelled and no job is cut.
func (m *Manager) autoDrainLimit(ctx context.Context) time.Duration {
	if m.o.AutoDrainLimit > 0 {
		return m.o.AutoDrainLimit
	}
	if min := m.o.Drainer.DrainTimeoutMinutes(ctx); min > 0 {
		return time.Duration(min) * time.Minute
	}
	return 60 * time.Minute
}

func (m *Manager) giveUpDrain(actor string, limit time.Duration) {
	m.mu.Lock()
	if m.pending == nil || m.state != StateWaitingForJobs {
		m.mu.Unlock()
		return
	}
	m.pending, m.state = nil, StateIdle
	m.errText = "Running jobs did not end within " + limit.String() + ". The drain was cancelled and the machine takes jobs again. The update tries again at the next check."
	lock := m.lock
	m.lock = nil
	msg := m.errText
	m.mu.Unlock()
	m.stage(Stage{Name: StageFailed, Percent: -1, Error: msg, LogPath: m.logPath(), FailedAt: StageWaitingForJobs})
	if err := m.o.Drainer.CancelDrain(context.Background(), actor); err != nil {
		m.o.Logger.Warn("could not cancel the drain", "err", err)
	}
	lock.Release()
}

func (m *Manager) fail(err error, version string) {
	m.mu.Lock()
	m.state, m.errText, m.errAttempt, m.pending = StateFailed, err.Error(), m.attempt, nil
	lock := m.lock
	m.lock = nil
	m.mu.Unlock()
	lock.Release()
	if version != "" {
		_ = RecordFailure(m.o.Dir, version, m.o.Now(), false)
	}
	m.stage(Stage{Name: StageFailed, Percent: -1, Version: version, Error: err.Error(), LogPath: m.logPath(), FailedAt: m.currentStage()})
	m.o.Logger.Warn("update refused or failed", "err", err)
}

// prepare downloads the asset and SHA256SUMS, verifies both checks, and (on
// unix) extracts the binary and runs the self-check. It returns the file to install.
func (m *Manager) prepare(ctx context.Context, rel Release) (string, error) {
	asset := PickAsset(rel, AssetFor(m.o.InstallKind, m.o.GOOS, m.o.GOARCH))
	assetURL, ok := rel.Assets[asset]
	if !ok {
		return "", fmt.Errorf("release %s has no %s for this machine", rel.Version, asset)
	}
	sumsURL, ok := rel.Assets["SHA256SUMS"]
	if !ok {
		return "", &Failure{Check: "checksum", Reason: "release " + rel.Version + " has no SHA256SUMS"}
	}
	for _, u := range []string{assetURL, sumsURL} {
		if !m.o.AllowURL(u) {
			return "", fmt.Errorf("refusing to download from %s: not a GitHub release address", u)
		}
	}
	dir := filepath.Join(m.o.Dir, rel.Version)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	file, sumsFile := filepath.Join(dir, asset), filepath.Join(dir, "SHA256SUMS")
	if err := m.o.Fetch(ctx, assetURL, file); err != nil {
		return "", fmt.Errorf("download %s: %w", asset, err)
	}
	if err := m.o.Fetch(ctx, sumsURL, sumsFile); err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	sums, err := os.ReadFile(sumsFile) //nolint:gosec // work folder
	if err != nil {
		return "", err
	}
	if err := Verify(ctx, file, asset, sums, m.o.Attester); err != nil {
		return "", err
	}
	if m.o.InstallKind != KindTarball {
		return file, nil
	}
	bin := filepath.Join(filepath.Dir(m.o.Exe), filepath.Base(m.o.Exe)+".new")
	if err := m.o.Extract(file, bin); err != nil {
		return "", fmt.Errorf("unpack %s: %w", asset, err)
	}
	if err := m.o.SelfCheck(ctx, bin, rel.Version); err != nil {
		_ = os.Remove(bin)
		return "", fmt.Errorf("the new build failed its self-check: %w", err)
	}
	return bin, nil
}

// OnIdle is called by the machines service when the drain is complete. It
// checks once more that no job runs (R5.14), then hands the install to the
// helper process. After it returns nil the service is about to stop.
func (m *Manager) OnIdle(ctx context.Context, kind Kind) error {
	m.mu.Lock()
	p := m.pending
	if m.timer != nil {
		m.timer.Stop()
	}
	m.mu.Unlock()
	if p == nil || p.kind != kind {
		return nil // not our change
	}
	jobs, err := m.o.Drainer.RunningJobs(ctx)
	if err != nil {
		return err
	}
	if len(jobs) > 0 {
		return ErrNotIdle
	}
	plan := Plan{
		Kind: string(kind), GOOS: m.o.GOOS, InstallKind: m.o.InstallKind, From: m.o.Current, To: p.version, File: p.file, Exe: m.o.Exe,
		Service: m.o.Service, HealthURL: m.o.HealthURL, Dir: m.o.Dir, Attempt: m.attemptID(),
		HealthTimeout: 60 * time.Second,
	}
	if m.o.GOOS == "windows" {
		plan.HealthTimeout = 3 * time.Minute
	}
	if m.o.GOOS == "windows" && kind == KindUpdate {
		return m.onIdleMSI(ctx, p)
	}
	planPath := filepath.Join(m.o.Dir, PlanFile)
	if err := writeJSON(planPath, plan); err != nil {
		m.fail(err, p.version)
		return err
	}
	m.mu.Lock()
	m.state = StateInstalling
	lock := m.lock
	m.mu.Unlock()
	m.stage(Stage{Name: StageInstalling, Percent: -1, Version: p.version})
	pid, err := m.o.Spawn(ctx, m.o.Exe, plan, planPath)
	if err != nil {
		m.fail(fmt.Errorf("could not start the install helper: %w", err), p.version)
		return err
	}
	if pid > 0 && lock != nil {
		_ = lock.HandOver(pid, m.o.Now())
	}
	m.mu.Lock()
	m.state, m.pending = StateRestarting, nil
	m.mu.Unlock()
	m.stage(Stage{Name: StageRestarting, Percent: -1, Version: p.version})
	return nil
}

// onIdleMSI starts the MSI from the controller itself. When another installer holds Windows
// Installer it waits and retries (TRAY11, superviseMSI). The upgrade stops this process, and
// the next start reads the log (msi.go).
func (m *Manager) onIdleMSI(ctx context.Context, p *pending) error {
	m.mu.Lock()
	m.state, m.pending = StateInstalling, nil
	m.mu.Unlock()
	pid := 0
	if busy, _ := m.o.InstallerBusy(); !busy {
		var err error
		if pid, err = m.launchMSI(ctx, p); err != nil {
			m.fail(err, p.version)
			return err
		}
	}
	go m.superviseMSI(p, pid)
	return nil
}

// launchMSI starts msiexec now and hands the update lock to it.
func (m *Manager) launchMSI(ctx context.Context, p *pending) (int, error) {
	m.stage(Stage{Name: StageInstalling, Percent: 0, Version: p.version})
	pid, err := m.startMSI(ctx, p)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	lock := m.lock
	m.mu.Unlock()
	if pid > 0 && lock != nil {
		_ = lock.HandOver(pid, m.o.Now())
	}
	return pid, nil
}

// Run checks at start (after a minute) and then every interval, until ctx ends.
// Turning auto-update off keeps the checks (the console still shows the newer
// version) but starts no install.
func (m *Manager) Run(ctx context.Context, first, every time.Duration) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := m.AutoCheck(ctx); err != nil {
				m.o.Logger.Warn("update check failed", "err", err)
			}
			t.Reset(every)
		}
	}
}

// httpFetch downloads url to dst (temporary name, then rename), capped at 300 MiB.
func httpFetch(ctx context.Context, url, dst string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "crsp-updater")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // work folder
	if err != nil {
		return err
	}
	const max = 300 << 20
	n, err := io.Copy(f, withProgressReader(ctx, io.LimitReader(resp.Body, max+1), resp.ContentLength))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = errors.New("the download is larger than 300 MiB")
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// progressReader reports the bytes read to the context's progress function, at most once per percent.
type progressReader struct {
	r     io.Reader
	ctx   context.Context //nolint:containedctx // read-through wrapper, lives for one download
	total int64
	done  int64
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	reportProgress(p.ctx, p.done, p.total)
	return n, err
}

// withProgressReader wraps r when the context asks for download progress.
func withProgressReader(ctx context.Context, r io.Reader, total int64) io.Reader {
	if _, ok := ctx.Value(progressKey{}).(func(done, total int64)); !ok {
		return r
	}
	return &progressReader{r: r, ctx: ctx, total: total}
}

// newAttemptID names an update or restart by the time of its yes.
func newAttemptID(now time.Time) string { return "a" + strconv.FormatInt(now.UnixNano(), 36) }

func (m *Manager) attemptID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempt
}
