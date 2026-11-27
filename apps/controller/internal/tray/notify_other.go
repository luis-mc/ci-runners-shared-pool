//go:build !linux && !darwin && !windows

package tray

// DesktopNotice is a no-op on this OS.
func DesktopNotice(string, string) {}
