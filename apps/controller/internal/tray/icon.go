package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

const iconSize = 32

// IconSizes are the sizes in the tray icon and in the Windows app icon.
var IconSizes = []int{16, 24, 32, 48, 64, 128, 256}

// trayIconSizes are the sizes the tray icon of the running tray carries.
var trayIconSizes = []int{16, 24, 32}

// Disc colours per state (decision tray D8). Only the colour changes; the
// disc and the letters CRSP are always the same. The words in the tooltip and
// the menu header name the state, so colour is never the only signal (UX-26).
var (
	colourHealthy  = color.NRGBA{R: 0x1a, G: 0x7f, B: 0x37, A: 0xff} // green
	colourWaiting  = color.NRGBA{R: 0x9a, G: 0x67, B: 0x00, A: 0xff} // amber
	colourFailed   = color.NRGBA{R: 0xc6, G: 0x28, B: 0x28, A: 0xff} // red
	colourStopped  = color.NRGBA{R: 0x4b, G: 0x55, B: 0x63, A: 0xff} // slate grey
	colourStarting = color.NRGBA{R: 0x0b, G: 0x5c, B: 0xc2, A: 0xff} // blue
)

// DiscColour is the disc colour of a state.
func DiscColour(s State) color.NRGBA {
	switch s {
	case StateHealthy:
		return colourHealthy
	case StateWaiting:
		return colourWaiting
	case StateFailed:
		return colourFailed
	case StateStopped:
		return colourStopped
	default:
		return colourStarting
	}
}

// glyphs is a 3x5 bitmap font for the letters of the name: C R S P.
var glyphs = map[byte][5]string{
	'C': {"###", "#..", "#..", "#..", "###"},
	'R': {"##.", "#.#", "##.", "#.#", "#.#"},
	'S': {".##", "#..", ".#.", "..#", "##."},
	'P': {"##.", "#.#", "##.", "#..", "#.."},
}

// The text is two lines, "CR" over "SP", so the letters stay legible at 16 px.
// Font units: width 7 (3 + 1 gap + 3), height 11 (5 + 1 gap + 5).
var textRows = [2]string{"CR", "SP"}

// textLayout gives the size of one font unit in pixels and the top-left of the
// text block. Where the unit is a whole number of pixels (16, 32, 48 ...) the
// block sits on the pixel grid, so the letters are sharp.
func textLayout(size int) (unit, x0, y0 float64) {
	unit = float64(size) * 0.062
	if size%16 == 0 {
		unit = float64(size / 16)
		x0 = float64((size - int(7*unit)) / 2)
		y0 = float64((size - int(11*unit)) / 2)
		return
	}
	return unit, (float64(size) - 7*unit) / 2, (float64(size) - 11*unit) / 2
}

// textOn reports whether the point (px, py), in pixels, is on a letter.
func textOn(px, py, unit, x0, y0 float64) bool {
	fx := (px - x0) / unit
	fy := (py - y0) / unit
	if fx < 0 || fy < 0 || fx >= 7 || fy >= 11 {
		return false
	}
	row, ry := 0, int(fy)
	if ry >= 6 {
		row, ry = 1, ry-6
	} else if ry == 5 {
		return false
	}
	col, cx := 0, int(fx)
	if cx >= 4 {
		col, cx = 1, cx-4
	} else if cx == 3 {
		return false
	}
	g := glyphs[textRows[row][col]]
	return g[ry][cx] == '#'
}

// DrawIcon draws the disc with the letters CRSP at size x size.
func DrawIcon(size int, disc color.NRGBA) *image.NRGBA {
	const ss = 4 // samples per axis for smooth edges
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	unit, x0, y0 := textLayout(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var d, t int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					du, dv := px/float64(size)-0.5, py/float64(size)-0.5
					if du*du+dv*dv <= 0.485*0.485 {
						d++
						if textOn(px, py, unit, x0, y0) {
							t++
						}
					}
				}
			}
			if d == 0 {
				continue
			}
			n := ss * ss
			// Blend the letters (white) over the disc, then apply the disc's coverage as alpha.
			mix := func(c uint8) uint8 {
				return uint8((int(c)*(n-t) + 255*t + n/2) / n)
			}
			img.SetNRGBA(x, y, color.NRGBA{R: mix(disc.R), G: mix(disc.G), B: mix(disc.B), A: uint8((255*d + n/2) / n)})
		}
	}
	return img
}

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// IconPNG draws the icon of a state: the same disc with CRSP in it, in the colour of the state (tray D8).
func IconPNG(s State) []byte { return pngBytes(DrawIcon(iconSize, DiscColour(s))) }

// ICOSizes wraps one PNG per size in an ICO container.
func ICOSizes(disc color.NRGBA, sizes []int) []byte {
	pngs := make([][]byte, len(sizes))
	for i, n := range sizes {
		pngs[i] = pngBytes(DrawIcon(n, disc))
	}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	off := 6 + 16*len(sizes)
	for i, n := range sizes {
		dim := byte(n)
		if n >= 256 {
			dim = 0
		}
		b.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&b, binary.LittleEndian, [2]uint16{1, 32})
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(pngs[i])))
		_ = binary.Write(&b, binary.LittleEndian, uint32(off))
		off += len(pngs[i])
	}
	for _, p := range pngs {
		b.Write(p)
	}
	return b.Bytes()
}

// AppICO is the multi-size icon of the program (healthy disc): the icon of crsp.exe and of the MSI.
func AppICO() []byte { return ICOSizes(colourHealthy, IconSizes) }

// IconICO is the tray icon of a state in an ICO container; Windows needs ICO for tray icons.
func IconICO(s State) []byte { return ICOSizes(DiscColour(s), trayIconSizes) }

// Icon returns the bytes systray wants on this OS.
func Icon(s State) []byte {
	if runtimeGOOS() == "windows" {
		return IconICO(s)
	}
	return IconPNG(s)
}
