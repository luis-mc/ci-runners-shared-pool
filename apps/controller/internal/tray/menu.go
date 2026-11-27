package tray

import (
	"fmt"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// Menu item labels. Words come from docs/design-system/next-version/content.md (TRAY3).
const (
	ProductName       = "CI Runners Shared Pool"
	LabelOpen         = "Open console"
	LabelCheck        = "Look for updates"
	LabelRestart      = "Restart the service"
	LabelOpenLogs     = "Open log folder"
	LabelOpenGitHub   = "Open on GitHub"
	LabelOpenDoctor   = "Open Doctor"
	LabelOpenDigest   = "Open weekly digest"
	LabelStartAtLogin = "Start at login"
	LabelQuit         = "Quit tray"
	LabelMode         = "Mode"
	LabelModeCurrent  = "Mode: "
	LabelAutomatic    = "Automatic"
	LabelAlways       = "Always"
	LabelPause        = "Pause"
	LabelPauseHour    = "for 1 hour"
	LabelPauseUntil   = "until I resume"
	// LabelUpdatePrefix is the up arrow before Update to <version>.
	LabelUpdatePrefix = "\u2191 Update to "
)

// Console pages the tray opens through a one-time link.
const (
	JobsPath   = "/jobs"
	DoctorPath = "/doctor"
)

// Item IDs, in menu order. The update item sits under the header and is shown
// only when a newer release exists.
const (
	IDHeader    = "header"
	IDUpdate    = "update"
	IDStatus    = "status"
	IDSep1      = "sep1"
	IDOpen      = "open"
	IDGitHub    = "github"
	IDCheck     = "check"
	IDSep2      = "sep2"
	IDPause     = "pause"
	IDPauseHour = "pause_hour"
	IDPauseTill = "pause_until_resumed"
	IDResume    = "resume"
	IDMode      = "mode"
	IDModeAuto  = "mode_automatic"
	IDModeAlw   = "mode_always"
	IDSep3      = "sep3"
	IDRestart   = "restart"
	IDLogs      = "logs"
	IDDoctor    = "doctor"
	IDDigest    = "digest"
	IDSep4      = "sep4"
	IDLogin     = "login"
	IDQuit      = "quit"
)

// MenuItem is one entry of the tray menu.
type MenuItem struct {
	ID        string
	Label     string
	Action    bool // pressable
	Switch    bool // has a tick
	Checked   bool
	Separator bool
	Hidden    bool // present in the model, not shown
	// Parent is the ID of the submenu this item sits in; empty for the top level.
	Parent string
	// Radio marks an item of a group where exactly one is ticked (Checked).
	Radio bool
}

// UpdateInfo is what the service says about updates (operations R5.11 to R5.18).
type UpdateInfo struct {
	Latest    string // newest stable release found, "" before the first look
	Available bool
	State     string // idle, checking, waiting_for_jobs, downloading, installing, restarting, failed
	AutoOff   bool   // auto-update is turned off (it is on by default)
	// ReleaseURL is the release page of Latest; Jobs is how many jobs run now (a drain waits for them).
	ReleaseURL string
	Jobs       int
	// Stage is the live update stage (the update window follows it); nil when none ran.
	Stage *StageInfo
}

// Updating says whether an update is in progress (drain, download, install).
func (u UpdateInfo) Updating() bool {
	switch u.State {
	case "waiting_for_jobs", "downloading", "installing", "restarting":
		return true
	}
	return false
}

// Header is the first menu line: product, version and update state.
// With no answer from the service the state is not known and is not claimed.
func Header(version string, st Status) string {
	base := ProductName + " " + version
	switch st.State {
	case StateStarting, StateStopped:
		return base
	}
	switch {
	case st.Update.State == "failed":
		base += " - update failed"
	case st.Update.Updating():
		base += " - updating…"
	case st.NewerFound():
		// The Update to line below says which version; the header stays plain.
	default:
		base += " - up to date"
	}
	if st.Update.AutoOff {
		base += " · auto-update off"
	}
	if w := st.Policy.Word(); w != "" {
		base += " · " + w
	}
	return base
}

// StatusLine is the status line: "Running - 2 jobs - 1 runner". The state is
// always a word (UX-58); the tooltip keeps the glyph.
func (s Status) StatusLine() string {
	word := map[State]string{
		StateHealthy: "Running", StateWaiting: "Waiting", StateFailed: "Failed",
		StateStopped: "Stopped", StateStarting: "Starting",
	}[s.State]
	if word == "" {
		word = "Starting"
	}
	var b strings.Builder
	b.WriteString(word)
	switch s.State {
	case StateStopped:
		if !s.StoppedAt.IsZero() {
			b.WriteString(" at " + s.StoppedAt.Format("15:04"))
		}
		fmt.Fprintf(&b, " - exit code %d", s.ExitCode)
	case StateFailed:
		if s.Fault != "" {
			b.WriteString(" - " + s.Fault)
		}
	case StateStarting:
	default:
		if s.State == StateHealthy && s.Runners == 0 && s.Running == 0 && s.Waiting == 0 {
			b.WriteString(" - no runners yet")
			break
		}
		fmt.Fprintf(&b, " - %s - %s", plural(s.Running, "job"), plural(s.Runners, "runner"))
	}
	return b.String()
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// UpdateShown says whether the Update to line shows: a newer release exists
// and no update is running.
func (s Status) UpdateShown() bool {
	return s.NewerFound() && !s.Update.Updating()
}

// NewerFound says a release newer than the one the service runs was found. The service
// says Available; this also checks the versions, so a release that is not newer is never
// offered (TRAY8). A service that does not say its version is trusted.
func (s Status) NewerFound() bool {
	if !s.Update.Available || s.Update.Latest == "" {
		return false
	}
	return s.Version == "" || update.Newer(s.Version, s.Update.Latest)
}

// ResumeLabel is the pause item while the user holds a pause.
func (p MachinePolicy) ResumeLabel() string {
	if !p.PauseUntil.IsZero() {
		return "Paused until " + p.PauseUntil.Local().Format("15:04") + " - Resume now"
	}
	return "Paused - Resume now"
}

// BuildMenu returns the tray menu in order (TRAY3). The same list is built for
// Windows, macOS and Linux. No entry is bold: the menu library has no default entry.
//
// With no policy from the service the pause and mode items are hidden: an older
// service cannot change them. While the user holds a pause the Pause submenu
// gives way to one Paused until HH:MM - Resume now entry. The Mode submenu has
// Automatic and Always; Paused is no mode here, so a machine paused from the
// console ticks neither.
func BuildMenu(version string, st Status, startAtLogin bool) []MenuItem {
	p := st.Policy
	hide := !p.Known
	mode := p.Mode
	if mode == "" {
		mode = ModeAutomatic
	}
	paused := p.UserPaused()
	modeLabel := LabelMode
	switch mode {
	case ModeAutomatic:
		modeLabel = LabelModeCurrent + LabelAutomatic
	case ModeAlways:
		modeLabel = LabelModeCurrent + LabelAlways
	}
	return []MenuItem{
		{ID: IDHeader, Label: Header(version, st)},
		{ID: IDUpdate, Label: LabelUpdatePrefix + st.Update.Latest, Action: true, Hidden: !st.UpdateShown()},
		{ID: IDStatus, Label: st.StatusLine(), Action: true},
		{ID: IDSep1, Separator: true},
		{ID: IDOpen, Label: LabelOpen, Action: true},
		{ID: IDGitHub, Label: LabelOpenGitHub, Action: true},
		{ID: IDCheck, Label: LabelCheck, Action: true},
		{ID: IDSep2, Separator: true},
		{ID: IDPause, Label: LabelPause, Action: true, Hidden: hide || paused},
		{ID: IDPauseHour, Label: LabelPauseHour, Action: true, Parent: IDPause, Hidden: hide || paused},
		{ID: IDPauseTill, Label: LabelPauseUntil, Action: true, Parent: IDPause, Hidden: hide || paused},
		{ID: IDResume, Label: p.ResumeLabel(), Action: true, Hidden: hide || !paused},
		{ID: IDMode, Label: modeLabel, Action: true, Hidden: hide},
		{ID: IDModeAuto, Label: LabelAutomatic, Action: true, Radio: true, Switch: true, Checked: mode == ModeAutomatic, Parent: IDMode, Hidden: hide},
		{ID: IDModeAlw, Label: LabelAlways, Action: true, Radio: true, Switch: true, Checked: mode == ModeAlways, Parent: IDMode, Hidden: hide},
		{ID: IDSep3, Separator: true},
		{ID: IDRestart, Label: LabelRestart, Action: true},
		{ID: IDLogs, Label: LabelOpenLogs, Action: true},
		{ID: IDDoctor, Label: LabelOpenDoctor, Action: true},
		{ID: IDDigest, Label: LabelOpenDigest, Action: true, Hidden: st.DigestPath == ""},
		{ID: IDSep4, Separator: true},
		{ID: IDLogin, Label: LabelStartAtLogin, Action: true, Switch: true, Checked: startAtLogin},
		{ID: IDQuit, Label: LabelQuit, Action: true},
	}
}

// Visible drops the hidden items.
func Visible(items []MenuItem) []MenuItem {
	var out []MenuItem
	for _, it := range items {
		if !it.Hidden {
			out = append(out, it)
		}
	}
	return out
}
