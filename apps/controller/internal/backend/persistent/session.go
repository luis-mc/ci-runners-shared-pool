package persistent

import (
	"context"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"strings"
	"time"
)

// After a restart (container pool start, VM restart, service restart) GitHub
// may still hold the runner's session from before the stop. Every listener then
// logs "A session for this runner already exists" until GitHub drops it, about
// two minutes in the field (handover lesson 29, runners R5.67). Inside a
// bounded window that is expected: the supervisor waits quietly and never
// starts a second listener. Past the window it is a real fault.

// SessionExistsText is the listener's refusal, matched without case.
const SessionExistsText = "a session for this runner already exists"

// DefaultSessionWindow is how long a stale session is expected after a restart
// (the pool setting session_wait_minutes, default 6). On 20 Nov a hard Docker
// restart kept every listener waiting for about 4 minutes, so 3 minutes was too
// short and showed a fault for what was only a wait (runners R5.95).
const DefaultSessionWindow = 6 * time.Minute

// WaitReason is the words shown on the runner while GitHub holds its old
// session. It is a wait, never an error (runners R5.95).
const WaitReason = runners.SessionWaitReason

// WaitText is WaitReason with the end of the wait window.
func WaitText(until time.Time) string { return runners.SessionWaitText(until) }

// Backoff is how long to wait before the next look at a refused session:
// 5 seconds, doubled each time, never more than a minute. The listener is
// never restarted in between (runners R5.46, R5.95).
func Backoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 0; i < attempt && d < time.Minute; i++ {
		d *= 2
	}
	return min(d, time.Minute)
}

// ErrSessionStale means the session is still refused after the window.
var ErrSessionStale = errors.New("GitHub still holds a session of this runner after the wait window")

// IsSessionExists says whether a listener output line is the stale-session refusal.
func IsSessionExists(line string) bool {
	return strings.Contains(strings.ToLower(line), SessionExistsText)
}

// SessionState is what a listener output means at one moment.
type SessionState int

// The states of a stale-session check.
const (
	SessionClear   SessionState = iota // no refusal in the output
	SessionExpired                     // refusal inside the window: expected, wait
	SessionFault                       // refusal past the window: a real fault
)

// InSessionWindow says whether now is inside the window that starts at restart.
// An unknown restart time (zero) is never inside the window.
func InSessionWindow(restart, now time.Time, window time.Duration) bool {
	if window <= 0 {
		window = DefaultSessionWindow
	}
	return !restart.IsZero() && !now.Before(restart) && now.Sub(restart) <= window
}

// SessionGuard supervises one runner's listener output after a restart.
type SessionGuard struct {
	Runner  string
	Restart time.Time     // when the pool, VM or service was started
	Window  time.Duration // zero means DefaultSessionWindow
	Now     func() time.Time
	Log     func(string) // may be nil
	// List returns the runner's listener process ids (R5.46).
	List func(context.Context) ([]int, error)

	announced bool
	attempts  int
}

// NextWait is the delay before the supervisor looks again after a refused
// session (Backoff grows with each refusal). A clear look starts it over.
func (g *SessionGuard) NextWait() time.Duration {
	d := Backoff(g.attempts)
	g.attempts++
	return d
}

func (g *SessionGuard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Until is the end of the window.
func (g *SessionGuard) Until() time.Time {
	w := g.Window
	if w <= 0 {
		w = DefaultSessionWindow
	}
	return g.Restart.Add(w)
}

// Observe reads listener output. Inside the window it logs one line (once) and
// returns SessionExpired; past it, SessionFault with ErrSessionStale.
func (g *SessionGuard) Observe(output string) (SessionState, error) {
	found := false
	for _, l := range strings.Split(output, "\n") {
		if IsSessionExists(l) {
			found = true
			break
		}
	}
	if !found {
		g.attempts = 0
		return SessionClear, nil
	}
	if InSessionWindow(g.Restart, g.now(), g.Window) {
		if !g.announced && g.Log != nil {
			g.Log(fmt.Sprintf("runner %s: %s", g.Runner, WaitText(g.Until())))
		}
		g.announced = true
		return SessionExpired, nil
	}
	return SessionFault, fmt.Errorf("%w (runner %s, waited until %s)", ErrSessionStale, g.Runner, g.Until().UTC().Format("15:04:05Z"))
}

// Start runs start only when no listener of the runner is alive. A stale
// session never starts a second listener (R5.60).
func (g *SessionGuard) Start(ctx context.Context, start func(context.Context) error) error {
	if err := CheckNoListener(ctx, g.List); err != nil {
		return err
	}
	return start(ctx)
}
