package updatewindow

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// fake is a monospace font: a character is 6/10 of the font height wide, bold the same.
func fake(unit int) Metrics {
	return Metrics{Unit: unit, Measure: func(t string, _ bool) int { return utf8.RuneCountInString(t) * unit * 6 / 10 }}
}

// The font height in pixels at 100, 125, 150 and 200% (a 9 pt face is about 15 px at 96 dpi).
var dpiUnits = map[string]int{"100%": 15, "125%": 19, "150%": 23, "200%": 30}

// D14: every state, at every DPI, nothing is clipped.
func TestNothingIsClippedAtAnyDPI(t *testing.T) {
	views := SampleViews()
	if len(views) != len(AllStates) {
		t.Fatalf("%d views", len(views))
	}
	for name, unit := range dpiUnits {
		for _, v := range views {
			m := fake(unit)
			l := Compute(m, v)
			if bad := Clipped(m, l); len(bad) > 0 {
				t.Errorf("%s %s: %s", name, v.State, strings.Join(bad, "; "))
			}
		}
	}
}

// D14: no fixed pixel heights: the window at 200% is twice the window at 100%, within rounding.
func TestLayoutScalesWithTheFontHeight(t *testing.T) {
	for _, v := range SampleViews() {
		a := Compute(fake(15), v)
		b := Compute(fake(30), v)
		if d := b.W - 2*a.W; d < -4 || d > 4 {
			t.Errorf("%s: width %d vs %d", v.State, a.W, b.W)
		}
		if d := b.H - 2*a.H; d < -2*a.H/20 || d > 2*a.H/20 { // each line rounds on its own
			t.Errorf("%s: height %d vs %d", v.State, a.H, b.H)
		}
	}
}

func TestLongTextWrapsAndTheWindowGrowsTaller(t *testing.T) {
	short := View{State: StateFailed, Heading: "The update failed", Body: []string{"Short."}, Buttons: []Button{{BtnClose, "Close", true}}}
	long := short
	long.Body = []string{strings.Repeat("a long reason ", 30), `C:\ProgramData\crsp\logs\` + strings.Repeat("x", 120)}
	m := fake(15)
	ls, ll := Compute(m, short), Compute(m, long)
	if ll.H <= ls.H || ll.W != ls.W {
		t.Fatalf("short %dx%d, long %dx%d", ls.W, ls.H, ll.W, ll.H)
	}
	if bad := Clipped(m, ll); len(bad) > 0 {
		t.Fatal(bad)
	}
}

// The buttons sit on the right, in order, on one row, and the bar sits below the text.
func TestButtonsOneRowLeftToRight(t *testing.T) {
	for _, v := range SampleViews() {
		if v.State != StateReview && v.State != StateDownloading {
			continue
		}
		l := Compute(fake(15), v)
		prev, y := -1, -1
		n := 0
		for _, it := range l.Items {
			if it.Kind != KindButton {
				continue
			}
			n++
			if it.Rect.Min.X <= prev || (y >= 0 && it.Rect.Min.Y != y) {
				t.Errorf("%s: buttons not on one row, left to right", v.State)
			}
			prev, y = it.Rect.Min.X, it.Rect.Min.Y
			if it.Rect.Max.X > l.W {
				t.Errorf("%s: a button leaves the window", v.State)
			}
		}
		if n != len(v.Buttons) {
			t.Errorf("%s: %d buttons of %d", v.State, n, len(v.Buttons))
		}
	}
}

func TestClippedSeesWhatIsCutOff(t *testing.T) {
	m := fake(15)
	l := Compute(m, SampleViews()[0])
	l.Items[1].Rect.Max.X = 20 // a heading box far too narrow
	if len(Clipped(m, l)) == 0 {
		t.Fatal("a too-narrow box must be reported")
	}
	l2 := Compute(m, SampleViews()[0])
	l2.H = 40
	if len(Clipped(m, l2)) == 0 {
		t.Fatal("items outside the client area must be reported")
	}
}

func TestWrapBreaksAPathThatIsTooWide(t *testing.T) {
	m := fake(15)
	for _, ln := range wrap(m, strings.Repeat("y", 100), 200, false) {
		if m.Measure(ln, false) > 200 {
			t.Fatalf("%q is too wide", ln)
		}
	}
}

// TRAY10: a line wider than its box is flagged, and so is a measure that answers 0 for text.
func TestClippedFlagsALineWiderThanItsBoxAndAFailedMeasure(t *testing.T) {
	m := fake(15)
	l := Compute(m, View{State: StateInstalling, Heading: "Installing 0.1.20", Body: []string{"Short."}, Bar: BarDeterminate, Percent: 0,
		Buttons: []Button{{BtnClose, "Hide", true}}})
	if bad := Clipped(m, l); len(bad) > 0 {
		t.Fatalf("clean layout flagged: %v", bad)
	}
	for i, it := range l.Items {
		if it.Kind == KindBody {
			l.Items[i].Lines = []string{strings.Repeat("w", 80)}
			l.Items[i].Rect.Max.Y = l.Items[i].Rect.Min.Y + 100
		}
	}
	if bad := Clipped(m, l); len(bad) == 0 {
		t.Fatal("a line wider than its box was not flagged")
	}
	zero := Metrics{Unit: 15, Measure: func(string, bool) int { return 0 }}
	if bad := Clipped(zero, Compute(m, View{State: StateInstalling, Heading: "H", Buttons: []Button{{BtnClose, "Hide", true}}})); len(bad) == 0 {
		t.Fatal("a measure of 0 for text was not flagged")
	}
}

// TRAY10: with the failing measure of the bug (0 for everything) the body line cannot wrap,
// so the wrap test below must be run with a real measure; here the real measure wraps it.
func TestInstallingBodyWrapsAtEveryDPI(t *testing.T) {
	body := "This cannot be cancelled. You can hide this window; the update goes on."
	for name, unit := range dpiUnits {
		m := fake(unit)
		l := Compute(m, View{State: StateInstalling, Heading: "Installing 0.1.20", Body: []string{body}, Bar: BarDeterminate, Percent: 0,
			Buttons: []Button{{BtnClose, "Hide", true}}})
		for _, it := range l.Items {
			if it.Kind == KindBody && len(it.Lines) < 2 {
				t.Errorf("%s: body did not wrap: %v", name, it.Lines)
			}
		}
		if bad := Clipped(m, l); len(bad) > 0 {
			t.Errorf("%s: %v", name, bad)
		}
	}
}
