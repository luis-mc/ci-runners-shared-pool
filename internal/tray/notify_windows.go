//go:build windows

package tray

// DesktopNotice shows one notice as a toast. It has no buttons and asks nothing (LC-22).
func DesktopNotice(title, body string) { OSToast(title, body, nil) }
