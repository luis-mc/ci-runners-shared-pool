// Command geniconcmd writes the program icon (the healthy CRSP disc, 16 to 256 px)
// from the tray's own drawing code, and with a second argument the same disc as a 64 px PNG
// for the console's tab icon (TRAY5 D18). Run by go generate in cmd/crsp.
package main

import (
	"bytes"
	"fmt"
	"image/png"
	"os"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: geniconcmd <out.ico> [<out.png>]")
		os.Exit(2)
	}
	if err := os.WriteFile(os.Args[1], tray.AppICO(), 0o644); err != nil { //nolint:gosec // a build output
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 3 {
		var b bytes.Buffer
		if err := png.Encode(&b, tray.DrawIcon(64, tray.DiscColour(tray.StateHealthy))); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], b.Bytes(), 0o644); err != nil { //nolint:gosec // a build output
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
