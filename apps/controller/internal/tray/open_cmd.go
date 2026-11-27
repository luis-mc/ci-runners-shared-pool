package tray

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// newClient is the client OpenCommand uses; tests replace it so they never reach a real service.
var newClient = NewClient

// OpenCommand is `crsp open` (R5.10): print a one-time sign-in link, and open it
// in a browser when a graphical session exists. Headless Linux only prints.
func OpenCommand(stdout, stderr io.Writer) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url, err := newClient().Link(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "error: could not get a sign-in link:", err, "- is the crsp service running? Check the log folder:", LogDir())
		return 1
	}
	_, _ = fmt.Fprintln(stdout, url)
	if headless() {
		return 0
	}
	_ = OSLauncher(url)
	return 0
}

func headless() bool {
	return runtimeGOOS() == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
}
