//go:build windows

package agent

import "golang.org/x/sys/windows"

// Elevated reports whether the process token is elevated (an Administrator
// PowerShell, not the same user without the elevation).
func Elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
