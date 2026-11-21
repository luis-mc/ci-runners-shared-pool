//go:build windows

package updatewindow

import "testing"

// TRAY10: the window builds fonts with newFonts, which owns its measuring DC. Measure must
// answer for text, and the sample body line must wrap, at 100, 150 and 200 %.
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
			if v.State == StateInstalling {
				for _, it := range l.Items {
					if it.Kind == KindBody && len(it.Lines) < 2 {
						t.Errorf("%d dpi: installing body did not wrap: %v", dpi, it.Lines)
					}
				}
			}
		}
		f.free()
	}
}
