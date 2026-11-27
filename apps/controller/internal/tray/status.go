package tray

import (
	"fmt"
	"strings"
	"time"
)

// State is what the tray shows about the local service.
type State string

// States, spec section 4. The icon is the same disc with CRSP in it; the disc colour and the words name the state (R5.4, decision tray D8).
const (
	StateStarting State = "starting"
	StateHealthy  State = "ok"
	StateWaiting  State = "waiting"
	StateFailed   State = "failed"
	StateStopped  State = "stopped"
)

// MaxTooltip is the Windows tooltip limit in characters (R6.5).
const MaxTooltip = 127

// Glyph returns the UX-58 glyph of a state.
func Glyph(s State) string {
	switch s {
	case StateHealthy:
		return "✓"
	case StateWaiting:
		return "◷"
	case StateFailed, StateStopped:
		return "✕"
	default:
		return "▶"
	}
}

// Status is one reading of the local service.
type Status struct {
	State     State
	Running   int
	Waiting   int
	Runners   int       // runners configured; 0 with StateHealthy reads "no runners yet"
	Fault     string    // StateFailed: why
	StoppedAt time.Time // StateStopped
	ExitCode  int       // StateStopped
	Version   string    // the version the service runs, "" when it does not say
	Update    UpdateInfo
	Policy    MachinePolicy
	// RunnerProblems are the runner services of this machine that did not start.
	RunnerProblems []RunnerProblem
	// DigestPath is the newest weekly digest file ("" when none): the menu links to it.
	DigestPath string
}

// Modes of a machine (machines R5.61) and the two pauses of the tray.
const (
	ModeAutomatic = "automatic"
	ModeAlways    = "always"
	ModePaused    = "paused"

	PauseOneHour      = "one_hour"
	PauseUntilResumed = "until_resumed"
)

// MachinePolicy is this machine's mode and whether it takes new jobs. Known is
// false when the service did not say (an older service, or no machine yet).
type MachinePolicy struct {
	Known       bool
	Mode        string
	Accepting   bool
	PauseReason string
	OnBattery   bool
	// PauseUntil is the end of a timed pause from the tray; zero when none.
	PauseUntil   time.Time
	UntilResumed bool
}

// UserPaused says whether the user holds the machine paused: the Paused mode or
// a pause from the tray.
func (p MachinePolicy) UserPaused() bool {
	return p.Known && (p.Mode == ModePaused || p.UntilResumed || !p.PauseUntil.IsZero())
}

// Word is the short word the header and tooltip show while the machine takes no
// new jobs: Paused or On battery. It is empty while accepting, and
// for the busy case: a machine over its budget is working, not held back.
func (p MachinePolicy) Word() string {
	if !p.Known || p.Accepting {
		return ""
	}
	switch p.PauseReason {
	case "paused":
		return "Paused"
	case "on_battery":
		return "On battery"
	}
	return ""
}

// Text is the status line and the tooltip body (R5.2), without the product name.
func (s Status) Text() string {
	g := Glyph(s.State)
	switch s.State {
	case StateStopped:
		when := ""
		if !s.StoppedAt.IsZero() {
			when = " at " + s.StoppedAt.Format("15:04")
		}
		return fmt.Sprintf("%s stopped%s · exit code %d", g, when, s.ExitCode)
	case StateFailed:
		if s.Fault != "" {
			return fmt.Sprintf("%s failed · %s", g, s.Fault)
		}
		return g + " failed"
	case StateStarting:
		return g + " starting"
	case StateHealthy:
		if s.Runners == 0 && s.Running == 0 && s.Waiting == 0 {
			return g + " ok · no runners yet"
		}
	}
	return fmt.Sprintf("%s %s · %d running · %d waiting", g, s.State, s.Running, s.Waiting) + s.policySuffix()
}

func (s Status) policySuffix() string {
	if w := s.Policy.Word(); w != "" {
		return " · " + strings.ToLower(w)
	}
	return ""
}

// Tooltip is "crsp: " + Text, cut at MaxTooltip characters with an ellipsis.
func (s Status) Tooltip() string {
	return cut("crsp: "+s.Text(), MaxTooltip)
}

func cut(t string, max int) string {
	r := []rune(t)
	if len(r) <= max {
		return t
	}
	return strings.TrimRight(string(r[:max-1]), " ") + "…"
}
