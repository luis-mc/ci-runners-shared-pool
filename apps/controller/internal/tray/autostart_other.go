//go:build !windows

package tray

// DefaultAutostart returns the LaunchAgent writer on macOS and the XDG writer elsewhere.
func DefaultAutostart(exe string) (Autostart, error) {
	if runtimeGOOS() == "darwin" {
		return NewLaunchAgent(exe)
	}
	return NewXDGAutostart(exe)
}
