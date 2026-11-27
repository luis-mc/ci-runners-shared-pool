//go:build windows

package updatewindow

import "testing"

// TRAY10: the window builds fonts with newFonts, which owns its measuring DC. Measure must
// answer for text, and a long body line must wrap, at 100, 150 and 200 %.
func TestFontsMeasureAfterTheScreenDCIsReleased(t *testing.T) {
	for _, dpi := range []int{96, 144, 192} {
		f := newFonts(dpi) // releases its temporary screen DC inside
		m := f.metrics()
		if w := m.Measure("100%", false); w <= 0 {
			t.Fatalf("%d dpi: Measure(100%%) = %d", dpi, w)
		}
		if w := m.Measure("Installing 0.1.20", true); w <= 0 {
			t.Fatalf("%d dpi: bold Measure = %d", dpi, w)
		}
		for _, v := range SampleViews() {
			l := Compute(m, v)
			if bad := Clipped(m, l); len(bad) > 0 {
				t.Errorf("%d dpi %s: %v", dpi, v.State, bad)
			}
		}
		// The installing body is now one short line ("Version 0.1.13"), so the wrap check
		// uses a body that must wrap: the real measuring path, at this dpi, has to break it.
		long := View{State: StateInstalling, Heading: "Installing 0.1.20", Body: []string{"Windows Installer replaces the service and starts it again; this window waits for it and keeps showing the progress until the new build reports healthy."}, Bar: BarDeterminate, Percent: 0,
			Buttons: []Button{{ID: BtnClose, Label: "Hide", Primary: true}}}
		for _, it := range Compute(m, long).Items {
			if it.Kind == KindBody && len(it.Lines) < 2 {
				t.Errorf("%d dpi: long body did not wrap: %v", dpi, it.Lines)
			}
		}
		f.free()
	}
}
