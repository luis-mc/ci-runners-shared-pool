package updatewindow

import (
	"errors"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
	"github.com/luis-mc/ci-runners-shared-pool/internal/update"
)

func snap(name string, pc int) Snapshot {
	return Snapshot{ServiceUp: true, ServiceVersion: "0.1.12", Jobs: 2, Stage: &tray.StageInfo{Name: name, Percent: pc, Version: "0.1.13", StartedBy: "tray"}}
}

func TestEveryStageMapsToItsView(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		snap  Snapshot
		want  State
		heads string
	}{
		{"nothing running, offered", ModeReview, Snapshot{ServiceUp: true, ServiceVersion: "0.1.12"}, StateReview, "Update to 0.1.13"},
		{"download", ModeLive, snap("downloading", 42), StateDownloading, "Downloading 0.1.13"},
		{"waiting", ModeLive, snap("waiting_for_jobs", -1), StateWaiting, "Waiting for jobs"},
		{"installing", ModeLive, snap("installing", 30), StateInstalling, "Installing 0.1.13"},
		{"waiting for another installer", ModeLive, snap("waiting_installer", -1), StateWaitingInstaller, "Waiting for another installer"},
		{"restarting", ModeLive, snap("restarting", -1), StateRestarting, "Restarting the service"},
		{"done (reopened from the toast)", ModeLive, snap("done", 100), StateDone, "Updated"},
		{"failed", ModeLive, snap("failed", -1), StateFailed, "The update failed"},
	}
	for _, c := range cases {
		m := NewModel("0.1.12", "0.1.13", c.mode)
		m.Observe(c.snap)
		v := m.View()
		if v.State != c.want || v.Heading != c.heads {
			t.Errorf("%s: %s %q", c.name, v.State, v.Heading)
		}
	}
}

func TestDownloadShowsThePercent(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	m.Observe(snap("downloading", 42))
	if v := m.View(); v.Bar != BarDeterminate || v.Percent != 42 {
		t.Fatalf("%+v", v)
	}
	m.Observe(snap("downloading", -1))
	if v := m.View(); v.Bar != BarIndeterminate {
		t.Fatalf("%+v", v)
	}
}

// D15: Cancel shows while it downloads and waits for jobs, never from Installing on.
func TestCancelOnlyBeforeInstalling(t *testing.T) {
	for _, s := range AllStates {
		want := s == StateDownloading || s == StateWaiting
		if CanCancel(s) != want {
			t.Errorf("%s: CanCancel %v", s, CanCancel(s))
		}
	}
	for _, c := range []struct {
		snap   Snapshot
		cancel bool
	}{
		{snap("downloading", 5), true}, {snap("waiting_for_jobs", -1), true},
		{snap("installing", 5), false}, {snap("restarting", -1), false}, {snap("done", 100), false}, {snap("failed", -1), false},
	} {
		m := NewModel("0.1.12", "0.1.13", ModeLive)
		m.Observe(c.snap)
		has := false
		for _, b := range m.View().Buttons {
			if b.ID == BtnCancel {
				has = true
			}
		}
		if has != c.cancel {
			t.Errorf("%s: cancel button %v", c.snap.Stage.Name, has)
		}
	}
	// From Installing on the only button is Hide.
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	m.Observe(snap("installing", 5))
	if b := m.View().Buttons; len(b) != 1 || b[0].ID != BtnHide {
		t.Fatalf("%+v", b)
	}
}

func TestReviewHasTwoButtonsAndTheKeys(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeReview)
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.12"})
	v := m.View()
	if len(v.Buttons) != 2 || v.Buttons[0].ID != BtnNotNow || v.Buttons[1].ID != BtnUpdate || !v.Buttons[1].Primary || v.Buttons[0].Primary {
		t.Fatalf("%+v", v.Buttons)
	}
	if v.Default != BtnUpdate || v.EscapeIs != BtnNotNow {
		t.Fatalf("enter %s escape %s", v.Default, v.EscapeIs)
	}
}

