package updatewindow

import (
	"fmt"
	"image"
	"strings"
	"unicode/utf8"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

// The layout is computed from the content, in units of the font height (the "unit", one text
// line at the current DPI). Nothing has a fixed pixel height, so at 100, 125, 150 and 200%
// nothing is cut off: the same numbers of units come out as more pixels (decision tray D14).

// Metrics is what the layout needs from the font of the window.
type Metrics struct {
	Unit    int                              // the font height in pixels at this DPI
	Measure func(text string, bold bool) int // the width of one line of text in pixels
}

// Kind says what an item of the layout is.
type Kind int

// Kinds of items.
const (
	KindBand Kind = iota
	KindHeading
	KindBody
	KindBar
	KindBarLabel
	KindDivider
	KindButton
)

// Item is one thing the window draws.
type Item struct {
	Kind   Kind
	Rect   image.Rectangle
	Lines  []string // text items: the wrapped lines, one per line height
	Button ButtonID
	Label  string
}

// Layout is the client area of the window and what sits in it.
type Layout struct {
	W, H  int
	Unit  int
	Items []Item
}

// Sizes in tenths of a unit.
const (
	clientUnits  = 360 // the client area is 36 units wide (at least)
	padUnits     = 15
	bandUnits    = 35
	headUnits    = 15 // a heading line
	lineUnits    = 13 // a body line
	paraGap      = 5
	barUnits     = 8
	buttonUnits  = 25
	buttonMinW   = 80
	buttonPadW   = 30 // the text plus this
	buttonGapW   = 10
	sectionGap   = 10
	bottomMargin = 10
)

func tenths(u, n int) int { return (u*n + 5) / 10 }

// Compute lays the view out.
func Compute(m Metrics, v View) Layout {
	u := m.Unit
	pad := tenths(u, padUnits)

	// Buttons first: they can make the window wider than 36 units.
	bw := make([]int, len(v.Buttons))
	need := 0
	for i, b := range v.Buttons {
		bw[i] = max(tenths(u, buttonMinW), m.Measure(b.Label, false)+tenths(u, buttonPadW))
		need += bw[i]
	}
	if n := len(v.Buttons); n > 1 {
		need += (n - 1) * tenths(u, buttonGapW)
	}
	w := max(tenths(u, clientUnits), need+2*pad)
	textW := w - 2*pad
	// Wrap a little narrower than the box: the measured width of a line can differ from the
	// drawn width by a pixel or two, and a long line must wrap, never be cut (TRAY8).
	wrapW := textW - tenths(u, 5)

	l := Layout{W: w, Unit: u}
	add := func(it Item) { l.Items = append(l.Items, it) }
	y := 0

	band := tenths(u, bandUnits)
	add(Item{Kind: KindBand, Rect: image.Rect(0, 0, w, band)})
	y = band + pad

	head := wrap(m, v.Heading, wrapW, true)
	hh := len(head) * tenths(u, headUnits)
	add(Item{Kind: KindHeading, Rect: image.Rect(pad, y, pad+textW, y+hh), Lines: head})
	y += hh + tenths(u, paraGap)

	for i, p := range v.Body {
		lines := wrap(m, p, wrapW, false)
		ph := len(lines) * tenths(u, lineUnits)
		add(Item{Kind: KindBody, Rect: image.Rect(pad, y, pad+textW, y+ph), Lines: lines})
		y += ph
		if i < len(v.Body)-1 {
			y += tenths(u, paraGap)
		}
	}

	if v.Bar != BarNone {
		y += tenths(u, sectionGap)
		barW := textW
		if v.Bar == BarDeterminate {
			lw := m.Measure("100%", false) + tenths(u, sectionGap)
			barW = textW - lw
			add(Item{Kind: KindBarLabel, Rect: image.Rect(pad+textW-lw+tenths(u, sectionGap), y-tenths(u, 2), pad+textW, y+tenths(u, lineUnits)),
				Lines: []string{fmt.Sprintf("%d%%", v.Percent)}})
		}
		bh := tenths(u, barUnits)
		add(Item{Kind: KindBar, Rect: image.Rect(pad, y, pad+barW, y+bh)})
		y += max(bh, tenths(u, lineUnits))
	}

	y += pad
	add(Item{Kind: KindDivider, Rect: image.Rect(0, y, w, y+max(1, u/15))})
	y += max(1, u/15) + tenths(u, sectionGap)
	bh := tenths(u, buttonUnits)
	x := w - pad - need
	for i, b := range v.Buttons {
		add(Item{Kind: KindButton, Rect: image.Rect(x, y, x+bw[i], y+bh), Button: b.ID, Label: b.Label})
		x += bw[i] + tenths(u, buttonGapW)
	}
	y += bh + tenths(u, bottomMargin) + pad/2
	l.H = y
	return l
}

// wrap breaks text into lines no wider than width: at spaces, and inside a word that alone is
// too wide (a file path).
func wrap(m Metrics, text string, width int, bold bool) []string {
	if text == "" {
		return []string{""}
	}
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, word := range strings.Fields(text) {
		try := word
		if cur != "" {
			try = cur + " " + word
		}
		if m.Measure(try, bold) <= width {
			cur = try
			continue
		}
		flush()
		for m.Measure(word, bold) > width && utf8.RuneCountInString(word) > 1 {
			n := utf8.RuneCountInString(word)
			cut := n - 1
			for cut > 1 && m.Measure(string([]rune(word)[:cut]), bold) > width {
				cut--
			}
			lines = append(lines, string([]rune(word)[:cut]))
			word = string([]rune(word)[cut:])
		}
		cur = word
	}
	flush()
	return lines
}

// Clipped lists everything in the layout that does not fit: an item outside the client area,
// a text line wider than its box, text taller than its box, a button label wider than its
// button, two items that overlap. An empty list means nothing is cut off.
func Clipped(m Metrics, l Layout) []string {
	var bad []string
	client := image.Rect(0, 0, l.W, l.H)
	for i, it := range l.Items {
		if !it.Rect.In(client) {
			bad = append(bad, fmt.Sprintf("item %d (%d) %v is outside the client area %v", i, it.Kind, it.Rect, client))
		}
		if it.Kind != KindBand && it.Kind != KindDivider && it.Rect.Max.X > l.W-tenths(l.Unit, padUnits) {
			bad = append(bad, fmt.Sprintf("item %d (%d) %v runs past the right margin %d", i, it.Kind, it.Rect, l.W-tenths(l.Unit, padUnits)))
		}
		switch it.Kind {
		case KindHeading, KindBody, KindBarLabel:
			lh := tenths(l.Unit, lineUnits)
			if it.Kind == KindHeading {
				lh = tenths(l.Unit, headUnits)
			}
			if len(it.Lines)*lh > it.Rect.Dy() {
				bad = append(bad, fmt.Sprintf("item %d: %d lines of %d px do not fit %d px", i, len(it.Lines), lh, it.Rect.Dy()))
			}
			for _, ln := range it.Lines {
				w := m.Measure(ln, it.Kind == KindHeading)
				if ln != "" && w <= 0 {
					bad = append(bad, fmt.Sprintf("item %d: line %q measures %d px (the measure failed)", i, ln, w))
				}
				if w > it.Rect.Dx() {
					bad = append(bad, fmt.Sprintf("item %d: line %q is %d px wide in %d px", i, ln, w, it.Rect.Dx()))
				}
			}
		case KindButton:
			if w := m.Measure(it.Label, false); w+tenths(l.Unit, 10) > it.Rect.Dx() {
				bad = append(bad, fmt.Sprintf("button %q: label %d px in %d px", it.Label, w, it.Rect.Dx()))
			}
			if it.Rect.Dy() < l.Unit*3/2 {
				bad = append(bad, fmt.Sprintf("button %q: %d px is lower than 1.5 lines", it.Label, it.Rect.Dy()))
			}
		}
		if it.Kind == KindBand {
			continue
		}
		for j := i + 1; j < len(l.Items); j++ {
			o := l.Items[j]
			if o.Kind == KindBand || (it.Kind == KindBar && o.Kind == KindBarLabel) || (it.Kind == KindBarLabel && o.Kind == KindBar) {
				continue
			}
			if it.Rect.Overlaps(o.Rect) {
				bad = append(bad, fmt.Sprintf("item %d (%d) overlaps item %d (%d)", i, it.Kind, j, o.Kind))
			}
		}
	}
	return bad
}

// SampleViews are the views the layout check and the screenshots draw: every state, with the
// longest words of each (a long reason, a long log path).
func SampleViews() []View {
	out := make([]View, 0, len(AllStates))
	for _, s := range AllStates {
		m := NewModel("0.1.12", "0.1.13", ModeLive)
		snap := Snapshot{ServiceUp: true, ServiceVersion: "0.1.12", Jobs: 12}
		switch s {
		case StateReview:
			m.Mode = ModeReview
		case StateDownloading:
			snap.Stage = sampleStage("downloading", 42)
		case StateWaiting:
			snap.Stage = sampleStage("waiting_for_jobs", -1)
		case StateWaitingInstaller:
			snap.Stage = sampleStage("waiting_installer", -1)
			snap.Stage.WaitingFor = "ComiclexHome-0.1.11712-x64-with-a-very-long-installer-file-name"
		case StateInstalling:
			snap.Stage = sampleStage("installing", 60)
		case StateRestarting:
			snap.Stage = sampleStage("restarting", -1)
			snap.ServiceUp = false
		case StateDone:
			snap.ServiceVersion = "0.1.13"
			snap.Stage = sampleStage("done", 100)
		case StateFailed:
			snap.Stage = sampleStage("failed", -1)
			snap.Stage.Error = "The new build did not report healthy within 3 minutes, so Windows Installer put the old version back."
			snap.Stage.LogPath = `C:\ProgramData\crsp\logs\msi-update-0.1.13-2026-11-18T14-05-00.log`
		case StateCancelled:
			m.Cancelled()
		case StateUpToDate:
			m.Mode, m.To = ModeReview, "0.1.12"
		}
		m.Observe(snap)
		out = append(out, m.View())
	}
	return out
}

func sampleStage(name string, pc int) *tray.StageInfo {
	return &tray.StageInfo{Name: name, Percent: pc, Version: "0.1.13", StartedBy: "tray"}
}
