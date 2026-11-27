//go:build darwin

package tray

import (
	"os/exec"
	"strconv"
)

// DesktopNotice shows one desktop notification.
func DesktopNotice(title, body string) {
	script := "display notification " + strconv.Quote(body) + " with title " + strconv.Quote(title)
	_ = exec.Command("osascript", "-e", script).Run()
}