// Enter and Escape are always one of the buttons on screen; Escape never cancels.
func TestKeysAreButtonsOfTheView(t *testing.T) {
	for _, s := range []Snapshot{{ServiceUp: true}, snap("downloading", 1), snap("waiting_for_jobs", -1), snap("installing", 1), snap("restarting", -1), snap("done", 100), snap("failed", -1)} {
		m := NewModel("0.1.12", "0.1.13", ModeLive)
		m.Observe(s)
		v := m.View()
		has := func(id ButtonID) bool {
			for _, b := range v.Buttons {
				if b.ID == id {
					return true
				}
			}
			return false
		}
		if !has(v.Default) || !has(v.EscapeIs) || v.EscapeIs == BtnCancel || v.Default == BtnCancel {
			t.Errorf("%s: default %s escape %s", v.State, v.Default, v.EscapeIs)
		}
	}
}

// A stage left from an earlier update is not shown when the window opens on the question.
func TestReviewIgnoresALeftoverStage(t *testing.T) {
	for _, name := range []string{"done", "failed"} {
		m := NewModel("0.1.12", "0.1.13", ModeReview)
		m.Observe(snap(name, 100))
		if m.State() != StateReview {
			t.Errorf("%s: %s", name, m.State())
		}
	}
}

// Press Update now: Downloading at once, then what the service says; a refusal is Failed.
func TestRequestFlow(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeReview)
	m.Observe(snap("failed", -1)) // leftover
	m.Requested()
	if v := m.View(); v.State != StateDownloading || !strings.Contains(v.Body[0], "Starting") {
		t.Fatalf("%+v", v)
	}
	m.RequestReturned(nil)
	m.Observe(snap("downloading", 3))
	if m.State() != StateDownloading {
		t.Fatal(m.State())
	}
	m2 := NewModel("0.1.12", "0.1.13", ModeReview)
	m2.Requested()
	m2.RequestReturned(errors.New("an update or restart is already running"))
	v := m2.View()
	if v.State != StateFailed || !strings.Contains(strings.Join(v.Body, "\n"), "Still on 0.1.12.") || !strings.Contains(v.Body[0], "already running") {
		t.Fatalf("%+v", v)
	}
}

func TestCancelEndsWithNothingChanged(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	m.Observe(snap("waiting_for_jobs", -1))
	m.Cancelled()
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.12"})
	v := m.View()
	if v.State != StateCancelled || !strings.Contains(v.Body[0], "Nothing changed") || !strings.Contains(v.Body[0], "0.1.12") {
		t.Fatalf("%+v", v)
	}
}

// D12: the window stays open across the service restart and ends on Done, with no second window.
func TestDoneAfterTheRestart(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeReview)
	m.Requested()
	m.RequestReturned(nil)
	m.Observe(snap("installing", 20))
	if m.State() != StateInstalling {
		t.Fatal(m.State())
	}
	m.Observe(Snapshot{}) // the service is down while the installer replaces it: still installing
	if m.State() != StateInstalling {
		t.Fatalf("down: %s", m.State())
	}
	// the service is back with the new version but the stage file is still on restarting
	back := snap("restarting", -1)
	back.ServiceVersion = "0.1.13"
	m.Observe(back)
	v := m.View()
	if v.State != StateDone || v.Body[0] != "Now running 0.1.13." {
		t.Fatalf("%+v", v)
	}
	// a stage of done wins the same way
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.13", Stage: &tray.StageInfo{Name: "done", Version: "0.1.13"}})
	if v := m.View(); v.State != StateDone || v.Body[0] != "Now running 0.1.13." {
		t.Fatalf("%+v", v)
	}
	// and a service that never answers stays on Restarting
	m2 := NewModel("0.1.12", "0.1.13", ModeLive)
	m2.Observe(snap("restarting", -1))
	m2.Observe(Snapshot{})
	if m2.State() != StateRestarting {
		t.Fatal(m2.State())
	}
}

