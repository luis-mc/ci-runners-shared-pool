//go:build windows

package tray

// OSFirstStart does nothing on Windows: the installer is its first-start dialog (D20 covers macOS and Linux).
func OSFirstStart(string) {}
