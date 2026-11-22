package updatewindow

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

func stageSnap(name string, mut func(*tray.StageInfo)) Snapshot {
	s := snap(name, -1)
	if mut != nil {
		mut(s.Stage)
	}
	return s
}

// LC-24: every state that can appear in the update window maps from its stage, with words from the table.
func TestEveryLifecycleStateRenders(t *testing.T) {
	cases := []struct {
		stage   string
		mut     func(*tray.StageInfo)
		want    State
		heading string
		bar     Bar
	}{
		{"waiting_for_jobs", nil, StateWaiting, "Waiting for running jobs", BarNone},
		{"downloading", func(s *tray.StageInfo) { s.Percent = 40 }, StateDownloading, "Downloading", BarDeterminate},
		{"verifying", nil, StateVerifying, "Checking the download", BarIndeterminate},
		{"installing", nil, StateInstalling, "Installing", BarIndeterminate},
		{"testing", nil, StateTesting, "Testing", BarIndeterminate},
		{"done", nil, StateDone, "Updated to version 0.1.13.", BarNone},
		{"rolling_back", func(s *tray.StageInfo) { s.Previous = "0.1.12" }, StateRollingBack, "Putting version 0.1.12 back", BarIndeterminate},
		{"refused", nil, StateRefused, "The download for 0.1.13 did not pass its signature check. Nothing changed.", BarNone},
		{"skipped", nil, StateSkipped, "Skipped version 0.1.13. We wait for a newer version.", BarNone},
	}
	for _, c := range cases {
		m := NewModel("0.1.12", "0.1.13", ModeLive)
		m.Observe(stageSnap(c.stage, c.mut))
		v := m.View()
		if v.State != c.want || v.Heading != c.heading || v.Bar != c.bar {
			t.Errorf("%s: %s %q bar %d", c.stage, v.State, v.Heading, v.Bar)
		}
		if has(v, v.Default) == false && v.Default != "" || !has(v, v.EscapeIs) {
			t.Errorf("%s: default %q escape %q are not buttons", c.stage, v.Default, v.EscapeIs)
		}
	}
}

func has(v View, id ButtonID) bool {
	for _, b := range v.Buttons {
		if b.ID == id {
			return true
		}
	}
	return false
}

func TestRolledBackNamesTheRetryTime(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	m.Observe(stageSnap("rolled_back", func(s *tray.StageInfo) { s.Previous, s.RetryAt = "0.1.12", "2026-11-21T18:05:00Z" }))
	v := m.View()
	if v.State != StateRolledBack || v.Tone != ToneProblem ||
		!strings.HasPrefix(v.Heading, "The update to 0.1.13 failed its test. Version 0.1.12 is running again. We try again at ") ||
		strings.Contains(v.Heading, "{") {
		t.Fatalf("%+v", v)
	}
	// After the third failure there is no retry time: the sentence does not promise one.
	m.Observe(stageSnap("rolled_back", func(s *tray.StageInfo) { s.Previous = "0.1.12" }))
	if h := m.View().Heading; strings.Contains(h, " at ") || strings.Contains(h, "{") {
		t.Fatalf("%q", h)
	}
}

// A stage left from an earlier update is not shown when the window opens on the question.
func TestReviewIgnoresLeftoverEndStages(t *testing.T) {
	for _, name := range []string{"rolled_back", "refused", "skipped"} {
		m := NewModel("0.1.12", "0.1.13", ModeReview)
		m.Observe(snap(name, -1))
		if m.State() != StateReview {
			t.Errorf("%s: %s", name, m.State())
		}
	}
}

// Updated shows What's new and Close; the others end on Close; working steps can only be hidden.
func TestButtonsOfTheNewStates(t *testing.T) {
	m := NewModel("0.1.12", "0.1.13", ModeLive)
	m.Observe(snap("done", 100))
	if b := m.View().Buttons; len(b) != 2 || b[0].ID != BtnWhatsNew || b[0].Label != "What's new" || b[1].ID != BtnClose || !b[1].Primary {
		t.Fatalf("%+v", b)
	}
	for _, name := range []string{"verifying", "testing", "rolling_back"} {
		m.Observe(snap(name, -1))
		if b := m.View().Buttons; len(b) != 1 || b[0].ID != BtnHide {
			t.Errorf("%s: %+v", name, b)
		}
	}
	if releaseURL("0.1.13") != "https://github.com/luis-mc/ci-runners-shared-pool/releases/tag/v0.1.13" {
		t.Fatal(releaseURL("0.1.13"))
	}
}

// Every word of every view comes from the table: no banned word, no unfilled slot, no doubled punctuation.
// OK, Yes and No are banned as answers (a label), the rest anywhere.
func TestNoBannedWordInAnyView(t *testing.T) {
	answers := []string{"OK", "Yes", "No", "Install now", "Later"}
	anywhere := []string{"daemon", "binary", "payload", "!!", "??", "..", ",,", "::"}
	for _, v := range SampleViews() {
		all := []string{v.Heading}
		all = append(all, v.Body...)
		labels := []string{}
		for _, b := range v.Buttons {
			labels = append(labels, b.Label)
		}
		for _, c := range v.Choices {
			labels = append(labels, c.Label)
		}
		all = append(all, labels...)
		for _, s := range all {
			if strings.Contains(s, "{") {
				t.Errorf("%s: unfilled %q", v.State, s)
			}
			for _, w := range anywhere {
				if strings.Contains(strings.ToLower(s), w) {
					t.Errorf("%s: %q has %q", v.State, s, w)
				}
			}
		}
		for _, l := range labels {
			for _, w := range answers {
				if strings.EqualFold(l, w) {
					t.Errorf("%s: label %q is banned", v.State, l)
				}
			}
		}
	}
}

