package updatewindow

import (
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// Sizes of the Windows Installer art (WixUIBannerBmp and WixUIDialogBmp).
const (
	BannerW, BannerH = 493, 58
	DialogW, DialogH = 493, 312
	dialogPanelW     = 164 // the left strip of the dialog bitmap that the stock dialogs leave to the art
	bannerPanelW     = 200 // the right block of the banner; the left stays light for the title text
)

// glyphs is a 5x7 bitmap font for the letters of the product name. The banner and the dialog
// strip spell it in capitals; GDI text in the window uses a real font.
var glyphs = map[rune][7]string{
	'A': {".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
	'D': {"####.", "#...#", "#...#", "#...#", "#...#", "#...#", "####."},
	'E': {"#####", "#....", "#....", "####.", "#....", "#....", "#####"},
	'H': {"#...#", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"},
	'I': {"#####", "..#..", "..#..", "..#..", "..#..", "..#..", "#####"},
	'L': {"#....", "#....", "#....", "#....", "#....", "#....", "#####"},
	'N': {"#...#", "##..#", "##..#", "#.#.#", "#..##", "#..##", "#...#"},
	'O': {".###.", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
	'P': {"####.", "#...#", "#...#", "####.", "#....", "#....", "#...."},
	'R': {"####.", "#...#", "#...#", "####.", "#.#..", "#..#.", "#...#"},
	'S': {".####", "#....", "#....", ".###.", "....#", "....#", "####."},
	'U': {"#...#", "#...#", "#...#", "#...#", "#...#", "#...#", ".###."},
}

// textWidth is the width in pixels of s at the given scale (a 5 pixel glyph and 1 pixel gap per letter).
func textWidth(s string, scale int) int {
	n := 0
	for range s {
		n += 6 * scale
	}
	if n > 0 {
		n -= scale
	}
	return n
}

func drawText(img *image.NRGBA, x, y, scale int, s string, c color.NRGBA) {
	for _, r := range s {
		if g, ok := glyphs[r]; ok {
			for gy, row := range g {
				for gx, ch := range row {
					if ch == '#' {
						fill(img, image.Rect(x+gx*scale, y+gy*scale, x+(gx+1)*scale, y+(gy+1)*scale), c)
					}
				}
			}
		}
		x += 6 * scale
	}
}

func fill(img *image.NRGBA, r image.Rectangle, c color.NRGBA) {
	draw.Draw(img, r.Intersect(img.Bounds()), &image.Uniform{C: c}, image.Point{}, draw.Src)
}

// scanlines lays the faint CRT lines over r: one pixel line in three, in the primary colour at
// the opacity of --ds-scanline-focus (6%).
func scanlines(img *image.NRGBA, r image.Rectangle, p Palette, step int) {
	const alpha = 15 // 6% of 255
	for y := r.Min.Y; y < r.Max.Y; y += 3 * step {
		for yy := y; yy < y+step && yy < r.Max.Y; yy++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				o := img.NRGBAAt(x, yy)
				mix := func(a, b uint8) uint8 { return uint8((int(a)*(255-alpha) + int(b)*alpha) / 255) }
				img.SetNRGBA(x, yy, color.NRGBA{mix(o.R, p.Primary.R), mix(o.G, p.Primary.G), mix(o.B, p.Primary.B), 0xff})
			}
		}
	}
}

func drawIcon(img *image.NRGBA, x, y, size int) {
	ic := tray.DrawIcon(size, tray.DiscColour(tray.StateHealthy))
	draw.Draw(img, image.Rect(x, y, x+size, y+size).Intersect(img.Bounds()), ic, image.Point{}, draw.Over)
}

// DrawBand draws the band of the update window: the crsp icon and the product name in the
// primary colour on the page colour, with scanlines. Text and icon scale with the height h.
func DrawBand(p Palette, w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	fill(img, img.Bounds(), p.Page.NRGBA())
	scale := max(1, h*4/10/7)
	icon := h * 6 / 10
	pad := (h - icon) / 2
	drawIcon(img, pad, pad, icon)
	name := "CI RUNNERS SHARED POOL"
	drawText(img, pad+icon+pad, (h-7*scale)/2, scale, name, p.Primary.NRGBA())
	scanlines(img, img.Bounds(), p, max(1, h/52))
	// the lower edge: the divider token
	fill(img, image.Rect(0, h-max(1, h/52), w, h), p.Divider.NRGBA())
	return img
}

var white = color.NRGBA{0xff, 0xff, 0xff, 0xff}

// InstallerBanner is WixUIBannerBmp (493x58): white where the dialog title is written, and on
// the right a Phosphor block with the icon and the name. The stock dialogs write black text
// over the left part, so that part stays light.
func InstallerBanner(p Palette) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, BannerW, BannerH))
	fill(img, img.Bounds(), white)
	panel := image.Rect(BannerW-bannerPanelW, 0, BannerW, BannerH)
	fill(img, panel, p.Page.NRGBA())
	drawIcon(img, panel.Min.X+10, (BannerH-40)/2, 40)
	x := panel.Min.X + 10 + 40 + 8
	drawText(img, x, 9, 2, "CI RUNNERS", p.Primary.NRGBA())
	drawText(img, x, 9+14+5, 2, "SHARED POOL", p.Primary.NRGBA())
	scanlines(img, panel, p, 1)
	fill(img, image.Rect(0, BannerH-1, BannerW, BannerH), p.Divider.NRGBA())
	return img
}

// InstallerDialog is WixUIDialogBmp (493x312): a Phosphor strip on the left (164 px) with the
// icon and the name; the rest is light for the dialog text.
func InstallerDialog(p Palette) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, DialogW, DialogH))
	fill(img, img.Bounds(), white)
	panel := image.Rect(0, 0, dialogPanelW, DialogH)
	fill(img, panel, p.Page.NRGBA())
	drawIcon(img, (dialogPanelW-96)/2, 36, 96)
	y := 36 + 96 + 24
	for _, line := range []string{"CI RUNNERS", "SHARED", "POOL"} {
		drawText(img, (dialogPanelW-textWidth(line, 2))/2, y, 2, line, p.Primary.NRGBA())
		y += 22
	}
	scanlines(img, panel, p, 1)
	fill(img, image.Rect(dialogPanelW-1, 0, dialogPanelW, DialogH), p.Divider.NRGBA())
	return img
}

// EncodeBMP writes img as a 24-bit bottom-up BMP, the format Windows Installer reads.
func EncodeBMP(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	row := (w*3 + 3) &^ 3
	size := 54 + row*h
	out := make([]byte, size)
	copy(out, "BM")
	binary.LittleEndian.PutUint32(out[2:], uint32(size)) //nolint:gosec // bounded by the image size
	binary.LittleEndian.PutUint32(out[10:], 54)
	binary.LittleEndian.PutUint32(out[14:], 40)
	binary.LittleEndian.PutUint32(out[18:], uint32(w)) //nolint:gosec // positive
	binary.LittleEndian.PutUint32(out[22:], uint32(h)) //nolint:gosec // positive
	binary.LittleEndian.PutUint16(out[26:], 1)
	binary.LittleEndian.PutUint16(out[28:], 24)
	binary.LittleEndian.PutUint32(out[34:], uint32(row*h)) //nolint:gosec // bounded
	for y := 0; y < h; y++ {
		o := 54 + (h-1-y)*row
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out[o+x*3], out[o+x*3+1], out[o+x*3+2] = uint8(bl>>8), uint8(g>>8), uint8(r>>8)
		}
	}
	return out
}
