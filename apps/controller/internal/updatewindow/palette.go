// Package updatewindow is the native update window of the tray on Windows (TRAY5, decision
// tray D12 to D17): one window for the whole update, drawn in the Phosphor dark tokens, and the
// art of the MSI dialogs. The state machine, the layout and the art are plain Go that runs
// on every OS and is tested there; only window_windows.go touches Win32.
package updatewindow

import "image/color"

// RGB is one colour of a design token.
type RGB struct{ R, G, B uint8 }

// NRGBA is the colour as an opaque image colour.
func (c RGB) NRGBA() color.NRGBA { return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xff} }

// COLORREF is the Win32 colour value (0x00BBGGRR).
func (c RGB) COLORREF() uint32 { return uint32(c.R) | uint32(c.G)<<8 | uint32(c.B)<<16 }

// Palette is the part of the Phosphor dark theme the window and the installer art use. The
// values come from packages/ui/src/tokens.css through the generator (tokens_gen.go); nobody
// types a colour here.
type Palette struct {
	Page, Surface, Text, TextMuted, Border, Divider RGB
	Primary, OnPrimary, Focus, Selection            RGB
	Done, Problem, NeedsYou                         RGB
}
