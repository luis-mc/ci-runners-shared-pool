package updatewindow

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func readTokens(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "packages", "ui", "src", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// D16: the colours are the tokens, not hand-typed: the committed Go file is what the generator
// writes from tokens.css now.
func TestPaletteIsGeneratedFromTheTokens(t *testing.T) {
	p, err := ParseTokens(readTokens(t))
	if err != nil {
		t.Fatal(err)
	}
	if p != Phosphor {
		t.Fatalf("tokens_gen.go is stale: run `go run ./apps/controller/internal/updatewindow/genart -go apps/controller/internal/updatewindow/tokens_gen.go`\ntokens: %+v\ncommitted: %+v", p, Phosphor)
	}
	got, err := os.ReadFile("tokens_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, GoSource(p)) {
		t.Fatal("tokens_gen.go differs from the generator output: regenerate it")
	}
	if p.Page != (RGB{0x04, 0x0a, 0x05}) || p.Primary != (RGB{0x4d, 0xff, 0x88}) {
		t.Fatalf("the default theme is Phosphor dark: %+v", p)
	}
}

func TestParseTokensRefusesWhatItCannotRead(t *testing.T) {
	for name, css := range map[string]string{
		"no marker": ":root { --ds-color-page: #000000; }",
		"no block":  "/* themes:start */ nothing",
		"bad hex":   "/* themes:start */\n:root { --ds-color-page: red; }",
	} {
		if _, err := ParseTokens(css); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func decodeBMP(t *testing.T, b []byte) (w, h int, px func(x, y int) RGB) {
	t.Helper()
	if string(b[:2]) != "BM" || binary.LittleEndian.Uint16(b[28:]) != 24 {
		t.Fatal("not a 24-bit BMP")
	}
	w, h = int(binary.LittleEndian.Uint32(b[18:])), int(binary.LittleEndian.Uint32(b[22:]))
	row := (w*3 + 3) &^ 3
	if len(b) != 54+row*h {
		t.Fatalf("size %d, want %d", len(b), 54+row*h)
	}
	return w, h, func(x, y int) RGB {
		o := 54 + (h-1-y)*row + x*3
		return RGB{b[o+2], b[o+1], b[o]}
	}
}

// D12 / D17: the bitmaps have the sizes Windows Installer wants; the text side stays light so
// the stock black text can be read; the art side is the Phosphor page colour.
func TestInstallerArtSizesAndSides(t *testing.T) {
	p := Phosphor
	w, h, px := decodeBMP(t, EncodeBMP(InstallerBanner(p)))
	if w != 493 || h != 58 {
		t.Fatalf("banner %dx%d", w, h)
	}
	if px(5, 5) != (RGB{255, 255, 255}) {
		t.Errorf("the title side of the banner must be light: %+v", px(5, 5))
	}
	art := false
	for x := 300; x < 493 && !art; x++ {
		for y := 0; y < 58; y++ {
			if px(x, y) == p.Primary {
				art = true
				break
			}
		}
	}
	if !art {
		t.Error("no primary-coloured text in the banner block")
	}
	w, h, px = decodeBMP(t, EncodeBMP(InstallerDialog(p)))
	if w != 493 || h != 312 {
		t.Fatalf("dialog %dx%d", w, h)
	}
	if px(300, 150) != (RGB{255, 255, 255}) {
		t.Error("the text side of the dialog bitmap must be light")
	}
	if c := px(3, 3); c == (RGB{255, 255, 255}) || c.G > 0x20 {
		t.Errorf("the strip is the dark page colour (with scanlines): %+v", c)
	}
}

// Scanlines are faint: a scanline pixel differs from the plain page colour only a little.
func TestBandHasFaintScanlinesAndScales(t *testing.T) {
	p := Phosphor
	img := DrawBand(p, 400, 52)
	seen := map[RGB]bool{}
	for y := 0; y < 50; y++ { // not the divider line at the foot
		c := img.NRGBAAt(399, y)
		seen[RGB{c.R, c.G, c.B}] = true
	}
	if len(seen) < 2 {
		t.Fatal("no scanline")
	}
	for c := range seen {
		if int(c.G)-int(p.Page.G) > 40 {
			t.Errorf("scanline too strong: %+v", c)
		}
	}
	for _, h := range []int{52, 78, 104} { // 100, 150, 200%
		b := DrawBand(p, 40*h/2, h)
		if b.Bounds().Dy() != h {
			t.Fatalf("height %d", b.Bounds().Dy())
		}
		found := false
		for y := 0; y < h && !found; y++ {
			for x := 0; x < b.Bounds().Dx(); x++ {
				if c := b.NRGBAAt(x, y); (RGB{c.R, c.G, c.B}) == p.Primary {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("height %d: no name text", h)
		}
	}
}

// Look at the art: UPDATEWINDOW_ART_DIR=dir go test -run Look writes PNG files.
func TestLookAtTheArt(t *testing.T) {
	dir := os.Getenv("UPDATEWINDOW_ART_DIR")
	if dir == "" {
		t.Skip("set UPDATEWINDOW_ART_DIR to write the art as PNG")
	}
	for name, img := range map[string]image.Image{"banner": InstallerBanner(Phosphor), "dialog": InstallerDialog(Phosphor), "band": DrawBand(Phosphor, 560, 56)} {
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		if err := os.WriteFile(filepath.Join(dir, name+".png"), b.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