func TestFailedShowsReasonStillOnAndLog(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	s := snap("failed", -1)
	s.Stage.Error = "the new build did not report healthy in time"
	s.Stage.LogPath = `C:\ProgramData\crsp\logs\msi-update.log`
	m.Observe(s)
	v := m.View()
	got := strings.Join(v.Body, "\n")
	for _, want := range []string{"did not report healthy", "Still on 0.1.12.", `Log: C:\ProgramData\crsp\logs\msi-update.log`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if v.Tone != ToneProblem {
		t.Error("a problem tone")
	}
}

func TestSnapshotOf(t *testing.T) {
	if s := SnapshotOf(tray.Status{}, errors.New("no")); s.ServiceUp {
		t.Fatal("an error is no answer")
	}
	st := tray.Status{Version: "0.1.12", Running: 3, Update: tray.UpdateInfo{Stage: &tray.StageInfo{Name: "downloading"}}}
	if s := SnapshotOf(st, nil); !s.ServiceUp || s.Jobs != 3 || s.Stage.Name != "downloading" || s.ServiceVersion != "0.1.12" {
		t.Fatalf("%+v", s)
	}
}

// TRAY8: the service says "already the newest": the window says Up to date, no failure, no internal prefix.
func TestNoUpdateAnswerShowsUpToDate(t *testing.T) {
	for _, err := range []error{update.ErrNoUpdate, errors.New("local channel: " + update.ErrNoUpdate.Error())} {
		m := NewModel("0.1.13", "0.1.13", ModeLive)
		m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.13"})
		m.Requested()
		m.RequestReturned(err)
		v := m.View()
		all := v.Heading + " " + strings.Join(v.Body, " ")
		if v.State != StateUpToDate || v.Heading != "Up to date" || v.Tone == ToneProblem || !strings.Contains(all, "0.1.13") || strings.Contains(all, "local channel") {
			t.Fatalf("%v: %+v", err, v)
		}
	}
}

// TRAY8: a review of a version that is not newer than the running one offers nothing.
func TestReviewNeverOffersAVersionThatIsNotNewer(t *testing.T) {
	for _, to := range []string{"0.1.13", "0.1.12"} {
		m := NewModel("0.1.13", to, ModeReview)
		m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.13"})
		v := m.View()
		if v.State != StateUpToDate {
			t.Fatalf("%s: %+v", to, v)
		}
		for _, b := range v.Buttons {
			if b.ID == BtnUpdate {
				t.Fatalf("%s: offers Update now", to)
			}
		}
	}
	m := NewModel("0.1.12", "0.1.13", ModeReview)
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.12"})
	if m.State() != StateReview {
		t.Fatal("a newer version must still be offered")
	}
}

// TRAY8: the long review text wraps in a narrow font and nothing is clipped.
func TestReviewBodyWrapsWithoutCutting(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeReview)
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.12"})
	v := m.View()
	for _, unit := range dpiUnits {
		mt := fake(unit)
		l := Compute(mt, v)
		if bad := Clipped(mt, l); len(bad) > 0 {
			t.Fatal(bad)
		}
		joined := ""
		for _, it := range l.Items {
			if it.Kind == KindBody {
				joined += strings.Join(it.Lines, " ") + " "
			}
		}
		if !strings.Contains(joined, "No job is stopped.") {
			t.Fatalf("text cut: %q", joined)
		}
	}
}

