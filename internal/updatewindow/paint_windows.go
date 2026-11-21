//go:build windows

package updatewindow

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"
)

// fonts are the two faces of a DPI. The design system's font is monospace; Consolas ships with
// every supported Windows.
type fonts struct {
	regular, bold uintptr
	unit          int     // the height of one line of text in pixels: the unit of the layout
	avgW          int     // the average character width in pixels: the fallback of Measure
	dc            uintptr // the measuring DC the fonts own for their whole life, freed in free()
}

const fontPoints = 10

// newFonts builds the faces of a DPI and a measuring DC of its own (compatible with the screen).
// The window and the layout check both build fonts here, so the check measures what the window
// measures. The fonts never keep a DC they do not own: a released DC makes
// GetTextExtentPoint32 fail and every width 0 (TRAY10).
func newFonts(dpi int) *fonts {
	screen, _, _ := pGetDC.Call(0)
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	_, _, _ = pReleaseDC.Call(0, screen)
	mk := func(weight int) uintptr {
		h := -mulDiv(fontPoints, dpi, 72)
		f, _, _ := pCreateFont.Call(uintptr(int32(h)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE_QUALITY*/, 0x31 /*FIXED_PITCH|FF_MODERN*/, uintptr(unsafe.Pointer(wstr("Consolas"))))
		return f
	}
	f := &fonts{regular: mk(fwNormal), bold: mk(fwBold), dc: dc}
	old, _, _ := pSelectObject.Call(dc, f.regular)
	var tm textMetric
	_, _, _ = pGetTextMetrics.Call(dc, ptr(&tm))
	_, _, _ = pSelectObject.Call(dc, old)
	f.unit = int(tm.Height)
	if f.unit < 8 {
		f.unit = 8
	}
	f.avgW = int(tm.AveCharWidth)
	if f.avgW < 1 {
		f.avgW = max(1, f.unit*6/10)
	}
	return f
}

func (f *fonts) free() {
	_, _, _ = pDeleteObject.Call(f.regular)
	_, _, _ = pDeleteObject.Call(f.bold)
	if f.dc != 0 {
		_, _, _ = pDeleteDC.Call(f.dc)
		f.dc = 0
	}
}

// metrics measures with the real faces, so the layout is the layout of the screen.
func (f *fonts) metrics() Metrics {
	return Metrics{Unit: f.unit, Measure: func(text string, bold bool) int {
		face := f.regular
		if bold {
			face = f.bold
		}
		old, _, _ := pSelectObject.Call(f.dc, face)
		defer func() { _, _, _ = pSelectObject.Call(f.dc, old) }()
		u := utf16.Encode([]rune(text))
		if len(u) == 0 {
			return 0
		}
		var sz struct{ CX, CY int32 }
		_, _, _ = pGetTextExtentPoint32.Call(f.dc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), ptr(&sz))
		return int(sz.CX)
	}}
}

// uiState is what the window adds to a view when it draws: focus, hover, press, busy buttons.
type uiState struct {
	focus, hot, down int // button indexes, -1 for none
	busy             map[ButtonID]string
	tick             int // moves the indeterminate bar
}

func newUI() uiState { return uiState{focus: -1, hot: -1, down: -1} }

func text(dc uintptr, x, y int, s string, c RGB, face uintptr) {
	if s == "" {
		return
	}
	old, _, _ := pSelectObject.Call(dc, face)
	_, _, _ = pSetTextColor.Call(dc, rgb(c))
	_, _, _ = pSetBkMode.Call(dc, transparent)
	u := utf16.Encode([]rune(s))
	_, _, _ = pTextOut.Call(dc, uintptr(int32(x)), uintptr(int32(y)), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)))
	_, _, _ = pSelectObject.Call(dc, old)
}

func toRect(r image.Rectangle) rect {
	return rect{int32(r.Min.X), int32(r.Min.Y), int32(r.Max.X), int32(r.Max.Y)}
}