func TestSampleViewsCoverEveryState(t *testing.T) {
	seen := map[State]bool{}
	for _, v := range SampleViews() {
		seen[v.State] = true
	}
	for _, s := range AllStates {
		if !seen[s] {
			t.Errorf("no sample for %s", s)
		}
	}
	if !seen["remove"] {
		t.Error("no sample for the remove question")
	}
}

// LC-24: Remove asks every time, with no default; it stays disabled until a choice is made.
func TestRemoveHasNoDefaultAndIsDisabledUntilChosen(t *testing.T) {
	m := NewRemoveModel("crsp", "1.4", `C:\ProgramData\crsp`)
	v := m.View()
	if len(v.Choices) != 2 || v.Choices[0].Label != "Keep my data" || v.Choices[1].Label != `Delete my data too (1.4 GB in C:\ProgramData\crsp)` {
		t.Fatalf("%+v", v.Choices)
	}
	for _, c := range v.Choices {
		if c.Chosen {
			t.Fatalf("%s is chosen at the start", c.ID)
		}
	}
	if v.Default != "" || v.EscapeIs != BtnCancel || len(v.Buttons) != 2 || v.Heading != "Remove crsp" {
		t.Fatalf("%+v", v)
	}
	cancel, remove := v.Buttons[0], v.Buttons[1]
	if cancel.ID != BtnCancel || cancel.Label != "Cancel" || remove.ID != BtnRemove || remove.Label != "Remove crsp" || !remove.Disabled || !remove.Primary {
		t.Fatalf("%+v", v.Buttons)
	}
	if _, ok := m.ExitCode(); ok {
		t.Fatal("an exit code before a choice")
	}
	m.Choose("nonsense")
	if m.Choice() != "" {
		t.Fatal("an unknown choice was taken")
	}
	if len(v.Body) != 1 || v.Body[0] != "Choose what happens to your data first." {
		t.Fatalf("the hint while Remove is disabled: %q", v.Body)
	}
	m.Choose(ChoiceKeep)
	if m.View().Body != nil {
		t.Fatal("the hint stays after a choice")
	}
}

func TestRemoveExitCodes(t *testing.T) {
	m := NewRemoveModel("crsp", "1.4", "x")
	m.Choose(ChoiceKeep)
	if c, ok := m.ExitCode(); !ok || c != 10 || m.View().Buttons[1].Disabled {
		t.Fatalf("keep: %d %v", c, ok)
	}
	m.Choose(ChoicePurge)
	if c, ok := m.ExitCode(); !ok || c != 11 {
		t.Fatalf("delete: %d %v", c, ok)
	}
	if ExitCancel != 1 || ExitKeep != 10 || ExitDelete != 11 {
		t.Fatal("exit codes")
	}
}

func TestRemoveArrowKeysStepTheChoice(t *testing.T) {
	m := NewRemoveModel("crsp", "1.4", "x")
	m.Step(1)
	if m.Choice() != ChoiceKeep {
		t.Fatal(m.Choice())
	}
	m.Step(1)
	m.Step(1)
	if m.Choice() != ChoiceKeep {
		t.Fatalf("wraps: %s", m.Choice())
	}
	m2 := NewRemoveModel("crsp", "1.4", "x")
	m2.Step(-1)
	if m2.Choice() != ChoicePurge {
		t.Fatal(m2.Choice())
	}
}

func TestFormatGB(t *testing.T) {
	for n, want := range map[int64]string{0: "0.0", 1 << 30: "1.0", 3 << 29: "1.5", 100 << 30: "100.0"} {
		if got := formatGB(n); got != want {
			t.Errorf("%d: %s", n, got)
		}
	}
	if DataSizeGB(t.TempDir()) != "0.0" {
		t.Fatal("empty folder")
	}
}

// The remove window never clips: the long path wraps and the window grows, at every DPI.
func TestRemoveLayoutGrowsWithALongPath(t *testing.T) {
	short := NewRemoveModel("crsp", "1", "C:/d").View()
	long := NewRemoveModel("crsp", "1", `C:\ProgramData\`+strings.Repeat("deep-folder-name\\", 12)).View()
	m := fake(15)
	if a, b := Compute(m, short), Compute(m, long); b.H <= a.H || len(Clipped(m, b)) > 0 {
		t.Fatalf("%d -> %d, clipped %v", a.H, b.H, Clipped(m, b))
	}
}

func TestLifecycleCommandAcceptsOnlyRemove(t *testing.T) {
	var out strings.Builder
	if RunLifecycle(Options{Args: nil, Stderr: &out}) != 2 || RunLifecycle(Options{Args: []string{"update"}, Stderr: &out}) != 2 {
		t.Fatal("exit 2 for an unknown question")
	}
}
