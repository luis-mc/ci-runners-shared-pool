//go:build windows

package update

import "golang.org/x/sys/windows"

func privileged() bool { return windows.GetCurrentProcessToken().IsElevated() }