// blit draws an image at (x, y) with SetDIBitsToDevice (top-down 32 bit).
func blit(dc uintptr, x, y int, img *image.NRGBA) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	pix := make([]byte, w*h*4)
	for i := 0; i < w*h; i++ {
		p := img.Pix[i*4 : i*4+4]
		pix[i*4], pix[i*4+1], pix[i*4+2], pix[i*4+3] = p[2], p[1], p[0], 0xff
	}
	bi := bitmapInfo{Size: uint32(unsafe.Sizeof(bitmapInfo{})), Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	_, _, _ = pSetDIBitsToDevice.Call(dc, uintptr(int32(x)), uintptr(int32(y)), uintptr(w), uintptr(h), 0, 0, 0, uintptr(h), uintptr(unsafe.Pointer(&pix[0])), ptr(&bi), 0)
}

// drawLayout paints a layout of a view onto dc. The same code draws the window, the
// screenshots and the layout check.
func drawLayout(dc uintptr, f *fonts, pal Palette, l Layout, v View, ui uiState) {
	fillRectColor(dc, rect{0, 0, int32(l.W), int32(l.H)}, pal.Page)
	u := l.Unit
	px := max(1, u/12) // a hairline in pixels
	btn := 0
	for _, it := range l.Items {
		switch it.Kind {
		case KindBand:
			blit(dc, 0, 0, DrawBand(pal, it.Rect.Dx(), it.Rect.Dy()))
		case KindHeading:
			c := pal.Primary
			switch v.Tone {
			case ToneDone:
				c = pal.Done
			case ToneProblem:
				c = pal.Problem
			}
			for i, ln := range it.Lines {
				text(dc, it.Rect.Min.X, it.Rect.Min.Y+i*tenths(u, headUnits), ln, c, f.bold)
			}
		case KindBody:
			for i, ln := range it.Lines {
				text(dc, it.Rect.Min.X, it.Rect.Min.Y+i*tenths(u, lineUnits), ln, pal.Text, f.regular)
			}
		case KindBarLabel:
			text(dc, it.Rect.Min.X, it.Rect.Min.Y, it.Lines[0], pal.TextMuted, f.regular)
		case KindBar:
			fillRectColor(dc, toRect(it.Rect), pal.Border)
			inner := it.Rect.Inset(px)
			fillRectColor(dc, toRect(inner), pal.Surface)
			switch v.Bar {
			case BarDeterminate:
				w := inner.Dx() * max(0, min(v.Percent, 100)) / 100
				fillRectColor(dc, toRect(image.Rect(inner.Min.X, inner.Min.Y, inner.Min.X+w, inner.Max.Y)), pal.Primary)
			case BarIndeterminate:
				bw := inner.Dx() / 4
				span := inner.Dx() - bw
				pos := 0
				if span > 0 {
					// back and forth
					t := ui.tick % (2 * span)
					if t > span {
						t = 2*span - t
					}
					pos = t
				}
				fillRectColor(dc, toRect(image.Rect(inner.Min.X+pos, inner.Min.Y, inner.Min.X+pos+bw, inner.Max.Y)), pal.Primary)
			}
		case KindDivider:
			fillRectColor(dc, toRect(it.Rect), pal.Divider)
		case KindButton:
			drawButton(dc, f, pal, it, v.Buttons[btn], btn, ui, px)
			btn++
		}
	}
}

func drawButton(dc uintptr, f *fonts, pal Palette, it Item, b Button, i int, ui uiState, px int) {
	label := it.Label
	busy := false
	if s, ok := ui.busy[b.ID]; ok {
		label, busy = s, true
	}
	r := it.Rect
	fg, bg, edge := pal.Text, pal.Page, pal.Border
	switch {
	case busy:
		fg, bg, edge = pal.TextMuted, pal.Page, pal.Divider
	case b.Primary:
		fg, bg, edge = pal.OnPrimary, pal.Primary, pal.Primary
		if ui.down == i {
			bg, edge = pal.Done, pal.Done
		}
	case ui.down == i:
		bg, edge = pal.Selection, pal.Primary
	case ui.hot == i:
		bg, edge = pal.Selection, pal.Border
	}
	fillRectColor(dc, toRect(r), edge)
	fillRectColor(dc, toRect(r.Inset(px)), bg)
	m := f.metrics()
	tw := m.Measure(label, false)
	text(dc, r.Min.X+(r.Dx()-tw)/2, r.Min.Y+(r.Dy()-f.unit)/2, label, fg, f.regular)
	if ui.focus == i && !busy {
		// the focus ring: the focus token, drawn just inside the button
		ring := max(2, f.unit/5)
		in := r.Inset(px + max(1, ring/2))
		for _, e := range []image.Rectangle{
			image.Rect(in.Min.X, in.Min.Y, in.Max.X, in.Min.Y+ring/2+1),
			image.Rect(in.Min.X, in.Max.Y-ring/2-1, in.Max.X, in.Max.Y),
			image.Rect(in.Min.X, in.Min.Y, in.Min.X+ring/2+1, in.Max.Y),
			image.Rect(in.Max.X-ring/2-1, in.Min.Y, in.Max.X, in.Max.Y),
		} {
			col := pal.Focus
			if b.Primary {
				col = pal.OnPrimary
			}
			fillRectColor(dc, toRect(e), col)
		}
	}
}

