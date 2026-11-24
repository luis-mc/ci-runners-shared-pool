package update

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Follow-the-controller (operations R5.39 to R5.41). An agent-only machine
// updates to the version its controller runs, with the same rules as the
// controller's own update: verified downloads, a drain that never cuts a job
// and gives up after the machine's drain timeout (machines R5.33, 60 minutes by default), three tries per version, and the per-OS apply.

// FollowActor is the actor name of a follow update; like the automatic update
// it waits at most the machine's drain timeout for running jobs.
const FollowActor = "follow-controller"

// ErrUnprivileged says this process cannot install a package or an MSI, so it
// cannot follow (an agent that runs as an unprivileged account, machines R5.44).
var ErrUnprivileged = errors.New("this service runs as an account that cannot install updates")

// Pinner is a Source that can be told which release to look up.
type Pinner interface{ Pin(version string) }

// PinnedSource looks up one given release by its tag instead of the latest one.
type PinnedSource struct {
	Base GitHubSource
	mu   sync.Mutex
	ver  string
}

// Pin sets the version Latest returns.
func (p *PinnedSource) Pin(version string) {
	p.mu.Lock()
	p.ver = version
	p.mu.Unlock()
}

// Latest implements Source.
func (p *PinnedSource) Latest(ctx context.Context) (Release, error) {
	p.mu.Lock()
	g := p.Base
	g.Version = p.ver
	p.mu.Unlock()
	if g.Version == "" {
		return Release{}, ErrNoRelease
	}
	return g.Latest(ctx)
}

// Privileged says whether this process may install updates: root on unix, an
// elevated token (LocalSystem) on Windows.
func Privileged() bool { return privileged() }

// Follow starts an update to version, the controller's. It does nothing when
// this build is not older, when the retry rule says to wait, or when an update
// runs already. It downloads, verifies, drains (cancelled after the machine's drain timeout,
// then the next ask tries again) and installs when idle.
func (m *Manager) Follow(ctx context.Context, version string) (Status, error) {
	if !Newer(m.o.Current, version) {
		return m.Status(ctx), ErrNoUpdate
	}
	if !m.o.Privileged() {
		return m.Status(ctx), ErrUnprivileged
	}
	if ok, why := AutoAllowed(readFailures(m.o.Dir), version, m.o.Now()); !ok {
		m.setError(why)
		return m.Status(ctx), nil
	}
	if p, ok := m.o.Source.(Pinner); ok {
		p.Pin(version)
	}
	if _, err := m.checkOnly(ctx); err != nil {
		return m.Status(ctx), err
	}
	if st := m.Status(ctx); st.Latest != version {
		return st, errors.New("GitHub has no release " + version + " yet")
	}
	return m.begin(ctx, KindUpdate, FollowActor)
}

// RestartActor is the actor name of a restart the controller asked for: the
// owner pressed Restart the service on this machine's page in the console.
const RestartActor = "console-restart"

// Restart restarts this machine's service after the running jobs end (machines
// R5.81, CRSP235). It is the agent's answer to the console's Restart the
// service: the same drain as an update, never a cut job, the per-OS restart of
// the install helper.
func (m *Manager) Restart(ctx context.Context) (Status, error) {
	if !m.o.Privileged() {
		return m.Status(ctx), ErrUnprivileged
	}
	return m.Apply(ctx, KindRestart, true, RestartActor)
}

func autoActor(a string) bool { return a == "auto-update" || a == FollowActor }

// ServiceActiveCommand is the command whose exit code says the service runs.
func ServiceActiveCommand(goos, service string) (string, []string) {
	switch goos {
	case "windows":
		return "powershell", []string{"-NoProfile", "-NonInteractive", "-Command", "if ((Get-Service -Name '" + service + "').Status -eq 'Running') { exit 0 } else { exit 1 }"}
	case "darwin":
		return "launchctl", []string{"print", "system/" + service}
	}
	return "systemctl", []string{"is-active", "--quiet", service}
}

// waitServiceActive is the health check of a machine without a console port
// (an agent): the service must be active, and still be after a settle time.
func waitServiceActive(ctx context.Context, p Plan, env *Env) error {
	limit := p.HealthTimeout
	if limit <= 0 {
		limit = 60 * time.Second
	}
	deadline := env.Now().Add(limit)
	name, args := ServiceActiveCommand(p.GOOS, p.Service)
	active := func() bool {
		code, err := env.Exec(ctx, name, args...)
		return err == nil && code == 0
	}
	for {
		if active() {
			env.Sleep(10 * time.Second)
			if active() {
				return nil
			}
		}
		if !env.Now().Before(deadline) || ctx.Err() != nil {
			return errNotHealthy
		}
		env.Sleep(2 * time.Second)
	}
}