// TRAY9: a done stage of an earlier update is not Done when a newer release is offered.
func TestLeftoverDoneStageTable(t *testing.T) {
	stage := func(name, v string) *tray.StageInfo { return &tray.StageInfo{Name: name, Version: v} }
	cases := []struct {
		name         string
		from, to     string
		mode         Mode
		snap         Snapshot
		want         State
		heading, msg string
	}{
		{"review: leftover done 0.1.17", "0.1.17", "0.1.18", ModeReview, Snapshot{ServiceUp: true, ServiceVersion: "0.1.17", Stage: stage("done", "0.1.17")}, StateReview, "Update to 0.1.18", ""},
		{"live: leftover done 0.1.17", "0.1.17", "0.1.18", ModeLive, Snapshot{ServiceUp: true, ServiceVersion: "0.1.17", Stage: stage("done", "0.1.17")}, StateReview, "Update to 0.1.18", ""},
		{"live: real done 0.1.18", "0.1.17", "0.1.18", ModeLive, Snapshot{ServiceUp: true, ServiceVersion: "0.1.18", Stage: stage("done", "0.1.18")}, StateDone, "Updated", "Now running 0.1.18."},
		{"live: done after restart, window opened on new version", "0.1.18", "", ModeLive, Snapshot{ServiceUp: true, ServiceVersion: "0.1.18", Stage: stage("done", "0.1.18")}, StateDone, "Updated", "Now running 0.1.18."},
		{"live: done, same version, nothing newer offered", "0.1.18", "0.1.18", ModeLive, Snapshot{ServiceUp: true, ServiceVersion: "0.1.18", Stage: stage("done", "0.1.18")}, StateDone, "Updated", "Now running 0.1.18."},
		{"review: no stage", "0.1.17", "0.1.18", ModeReview, Snapshot{ServiceUp: true, ServiceVersion: "0.1.17"}, StateReview, "Update to 0.1.18", ""},
		{"review: old failed stage", "0.1.17", "0.1.18", ModeReview, Snapshot{ServiceUp: true, ServiceVersion: "0.1.17", Stage: stage("failed", "0.1.17")}, StateReview, "Update to 0.1.18", ""},
	}
	for _, c := range cases {
		m := NewModel(c.from, c.to, c.mode)
		m.Observe(c.snap)
		v := m.View()
		if v.State != c.want || v.Heading != c.heading || (c.msg != "" && (len(v.Body) == 0 || v.Body[0] != c.msg)) {
			t.Errorf("%s: %+v", c.name, v)
		}
	}
	// the person presses Update now over a leftover done: the window downloads, not Done
	m := NewModel("0.1.17", "0.1.18", ModeReview)
	m.Observe(Snapshot{ServiceUp: true, ServiceVersion: "0.1.17", Stage: stage("done", "0.1.17")})
	m.Requested()
	m.RequestReturned(nil)
	if m.State() != StateDownloading {
		t.Errorf("requested over leftover: %s", m.State())
	}
}

// TRAY11: another installer holds Windows Installer; the window names it and keeps Hide only.
func TestWaitingForAnotherInstaller(t *testing.T) {
	m := NewModel("0.1.19", "0.1.20", ModeLive)
	s := snap("waiting_installer", -1)
	s.Stage.WaitingFor = "ComiclexHome-0.1.11712-x64"
	m.Observe(s)
	v := m.View()
	want := "ComiclexHome-0.1.11712-x64 is installing. Windows installs one program at a time; this update starts when it finishes."
	if v.State != StateWaitingInstaller || v.Heading != "Waiting for another installer" || len(v.Body) != 1 || v.Body[0] != want {
		t.Fatalf("%+v", v)
	}
	if v.Bar != BarIndeterminate || len(v.Buttons) != 1 || v.Buttons[0].ID != BtnHide || v.Default != BtnHide || v.EscapeIs != BtnHide {
		t.Fatalf("bar and buttons: %+v", v)
	}
	if CanCancel(StateWaitingInstaller) {
		t.Fatal("no cancel while the installer is queued behind another")
	}
	// no name known: a generic one
	s.Stage.WaitingFor = ""
	m.Observe(s)
	if b := m.View().Body[0]; !strings.HasPrefix(b, "Another program is installing.") {
		t.Fatal(b)
	}
}

// TRAY11: no stage claims progress that has not happened. Restarting shows only once the
// service said the installer finished; a service that is down while installing is installing.
func TestRestartingOnlyAfterTheInstallerFinished(t *testing.T) {
	m := NewModel("0.1.19", "0.1.20", ModeLive)
	m.Observe(snap("installing", -1))
	m.Observe(Snapshot{})
	if m.State() != StateInstalling {
		t.Fatalf("down during install: %s", m.State())
	}
	m.Observe(snap("restarting", -1))
	m.Observe(Snapshot{})
	if m.State() != StateRestarting {
		t.Fatalf("down after the installer finished: %s", m.State())
	}
	w := NewModel("0.1.19", "0.1.20", ModeLive)
	w.Observe(snap("waiting_installer", -1))
	if w.State() == StateRestarting || w.State() == StateInstalling {
		t.Fatalf("waiting claims progress: %s", w.State())
	}
}