// renderDIB draws a layout into a memory bitmap and returns it as an image (the screenshots).
func renderDIB(f *fonts, pal Palette, l Layout, v View, ui uiState) *image.NRGBA {
	screen, _, _ := pGetDC.Call(0)
	defer func() { _, _, _ = pReleaseDC.Call(0, screen) }()
	dc, _, _ := pCreateCompatibleDC.Call(screen)
	defer func() { _, _, _ = pDeleteDC.Call(dc) }()
	bi := bitmapInfo{Size: uint32(unsafe.Sizeof(bitmapInfo{})), Width: int32(l.W), Height: -int32(l.H), Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bmp, _, _ := pCreateDIBSection.Call(dc, ptr(&bi), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return nil
	}
	defer func() { _, _, _ = pDeleteObject.Call(bmp) }()
	old, _, _ := pSelectObject.Call(dc, bmp)
	defer func() { _, _, _ = pSelectObject.Call(dc, old) }()
	drawLayout(dc, f, pal, l, v, ui)
	src := unsafe.Slice((*byte)(bits), l.W*l.H*4)
	out := image.NewNRGBA(image.Rect(0, 0, l.W, l.H))
	for i := 0; i < l.W*l.H; i++ {
		out.Pix[i*4], out.Pix[i*4+1], out.Pix[i*4+2], out.Pix[i*4+3] = src[i*4+2], src[i*4+1], src[i*4], 0xff
	}
	return out
}

// layoutCheck is `crsp update-window --layout-check` (like comics-organizer's -LayoutCheck): it
// builds the window layout, off screen, for every state at 100, 150 and 200% (and 125%), with
// the real fonts. It exits 1 when anything is clipped; --screenshots also writes PNG files.
func layoutCheck(c Config, o Options) int {
	if c.Shots != "" {
		if err := os.MkdirAll(c.Shots, 0o750); err != nil {
			say(o.Stderr, "error: "+err.Error())
			return 1
		}
	}
	bad := 0
	for _, dpi := range []int{96, 120, 144, 192} {
		f := newFonts(dpi)
		m := f.metrics()
		for _, v := range SampleViews() {
			l := Compute(m, v)
			problems := Clipped(m, l)
			say(o.Stdout, fmt.Sprintf("%3d%%  %-17s %4dx%-4d unit %2d px  %s", dpi*100/96, v.State, l.W, l.H, f.unit, okWord(problems)))
			for _, p := range problems {
				say(o.Stdout, "      clipped: "+p)
				bad++
			}
			if c.Shots != "" {
				ui := newUI()
				ui.focus = indexOf(v, v.Default)
				if img := renderDIB(f, Phosphor, l, v, ui); img != nil {
					var b bytes.Buffer
					_ = png.Encode(&b, img)
					name := fmt.Sprintf("update-%s-%d.png", strings.ReplaceAll(string(v.State), "_", "-"), dpi*100/96)
					_ = os.WriteFile(filepath.Join(c.Shots, name), b.Bytes(), 0o600)
				} else {
					say(o.Stderr, "could not draw "+string(v.State))
					bad++
				}
			}
		}
		f.free()
	}
	if bad > 0 {
		say(o.Stderr, fmt.Sprintf("layout check: %d problem(s)", bad))
		return 1
	}
	say(o.Stdout, "layout check: nothing is clipped")
	return 0
}

func okWord(p []string) string {
	if len(p) == 0 {
		return "ok"
	}
	return "CLIPPED"
}

func indexOf(v View, id ButtonID) int {
	for i, b := range v.Buttons {
		if b.ID == id {
			return i
		}
	}
	return -1
}
