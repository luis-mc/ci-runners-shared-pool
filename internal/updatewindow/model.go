package updatewindow

import (
	"errors"
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

// State is the one step the window shows (decision tray D12).
type State string

// The seven steps of the update, and Cancelled for an update the person stopped (D15).
const (
	StateReview      State = "review"
	StateDownloading State = "downloading"
	StateWaiting     State = "waiting_for_jobs"
	StateInstalling  State = "installing"
	// StateWaitingInstaller: another installer holds Windows Installer; the update starts when it ends (TRAY11).
	StateWaitingInstaller State = "waiting_installer"
	StateRestarting       State = "restarting"
	StateDone             State = "done"
	StateFailed           State = "failed"
	StateCancelled        State = "cancelled"
	StateUpToDate         State = "up_to_date" // the version offered is not newer than the one that runs (TRAY8)
)

// AllStates lists every state, in the order of an update.
var AllStates = []State{StateReview, StateDownloading, StateWaiting, StateWaitingInstaller, StateInstalling, StateRestarting, StateDone, StateFailed, StateCancelled, StateUpToDate}

// Mode is how the window was opened.
type Mode string

const (
	// ModeReview opens on the question: the tray offered the update. An old done or failed
	// stage left in the work folder is not this update and is not shown.
	ModeReview Mode = "review"
	// ModeLive opens on whatever the update does now: the menu item while it runs, or the
	// toast after an automatic update (Done).
	ModeLive Mode = "live"
)

// ButtonID names a button. The window sends the ID back; the page code never draws a button.
type ButtonID string

// The buttons of the window.
const (
	BtnNotNow ButtonID = "not_now"
	BtnUpdate ButtonID = "update"
	BtnHide   ButtonID = "hide"
	BtnCancel ButtonID = "cancel"
	BtnClose  ButtonID = "close"
)

// Button is one button of the bar, left to right.
type Button struct {
	ID      ButtonID
	Label   string
	Primary bool // filled; the others are outlined (D16)
}

// Bar is the progress bar of a state.
type Bar int

// Bar kinds.
const (
	BarNone Bar = iota
	BarDeterminate
	BarIndeterminate
)

// Tone colours the heading: normal, done or a problem.
type Tone int

// Tones.
const (
	ToneNormal Tone = iota
	ToneDone
	ToneProblem
)

// View is everything the window draws for a state. The window passes it to the layout.
type View struct {
	State   State
	Heading string
	Body    []string // paragraphs
	Bar     Bar
	Percent int // 0 to 100; Bar determinate only
	Tone    Tone
	Buttons []Button
	// Default is the button Enter presses; Escape and the close button of the title bar do
	// EscapeIs: Not now on Review, Hide while it works, Close at the end.
	Default  ButtonID
	EscapeIs ButtonID
}

// Snapshot is one reading of the service, as the model needs it.
type Snapshot struct {
	Stage          *tray.StageInfo
	ServiceUp      bool   // the service answered
	ServiceVersion string // the version it runs
	Jobs           int    // jobs running now
}

// SnapshotOf turns the answer of the local channel into a Snapshot; err means no answer.
func SnapshotOf(st tray.Status, err error) Snapshot {
	if err != nil {
		return Snapshot{}
	}
	return Snapshot{Stage: st.Update.Stage, ServiceUp: true, ServiceVersion: st.Version, Jobs: st.Running}
}

// Model is the state machine of the window: stage and answers in, one View out. It has no
// window and no clock, so every rule is a plain test.
type Model struct {
	From string // the version that ran when the window opened
	To   string // the version offered; the stage names it once the update runs
	Mode Mode

	last        Snapshot
	requested   bool   // the person pressed Update now
	returned    bool   // the service answered that request
	startErr    string // the service refused the request
	cancelled   bool
	upToDate    bool // the service answered that this is already the newest release
	seenInstall bool // installing or restarting was seen: the service goes down and comes back
	seenRestart bool // restarting was seen: the installer has finished
}

// NewModel starts a model for the window.
func NewModel(from, to string, mode Mode) *Model { return &Model{From: from, To: to, Mode: mode} }

// Observe takes a new reading.
func (m *Model) Observe(s Snapshot) {
	m.last = s
	if s.Stage != nil && (s.Stage.Name == "installing" || s.Stage.Name == "restarting") {
		m.seenInstall = true
	}
	if s.Stage != nil && s.Stage.Name == "restarting" {
		m.seenRestart = true // only the service says so, once msiexec has finished
	}
}

// Requested says the person pressed Update now; the service has not answered yet.
func (m *Model) Requested() {
	m.requested, m.returned, m.startErr, m.cancelled, m.upToDate, m.seenRestart = true, false, "", false, false, false
}

// RequestReturned says the service answered the request: err nil means the update runs.
func (m *Model) RequestReturned(err error) {
	m.returned = true
	if err != nil && (errors.Is(err, update.ErrNoUpdate) || strings.Contains(err.Error(), update.ErrNoUpdate.Error())) {
		m.upToDate = true // not a failure: nothing newer exists
		return
	}
	if err != nil {
		m.startErr = err.Error()
	}
}

// Cancelled says the service stopped the update at the person's request.
func (m *Model) Cancelled() { m.cancelled = true }

// CanCancel is true only before the install begins: while it downloads or waits for jobs (D15).
func CanCancel(s State) bool { return s == StateDownloading || s == StateWaiting }

// Closable is true when pressing the title bar's close button only hides the window (the
// update goes on); the window never stops an update by closing.
func Closable(State) bool { return true }

// State is the step the window shows now.
func (m *Model) State() State {
	if m.cancelled {
		return StateCancelled
	}
	if m.upToDate {
		return StateUpToDate
	}
	if m.startErr != "" {
		return StateFailed
	}
	trust := m.Mode == ModeLive || m.requested || m.seenInstall // this update's stage, not a leftover
	if m.requested && !m.returned {
		return StateDownloading // between the press and the service's answer
	}
	st := m.last.Stage
	if st != nil {
		switch st.Name {
		case "done":
			if trust && !m.leftoverDone(st) {
				return StateDone
			}
		case "failed":
			if trust {
				return StateFailed
			}
		case "downloading":
			return StateDownloading
		case "waiting_for_jobs":
			return StateWaiting
		case "waiting_installer":
			return StateWaitingInstaller
		case "installing", "restarting":
			if m.last.ServiceUp && m.last.ServiceVersion != "" && m.last.ServiceVersion != m.From {
				return StateDone // the new version answers; the stage file has not caught up
			}
			if st.Name == "installing" {
				return StateInstalling
			}
			return StateRestarting
		}
	}
	if m.seenInstall {
		if !m.last.ServiceUp {
			// The service is down while it is replaced. It is a restart only once the
			// service said the installer finished; before that the installer still works.
			if m.seenRestart {
				return StateRestarting
			}
			return StateInstalling
		}
		if m.last.ServiceVersion != "" && m.last.ServiceVersion != m.From {
			return StateDone // back, and running the new version: Done after the restart
		}
	}
	if m.requested {
		return StateDownloading
	}
	if m.Mode == ModeReview && m.To != "" && m.From != "" && !update.Newer(m.running(), m.To) {
		return StateUpToDate // never offer a version that is not newer than the running one
	}
	return StateReview
}

// leftoverDone says a done stage is an earlier update's: it names the version the window
// opened on while a newer one is offered. It is never shown as Done.
func (m *Model) leftoverDone(st *tray.StageInfo) bool {
	return st.Version != "" && st.Version == m.From && m.To != "" && update.Newer(m.From, m.To)
}

// version is the version the update brings, as far as it is known.
func (m *Model) version() string {
	if st := m.last.Stage; st != nil && st.Version != "" {
		return st.Version
	}
	return m.To
}

// running is the version that runs now: the service's answer, else the one the window opened on.
func (m *Model) running() string {
	if m.last.ServiceUp && m.last.ServiceVersion != "" {
		return m.last.ServiceVersion
	}
	return m.From
}

// View is what the window draws now.
func (m *Model) View() View {
	s := m.State()
	to := m.version()
	v := View{State: s, Percent: -1}
	switch s {
	case StateReview:
		if m.To != "" {
			to = m.To // the offer, never the version of a leftover stage
		}
		v.Heading = "Update to " + to
		v.Body = []string{
			fmt.Sprintf("Version %s is ready. You run %s.", to, m.From),
			"Running jobs finish first, then the service restarts (about 1 min). No job is stopped.",
		}
		v.Buttons = []Button{{BtnNotNow, WordNotNow, false}, {BtnUpdate, WordUpdate, true}}
		v.Default, v.EscapeIs = BtnUpdate, BtnNotNow
	case StateDownloading:
		v.Heading = "Downloading " + to
		v.Bar = BarIndeterminate
		if st := m.last.Stage; st != nil && st.Name == "downloading" && st.Percent >= 0 {
			v.Bar, v.Percent = BarDeterminate, min(st.Percent, 100)
		}
		if m.requested && !m.returned {
			v.Body = []string{WordStarted}
		} else {
			v.Body = []string{"The update is checked against its checksum and signature before it installs."}
		}
		v.Buttons = []Button{{BtnHide, WordHide, false}, {BtnCancel, WordCancel, false}}
		v.Default, v.EscapeIs = BtnHide, BtnHide
	case StateWaiting:
		v.Heading = "Waiting for jobs"
		v.Body = []string{jobsLine(m.last.Jobs), "This machine takes no new jobs meanwhile. No job is stopped."}
		v.Buttons = []Button{{BtnHide, WordHide, false}, {BtnCancel, WordCancel, false}}
		v.Default, v.EscapeIs = BtnHide, BtnHide
	case StateInstalling:
		v.Heading = "Installing " + to
		v.Bar = BarIndeterminate
		if st := m.last.Stage; st != nil && st.Name == "installing" && st.Percent >= 0 {
			v.Bar, v.Percent = BarDeterminate, min(st.Percent, 100)
		}
		v.Body = []string{"This cannot be cancelled. You can hide this window; the update goes on."}
		v.Buttons = []Button{{BtnHide, WordHide, false}}
		v.Default, v.EscapeIs = BtnHide, BtnHide
	case StateWaitingInstaller:
		v.Heading = WordWaitingInstaller
		v.Bar = BarIndeterminate
		v.Body = []string{waitingInstallerLine(m.last.Stage)}
		v.Buttons = []Button{{BtnHide, WordHide, false}}
		v.Default, v.EscapeIs = BtnHide, BtnHide
	case StateRestarting:
		v.Heading = "Restarting the service"
		v.Bar = BarIndeterminate
		v.Body = []string{"The service is replaced and starts again. This window waits for it."}
		v.Buttons = []Button{{BtnHide, WordHide, false}}
		v.Default, v.EscapeIs = BtnHide, BtnHide
	case StateDone:
		v.Heading, v.Tone = "Updated", ToneDone
		v.Body = []string{"Now running " + m.doneVersion() + "."}
		v.Buttons = []Button{{BtnClose, WordClose, true}}
		v.Default, v.EscapeIs = BtnClose, BtnClose
	case StateFailed:
		v.Heading, v.Tone = "The update failed", ToneProblem
		v.Body = m.failureLines()
		v.Buttons = []Button{{BtnClose, WordClose, true}}
		v.Default, v.EscapeIs = BtnClose, BtnClose
	case StateUpToDate:
		v.Heading = WordUpToDate
		v.Body = []string{"Now running " + m.running() + ". This is the newest stable release."}
		v.Buttons = []Button{{BtnClose, WordClose, true}}
		v.Default, v.EscapeIs = BtnClose, BtnClose
	case StateCancelled:
		v.Heading = "Update cancelled"
		v.Body = []string{"Nothing changed. Still on " + m.running() + "."}
		v.Buttons = []Button{{BtnClose, WordClose, true}}
		v.Default, v.EscapeIs = BtnClose, BtnClose
	}
	return v
}

func waitingInstallerLine(st *tray.StageInfo) string {
	name := WordAnotherProgram
	if st != nil && strings.TrimSpace(st.WaitingFor) != "" {
		name = strings.TrimSpace(st.WaitingFor)
	}
	return fmt.Sprintf(WordWaitingInstallerBody, name)
}

func jobsLine(n int) string {
	switch n {
	case 0:
		return "The jobs on this machine are finishing. The update starts when they are done."
	case 1:
		return "1 job runs now. The update starts when it is done."
	}
	return fmt.Sprintf("%d jobs run now. The update starts when they are done.", n)
}

func (m *Model) doneVersion() string {
	if st := m.last.Stage; st != nil && st.Name == "done" && st.Version != "" {
		return st.Version
	}
	if m.last.ServiceUp && m.last.ServiceVersion != "" {
		return m.last.ServiceVersion
	}
	return m.To
}

func (m *Model) failureLines() []string {
	reason := m.startErr
	log := ""
	if st := m.last.Stage; st != nil && st.Name == "failed" {
		if reason == "" {
			reason = st.Error
		}
		log = st.LogPath
	}
	if reason == "" {
		reason = "The update did not finish."
	}
	out := []string{strings.TrimSpace(reason), "Still on " + m.running() + "."}
	if log != "" {
		out = append(out, "Log: "+log)
	}
	return out
}
