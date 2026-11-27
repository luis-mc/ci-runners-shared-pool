// Command genart builds the update window's colours and the installer art from the design
// tokens (TRAY5 D12, D16, D17). Nothing here holds a hand-picked colour.
//
//	genart -tokens packages/ui/src/tokens.css -go internal/updatewindow/tokens_gen.go
//	genart -tokens packages/ui/src/tokens.css -bmp installer/out/art
//
// The installer build runs the second form: it writes WixUIBannerBmp (493x58) and
// WixUIDialogBmp (493x312) as 24-bit BMP files.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/updatewindow"
)

func main() {
	tokens := flag.String("tokens", "packages/ui/src/tokens.css", "the design tokens")
	goOut := flag.String("go", "", "write the palette Go file here")
	bmpDir := flag.String("bmp", "", "write banner.bmp and dialog.bmp into this folder")
	flag.Parse()
	if *goOut == "" && *bmpDir == "" {
		fmt.Fprintln(os.Stderr, "usage: genart -tokens tokens.css [-go tokens_gen.go] [-bmp dir]")
		os.Exit(2)
	}
	css, err := os.ReadFile(*tokens)
	if err != nil {
		fail(err)
	}
	p, err := updatewindow.ParseTokens(string(css))
	if err != nil {
		fail(err)
	}
	if *goOut != "" {
		if err := os.WriteFile(*goOut, updatewindow.GoSource(p), 0o644); err != nil { //nolint:gosec // a source file
			fail(err)
		}
	}
	if *bmpDir != "" {
		if err := os.MkdirAll(*bmpDir, 0o750); err != nil {
			fail(err)
		}
		files := map[string][]byte{
			"banner.bmp": updatewindow.EncodeBMP(updatewindow.InstallerBanner(p)),
			"dialog.bmp": updatewindow.EncodeBMP(updatewindow.InstallerDialog(p)),
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(*bmpDir, name), b, 0o644); err != nil { //nolint:gosec // build output
				fail(err)
			}
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genart:", err)
	os.Exit(1)
}
