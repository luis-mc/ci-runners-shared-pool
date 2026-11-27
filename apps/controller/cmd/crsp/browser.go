package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// openBrowser opens url in the default browser when a desktop session exists.
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.CommandContext(context.Background(), "rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.CommandContext(context.Background(), "open", url).Start()
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return errors.New("no desktop session")
		}
		return exec.CommandContext(context.Background(), "xdg-open", url).Start()
	}
}
